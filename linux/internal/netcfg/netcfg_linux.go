//go:build linux

package netcfg

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	// TunnelDevice is the TUN interface the engine creates.
	TunnelDevice = "sharenet"
	// TunnelIPv4 is the address assigned to the tunnel, inside the range
	// reserved for benchmarking so it can never collide with a real network.
	TunnelIPv4 = "198.18.0.1/15"
	// TunnelIPv6 is assigned only when the phone's VPN carries IPv6.
	TunnelIPv6 = "fd00::1/64"
)

// IsRoot reports whether the process can create a TUN device and change routes.
func IsRoot() bool { return os.Geteuid() == 0 }

// Check verifies the Fedora requirements before a session starts.
func Check() error {
	if !IsRoot() {
		return errors.New(
			"ShareNet must run as root to create the virtual network adapter: sudo sharenet connect …",
		)
	}
	if _, err := os.Stat("/dev/net/tun"); err != nil {
		return errors.New(
			"/dev/net/tun is missing. Load the TUN module first: sudo modprobe tun",
		)
	}
	if _, err := exec.LookPath("ip"); err != nil {
		return errors.New("iproute2 is required: sudo dnf install iproute")
	}
	return nil
}

// Cleanup removes anything a previous run left behind. It is safe to call at
// startup and repeatedly.
func Cleanup(ctx context.Context, logf func(string, ...any)) {
	// DNS first: removing the adapter while the resolver still points at it
	// would leave DNS dead until the next resolver restart.
	revertDNS(ctx)

	if _, err := net.InterfaceByName(TunnelDevice); err == nil {
		if err := run(ctx, "ip", "link", "del", "dev", TunnelDevice); err != nil {
			logf("Adapter cleanup warning: %v", err)
		} else {
			logf("Removed leftover ShareNet adapter and routes")
		}
		return
	}

	_ = runQuiet(ctx, "ip", "link", "del", "dev", TunnelDevice)
}

// WaitInterface blocks until the TUN device created by the engine appears.
func WaitInterface(ctx context.Context, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := net.InterfaceByName(name); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("interface %s did not appear", name)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Configure addresses the tunnel, installs the half-default routes, and points
// DNS at the tunnel.
func Configure(ctx context.Context, mtu int, includeIPv6 bool, logf func(string, ...any)) error {
	if err := WaitInterface(ctx, TunnelDevice, 20*time.Second); err != nil {
		return errors.New("the ShareNet virtual adapter did not appear. See the engine log for details")
	}

	_ = runQuiet(ctx, "ip", "addr", "flush", "dev", TunnelDevice)
	if err := run(ctx, "ip", "addr", "add", TunnelIPv4, "dev", TunnelDevice); err != nil {
		return fmt.Errorf("could not address the virtual adapter: %w", err)
	}
	if err := run(ctx, "ip", "link", "set", "dev", TunnelDevice, "mtu", strconv.Itoa(mtu)); err != nil {
		logf("MTU warning: %v", err)
	}
	if err := run(ctx, "ip", "link", "set", "dev", TunnelDevice, "up"); err != nil {
		return fmt.Errorf("could not bring the virtual adapter up: %w", err)
	}

	// Half-default routes cover every address without replacing the physical
	// default route, so cleanup is a single interface delete.
	for _, prefix := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		if err := run(ctx, "ip", "route", "replace", prefix, "dev", TunnelDevice, "metric", "1"); err != nil {
			return fmt.Errorf("could not install the tunnel routes: %w", err)
		}
	}
	logf("IPv4 routes installed")

	if includeIPv6 {
		if err := configureIPv6(ctx); err != nil {
			logf("IPv6 skipped: %v", err)
		} else {
			logf("IPv6 routes installed")
		}
	} else {
		// Leave IPv6 alone, but keep the new link from attracting it.
		_ = runQuiet(ctx, "sysctl", "-qw", "net.ipv6.conf."+TunnelDevice+".disable_ipv6=1")
		logf("IPv6 disabled: the phone's VPN has no IPv6 route")
	}

	configureDNS(ctx, logf)
	return nil
}

func configureIPv6(ctx context.Context) error {
	if err := run(ctx, "ip", "-6", "addr", "add", TunnelIPv6, "dev", TunnelDevice); err != nil {
		return err
	}
	for _, prefix := range []string{"::/1", "8000::/1"} {
		if err := run(ctx, "ip", "-6", "route", "replace", prefix, "dev", TunnelDevice, "metric", "1"); err != nil {
			return err
		}
	}
	return nil
}

// configureDNS hands every query to the tunnel via systemd-resolved's per-link
// DNS plus a routing domain. That is the Linux equivalent of the NRPT rule.
func configureDNS(ctx context.Context, logf func(string, ...any)) {
	if _, err := exec.LookPath("resolvectl"); err != nil {
		logf("DNS warning: resolvectl was not found, so DNS still uses the system resolver and may " +
			"bypass the tunnel. On Fedora run: sudo systemctl enable --now systemd-resolved")
		return
	}

	if err := run(ctx, "resolvectl", "dns", TunnelDevice, "1.1.1.1", "8.8.8.8"); err != nil {
		logf("DNS warning: %v", err)
		return
	}
	// "~." makes this link the resolver's default route, so queries do not
	// leak to the physical network's DNS servers.
	_ = runQuiet(ctx, "resolvectl", "domain", TunnelDevice, "~.")
	_ = runQuiet(ctx, "resolvectl", "default-route", TunnelDevice, "yes")
	_ = runQuiet(ctx, "resolvectl", "flush-caches")
	logf("DNS resolves through the tunnel (systemd-resolved)")
}

func revertDNS(ctx context.Context) {
	if _, err := exec.LookPath("resolvectl"); err != nil {
		return
	}
	_ = runQuiet(ctx, "resolvectl", "revert", TunnelDevice)
	_ = runQuiet(ctx, "resolvectl", "flush-caches")
}

// DefaultRoute is the physical path the kernel uses for internet traffic.
func DefaultRoute(ctx context.Context) (*Route, error) {
	return RouteFor(ctx, net.IPv4(1, 1, 1, 1))
}

// RouteFor inspects the kernel's route to one address.
func RouteFor(ctx context.Context, target net.IP) (*Route, error) {
	out, err := output(ctx, "ip", "-4", "route", "get", target.String())
	if err != nil {
		return nil, err
	}

	route := &Route{}
	fields := strings.Fields(out)
	for i := 0; i+1 < len(fields); i++ {
		switch fields[i] {
		case "dev":
			route.Interface = fields[i+1]
		case "src":
			route.Source = net.ParseIP(fields[i+1])
		case "via":
			route.Gateway = net.ParseIP(fields[i+1])
		}
	}

	if route.Interface == "" || route.Source == nil {
		return nil, fmt.Errorf("could not determine the interface used for %s", target)
	}

	prefix, err := interfacePrefix(ctx, route.Interface, route.Source)
	if err != nil {
		return nil, err
	}
	route.Prefix = prefix
	return route, nil
}

// IsTunnel reports whether an interface name is the tunnel itself.
func IsTunnel(name string) bool { return name == TunnelDevice }

// AddPhoneRoute pins the phone's address to the physical adapter, so the link
// can never be pulled into the tunnel it carries.
func AddPhoneRoute(ctx context.Context, route *Route, phone net.IP) error {
	args := []string{"route", "replace", phone.String() + "/32", "dev", route.Interface}
	if route.Gateway != nil && !route.SameLink(phone) {
		args = append(args, "via", route.Gateway.String())
	}
	_, err := output(ctx, "ip", args...)
	return err
}

// RemovePhoneRoute is best effort: the route dies with the adapter anyway.
func RemovePhoneRoute(ctx context.Context, phone net.IP) {
	if phone == nil {
		return
	}
	_ = runQuiet(ctx, "ip", "route", "del", phone.String()+"/32")
}

func removePhoneRouteBestEffort(ctx context.Context) {
	// Nothing to do: host routes for the phone are removed by their caller,
	// and a stale one only pins one address to a physical adapter.
}

// InterfaceInfo describes one adapter's IPv4 address and network.
func InterfaceInfo(ctx context.Context, name string) (*Route, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, fmt.Errorf("interface %s was not found", name)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}

	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.To4() == nil {
			continue
		}

		source := ipNet.IP.To4()
		prefix, err := interfacePrefix(ctx, name, source)
		if err != nil {
			return nil, err
		}
		return &Route{Interface: name, Source: source, Prefix: prefix}, nil
	}

	return nil, fmt.Errorf("interface %s has no IPv4 address", name)
}

func interfacePrefix(ctx context.Context, name string, source net.IP) (*net.IPNet, error) {
	out, err := output(ctx, "ip", "-o", "-4", "addr", "show", "dev", name)
	if err != nil {
		return nil, err
	}

	fields := strings.Fields(out)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] != "inet" {
			continue
		}
		_, network, err := net.ParseCIDR(fields[i+1])
		if err != nil {
			continue
		}
		return network, nil
	}

	return nil, fmt.Errorf("interface %s has no IPv4 address", name)
}

func run(ctx context.Context, name string, args ...string) error {
	_, err := output(ctx, name, args...)
	return err
}

func runQuiet(ctx context.Context, name string, args ...string) error {
	_, err := output(ctx, name, args...)
	return err
}

func output(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	combined, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(combined))
		if detail == "" {
			detail = err.Error()
		}
		if line := firstLine(detail); line != "" {
			detail = line
		}
		return "", fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), detail)
	}
	return strings.TrimSpace(string(combined)), nil
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

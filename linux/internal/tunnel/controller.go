// Package tunnel orchestrates one ShareNet session, mirroring the Windows app:
// phone link -> loopback SOCKS5 bridge -> tun2socks -> TUN device -> routes.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/adb"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/discovery"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/engine"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/hotspot"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/link"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/logx"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/netcfg"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/socks"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/stats"
)

const (
	// USBMTU keeps every USB packet inside one adb transfer window.
	USBMTU = 1400
	// WiFiMTU uses the full Ethernet MTU; Wi-Fi links fragment safely.
	WiFiMTU = 1500

	// LocalForwardPort is the loopback port adb forwards the phone's link to.
	LocalForwardPort = 31080

	watchdogInterval = 5 * time.Second
	watchdogGrace    = 4 * time.Second
	watchdogStrikes  = 3
)

// State is what the CLI reports about the session.
type State int

const (
	StateStopped State = iota
	StateConnecting
	StateConnected
	StateDisconnecting
	StateError
)

// String names the state for logs.
func (s State) String() string {
	switch s {
	case StateConnecting:
		return "Connecting"
	case StateConnected:
		return "Connected"
	case StateDisconnecting:
		return "Disconnecting"
	case StateError:
		return "Error"
	default:
		return "Stopped"
	}
}

// Controller owns the session lifecycle. One instance serves one CLI run.
type Controller struct {
	log        *logx.Logger
	stats      *stats.Stats
	enginePath string

	mu          sync.Mutex
	state       State
	status      string
	lastErr     error
	linkOptions link.Options

	cleanupMu sync.Mutex
	cancel    context.CancelFunc
	bridge    *socks.Server
	runner    *engine.Runner
	manager   *adb.Manager
	serial    string
	localPort int
	forwarded bool
	phoneIP   net.IP
	transport string
	hotspot   bool
	notifier  func(title, body string, urgent bool)
}

// New builds a controller. enginePath may be empty to search the usual places.
func New(log *logx.Logger, st *stats.Stats, enginePath string) *Controller {
	return &Controller{
		log:        log,
		stats:      st,
		enginePath: enginePath,
		state:      StateStopped,
		status:     "Ready",
	}
}

// State returns the current state, its human-readable status, and the last
// error, if any.
func (c *Controller) State() (State, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state, c.status, c.lastErr
}

// Transport is "usb", "wifi" or empty when nothing has connected yet.
func (c *Controller) Transport() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.transport
}

// Snapshot is the live traffic reading for the window.
func (c *Controller) Snapshot() stats.Snapshot { return c.stats.Snapshot() }

// Stats is the counter set the SOCKS bridge writes to.
func (c *Controller) Stats() *stats.Stats { return c.stats }

// Logs returns the last n log lines for the activity panel.
func (c *Controller) Logs(n int) []logx.Entry { return c.log.Recent(n) }

// Adapters lists the physical interfaces the UI can search on.
func (c *Controller) Adapters(ctx context.Context) ([]netcfg.Adapter, error) {
	return netcfg.Adapters(ctx)
}

// Discover looks for phones on the network the adapter (or the default route)
// uses, without connecting.
func (c *Controller) Discover(ctx context.Context, adapter string) ([]discovery.Phone, error) {
	route, err := adapterRoute(ctx, adapter)
	if err != nil {
		return nil, err
	}
	phones := discovery.Scan(ctx, route.Prefix, route.Source, c.log.Info)
	if len(phones) == 0 {
		return nil, errors.New(
			"no ShareNet phone answered on this network. Start sharing over Wi-Fi in the app, " +
				"join the phone's hotspot, or type the phone's address",
		)
	}
	return phones, nil
}

// SetNotifier installs the desktop notification hook used for connect and
// failure events.
func (c *Controller) SetNotifier(fn func(title, body string, urgent bool)) {
	c.mu.Lock()
	c.notifier = fn
	c.mu.Unlock()
}

// CleanupStale removes what a crashed run left behind, like the Windows app
// does on startup.
func (c *Controller) CleanupStale(ctx context.Context) {
	c.log.Info("Checking for leftovers from a previous run…")
	netcfg.Cleanup(ctx, c.log.Info)
}

// Devices lists the phones attached over USB.
func (c *Controller) Devices(ctx context.Context) ([]adb.Device, error) {
	path, err := adb.Find()
	if err != nil {
		return nil, err
	}

	manager := adb.New(path)
	if err := manager.EnsureServer(ctx); err != nil {
		return nil, err
	}
	return manager.Devices(ctx)
}

// ConnectUSB forwards the phone's link over adb and starts the tunnel.
func (c *Controller) ConnectUSB(ctx context.Context, serial, token string) error {
	if err := c.begin(); err != nil {
		return err
	}
	if err := netcfg.Check(); err != nil {
		c.fail(err)
		return err
	}
	if err := c.connectUSB(ctx, serial, token); err != nil {
		c.fail(err)
		return err
	}
	return nil
}

// ConnectWiFi reaches the phone directly on the local network. With hotspot set
// and no host, it joins the access point the phone opens first.
func (c *Controller) ConnectWiFi(ctx context.Context, host, token, adapter string, find, hotspotJoin bool) error {
	if err := c.begin(); err != nil {
		return err
	}
	if err := netcfg.Check(); err != nil {
		c.fail(err)
		return err
	}
	if err := c.connectWiFi(ctx, host, token, adapter, find, hotspotJoin); err != nil {
		c.fail(err)
		return err
	}
	return nil
}

// Cleanup removes anything a crashed run left behind.
func (c *Controller) Cleanup(ctx context.Context) {
	netcfg.Cleanup(ctx, c.log.Info)
}

// Disconnect tears the session down and restores the machine's networking.
func (c *Controller) Disconnect(ctx context.Context) {
	c.mu.Lock()
	if c.state == StateStopped || c.state == StateDisconnecting {
		c.mu.Unlock()
		return
	}
	c.state = StateDisconnecting
	c.status = "Disconnecting…"
	c.mu.Unlock()

	c.log.Info("Disconnecting…")
	c.cleanup(ctx)

	c.mu.Lock()
	c.state = StateStopped
	c.status = "Ready"
	c.lastErr = nil
	c.mu.Unlock()
	c.log.Info("Disconnected")
}

func (c *Controller) begin() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state == StateConnected || c.state == StateConnecting || c.state == StateDisconnecting {
		return errors.New("ShareNet is already connected or connecting")
	}

	c.state = StateConnecting
	c.status = "Connecting…"
	c.lastErr = nil
	return nil
}

func (c *Controller) fail(err error) {
	c.cleanupMu.Lock()
	defer c.cleanupMu.Unlock()

	c.mu.Lock()
	active := c.state == StateConnected || c.state == StateConnecting
	c.mu.Unlock()
	if !active {
		return
	}

	c.log.Error("%v", err)
	c.cleanupLocked(context.Background())
	c.notify("ShareNet disconnected", err.Error(), true)

	c.mu.Lock()
	c.state = StateError
	c.status = "Not connected"
	c.lastErr = err
	c.mu.Unlock()
}

func (c *Controller) notify(title, body string, urgent bool) {
	c.mu.Lock()
	fn := c.notifier
	c.mu.Unlock()
	if fn == nil {
		return
	}
	go fn(title, body, urgent)
}

func (c *Controller) publishConnected(status string) {
	c.stats.StartSession()

	c.mu.Lock()
	sessionCtx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.state = StateConnected
	c.status = status
	c.lastErr = nil
	options := c.linkOptions
	runner := c.runner
	c.mu.Unlock()

	c.log.Info("%s", status)
	c.log.Info("Check your public IP in another terminal: curl https://ifconfig.me")
	c.notify("ShareNet connected", status, false)

	go c.watchdogLoop(sessionCtx, options)
	go c.statsLoop(sessionCtx)
	go c.monitorEngine(sessionCtx, runner)
	go c.verifyInternet(sessionCtx)
}

func (c *Controller) connectUSB(ctx context.Context, serial, token string) error {
	c.cleanup(ctx)

	adbPath, err := adb.Find()
	if err != nil {
		return err
	}
	manager := adb.New(adbPath)
	c.log.Info("Using adb: %s", adbPath)

	if err := manager.EnsureServer(ctx); err != nil {
		return err
	}

	devices, err := manager.Devices(ctx)
	if err != nil {
		return err
	}
	device, err := chooseDevice(devices, serial)
	if err != nil {
		return err
	}

	answered := false
	for _, remote := range []string{
		"localabstract:sharenet",
		fmt.Sprintf("tcp:%d", link.UsbFallbackPort),
	} {
		c.log.Info("Forwarding the phone link to 127.0.0.1:%d (%s)", LocalForwardPort, remote)
		if err := manager.Forward(ctx, device.Serial, LocalForwardPort, remote); err != nil {
			c.log.Warn("%v", err)
			continue
		}

		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := link.Check(probeCtx, link.Options{
			Host:  "127.0.0.1",
			Port:  LocalForwardPort,
			Token: token,
		})
		cancel()
		if err != nil {
			manager.RemoveForward(ctx, device.Serial, LocalForwardPort)
			c.log.Warn("The phone did not answer over %s: %v", remote, err)
			continue
		}

		answered = true
		break
	}

	if !answered {
		return errors.New(
			"the phone did not answer over USB. Make sure sharing is started in the ShareNet " +
				"app and that USB debugging is allowed, or share over Wi-Fi instead",
		)
	}

	c.mu.Lock()
	c.manager = manager
	c.serial = device.Serial
	c.localPort = LocalForwardPort
	c.forwarded = true
	c.transport = "usb"
	c.mu.Unlock()

	c.log.Info("USB link established with %s", device.DisplayName())

	options := link.Options{
		Host:  "127.0.0.1",
		Port:  LocalForwardPort,
		Token: token,
	}
	if err := c.startDataPath(ctx, options, USBMTU); err != nil {
		return err
	}

	c.publishConnected(fmt.Sprintf("Connected over USB — %s", device.DisplayName()))
	return nil
}

func (c *Controller) connectWiFi(ctx context.Context, host, token, adapter string, find, hotspotJoin bool) error {
	token = link.NormalizeToken(token)
	if token != "" && (len(token) < 4 || len(token) > 64) {
		return errors.New("the password must be 4-64 characters. Leave it empty when the phone has no password")
	}

	c.mu.Lock()
	c.transport = "wifi"
	c.mu.Unlock()

	c.cleanup(ctx)

	// The Windows app looks on the network the PC is already on before it joins
	// the phone's own hotspot, so two devices on one Wi-Fi never need a join.
	if host == "" {
		if find || hotspotJoin {
			if route, err := adapterRoute(ctx, adapter); err == nil {
				c.log.Info("Searching for ShareNet on %s (%s)…", route.Interface, route.Prefix)
				phones := discovery.Scan(ctx, route.Prefix, route.Source, c.log.Info)
				if len(phones) > 0 {
					host = phones[0].IP.String()
					adapter = route.Interface
					c.log.Info("Selected %s", phones[0])
				}
			} else if find {
				return err
			}
		}

		if host == "" && hotspotJoin {
			if !hotspot.Available() {
				return errors.New(
					"joining the phone's hotspot needs NetworkManager: sudo dnf install NetworkManager, " +
						"or type the phone's address instead",
				)
			}

			joined, err := hotspot.JoinNetwork(ctx, token, hotspot.NewOps(token, c.log.Info))
			if err != nil {
				return err
			}
			c.mu.Lock()
			c.hotspot = true
			c.mu.Unlock()

			host = joined.Gateway.String()
			adapter = joined.Interface
		}
	}

	if host == "" {
		if find {
			return errors.New(
				"no ShareNet phone answered on this network. Start sharing over Wi-Fi in the app, " +
					"join the phone's hotspot, or pass the phone's address with --wifi",
			)
		}
		return errors.New("enter the phone's address, or add --find or --hotspot")
	}

	phone := net.ParseIP(host)
	if phone == nil || phone.To4() == nil {
		return errors.New("enter the phone's IPv4 address (for example 192.168.1.20)")
	}

	route, err := netcfg.RouteFor(ctx, phone)
	if err != nil {
		return err
	}
	if netcfg.IsTunnel(route.Interface) {
		c.log.Info("The phone currently routes through the tunnel; clearing stale routes first")
		netcfg.Cleanup(ctx, c.log.Info)
		if route, err = netcfg.RouteFor(ctx, phone); err != nil {
			return err
		}
	}
	if !route.SameLink(phone) {
		return fmt.Errorf(
			"%s is not on the same network as %s (%s)",
			phone, route.Source, route.Prefix,
		)
	}

	if err := netcfg.AddPhoneRoute(ctx, route, phone); err != nil {
		c.log.Warn("Could not pin the phone route: %v", err)
	}
	c.mu.Lock()
	c.phoneIP = phone
	c.mu.Unlock()

	options := link.Options{
		Host:       phone.String(),
		Port:       link.WifiPort,
		Token:      token,
		BindIP:     route.Source,
		BindDevice: route.Interface,
	}

	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	err = link.Check(probeCtx, options)
	cancel()
	if err != nil {
		netcfg.RemovePhoneRoute(ctx, phone)
		c.mu.Lock()
		c.phoneIP = nil
		c.mu.Unlock()
		return fmt.Errorf(
			"could not reach the phone at %s: %w. Check that sharing is on, the password is right, "+
				"and the phone is on the same Wi-Fi network",
			phone, err,
		)
	}

	c.log.Info("Wi-Fi link established with %s over %s", phone, route.Interface)

	if err := c.startDataPath(ctx, options, WiFiMTU); err != nil {
		return err
	}

	c.publishConnected(fmt.Sprintf("Connected over Wi-Fi — %s via %s", phone, route.Interface))
	return nil
}

func (c *Controller) startDataPath(ctx context.Context, options link.Options, mtu int) error {
	includeIPv6 := c.probeIPv6(ctx, options)

	bridge, err := socks.Start(
		func(ctx context.Context) (*link.Link, error) { return link.Dial(ctx, options) },
		func(message string) { c.log.Debug("%s", message) },
		c.stats,
	)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.bridge = bridge
	c.mu.Unlock()
	c.log.Info("Internal SOCKS5 bridge on 127.0.0.1:%d", bridge.Port())

	enginePath, err := engine.Find(c.enginePath)
	if err != nil {
		return err
	}

	// The engine's flags and TUN behaviour are tied to the pinned release, so
	// say when this machine has a different build. It is only a warning: the
	// engine still gets the chance to run.
	if version, versionErr := engine.Version(enginePath); versionErr != nil {
		c.log.Warn("Could not check the tunnel engine version: %v", versionErr)
	} else if version != engine.PinnedVersion {
		c.log.Warn("Tunnel engine %s does not match the pinned %s — refresh it with linux/scripts/fetch-engine.sh", version, engine.PinnedVersion)
	}

	runner, err := engine.Start(
		enginePath,
		netcfg.TunnelDevice,
		bridge.Port(),
		mtu,
		func(line string) { c.log.Debug("%s", line) },
	)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.runner = runner
	c.linkOptions = options
	c.mu.Unlock()
	c.log.Info("Tunnel engine started (%s, MTU %d)", enginePath, mtu)

	configCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	configured := make(chan error, 1)
	go func() { configured <- netcfg.Configure(configCtx, mtu, includeIPv6, c.log.Info) }()

	select {
	case err := <-configured:
		return err
	case <-ctx.Done():
		cancel()
		return ctx.Err()
	case <-runner.Wait():
		// A refused flag or a TUN permission error kills the engine before the
		// adapter exists. Say so instead of waiting out the adapter timeout.
		cancel()
		reason := "the tunnel engine exited before the ShareNet adapter was ready"
		if err := runner.ExitError(); err != nil {
			reason += " (" + err.Error() + ")"
		}
		if tail := runner.Tail(); tail != "" {
			reason += ": " + tail
		}
		return errors.New(reason)
	}
}

// probeIPv6 mirrors the Windows app: the tunnel only carries IPv6 when the
// phone's VPN can actually reach an IPv6 address.
func (c *Controller) probeIPv6(ctx context.Context, options link.Options) bool {
	targets := []link.Address{
		{Host: "2001:4860:4860::8888", Port: 53},
		{Host: "2606:4700:4700::1111", Port: 53},
	}

	for _, target := range targets {
		probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)

		linkConn, err := link.Dial(probeCtx, options)
		if err != nil {
			cancel()
			continue
		}

		stream, err := linkConn.OpenTCP(probeCtx, target.Host, target.Port)
		cancel()
		if err != nil {
			linkConn.Close()
			continue
		}
		stream.Close()
		return true
	}

	return false
}

// verifyInternet mirrors the Windows check: routes and DNS were only just
// installed, so retry a few times instead of warning on the first-query race.
// It warns rather than fails, because the phone's VPN may still be starting.
func (c *Controller) verifyInternet(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(1500 * time.Millisecond):
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			return
		}

		dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		dialer := net.Dialer{}
		conn, err := dialer.DialContext(dialCtx, "tcp", "www.example.com:80")
		cancel()
		if err == nil {
			_ = conn.Close()
			c.log.Info("Connectivity check: TCP through the tunnel succeeded")
			lastErr = nil
			break
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}

	if lastErr != nil {
		c.log.Warn("Connectivity check warning: %v", lastErr)
		c.log.Warn("If it stays like this, check that the phone's VPN is connected")
	}

	var dnsErr error
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			return
		}

		lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		addresses, err := net.DefaultResolver.LookupHost(lookupCtx, "example.com")
		cancel()
		if err == nil && len(addresses) > 0 {
			c.log.Info("DNS check: resolved example.com (%d addresses)", len(addresses))
			dnsErr = nil
			break
		}
		if err == nil {
			err = errors.New("no addresses returned")
		}
		dnsErr = err

		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}

	if dnsErr != nil {
		c.log.Warn("DNS check warning: %v", dnsErr)
	}
}

func (c *Controller) watchdogLoop(ctx context.Context, options link.Options) {
	ticker := time.NewTicker(watchdogInterval)
	defer ticker.Stop()

	strikes := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		probeCtx, cancel := context.WithTimeout(ctx, watchdogGrace)
		err := link.Check(probeCtx, options)
		cancel()

		if err == nil {
			strikes = 0
			continue
		}
		if ctx.Err() != nil {
			return
		}

		strikes++
		c.log.Warn("Phone check failed (%d/%d): %v", strikes, watchdogStrikes, err)
		if strikes >= watchdogStrikes {
			c.fail(errors.New(
				"the phone link was lost. Start sharing again in the app, then reconnect",
			))
			return
		}
	}
}

func (c *Controller) monitorEngine(ctx context.Context, runner *engine.Runner) {
	select {
	case <-runner.Wait():
	case <-ctx.Done():
		return
	}

	c.mu.Lock()
	active := c.runner == runner && c.state == StateConnected
	c.mu.Unlock()
	if !active {
		return
	}

	detail := ""
	if err := runner.ExitError(); err != nil {
		detail = ": " + err.Error()
	}
	c.fail(fmt.Errorf("the tunnel engine stopped unexpectedly%s", detail))
}

func (c *Controller) statsLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	ticks := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		c.stats.Sample()
		ticks++
		if ticks%5 != 0 {
			continue
		}

		snapshot := c.stats.Snapshot()
		c.log.Info(
			"Traffic  down %s (%s)  up %s (%s)",
			stats.FormatBytes(snapshot.Download), stats.FormatRate(snapshot.DownloadRate),
			stats.FormatBytes(snapshot.Upload), stats.FormatRate(snapshot.UploadRate),
		)
	}
}

func (c *Controller) cleanup(ctx context.Context) {
	c.cleanupMu.Lock()
	defer c.cleanupMu.Unlock()
	c.cleanupLocked(ctx)
}

func (c *Controller) cleanupLocked(ctx context.Context) {
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	c.stats.StopSession()

	// DNS and routes first, so nothing is pointing at an adapter that is about
	// to disappear.
	netcfg.Cleanup(ctx, c.log.Info)

	c.mu.Lock()
	runner := c.runner
	c.runner = nil
	bridge := c.bridge
	c.bridge = nil
	manager := c.manager
	serial := c.serial
	localPort := c.localPort
	forwarded := c.forwarded
	c.forwarded = false
	phone := c.phoneIP
	c.phoneIP = nil
	joinedHotspot := c.hotspot
	c.hotspot = false
	c.transport = ""
	c.mu.Unlock()

	if runner != nil {
		runner.Stop(6 * time.Second)
	}
	if bridge != nil {
		_ = bridge.Close()
	}
	if forwarded && manager != nil && serial != "" {
		manager.RemoveForward(ctx, serial, localPort)
	}
	if phone != nil {
		netcfg.RemovePhoneRoute(ctx, phone)
	}
	if joinedHotspot {
		// Last, so the machine keeps a working link until the tunnel is gone.
		hotspot.Leave(ctx, c.log.Info)
	}
}

func chooseDevice(devices []adb.Device, serial string) (adb.Device, error) {
	if len(devices) == 0 {
		return adb.Device{}, errors.New(
			"no Android device is connected over USB. Plug in the phone, enable USB debugging, " +
				"and accept the prompt on the phone",
		)
	}

	usable := make([]adb.Device, 0, len(devices))
	for _, device := range devices {
		if device.Usable() {
			usable = append(usable, device)
		}
	}

	if serial != "" {
		for _, device := range usable {
			if strings.EqualFold(device.Serial, serial) {
				return device, nil
			}
		}
		for _, device := range devices {
			if strings.EqualFold(device.Serial, serial) {
				return adb.Device{}, fmt.Errorf(
					"device %s is not ready (%s). Unlock it and accept the USB debugging prompt",
					serial, device.State,
				)
			}
		}
		return adb.Device{}, fmt.Errorf("no device with serial %s was found", serial)
	}

	if len(usable) > 1 {
		names := make([]string, len(usable))
		for i, device := range usable {
			names[i] = device.DisplayName()
		}
		return adb.Device{}, fmt.Errorf(
			"more than one phone is connected (%s). Choose one with --serial",
			strings.Join(names, ", "),
		)
	}

	if len(usable) == 0 {
		first := devices[0]
		state := strings.ToLower(first.State)
		switch {
		case strings.Contains(state, "unauthorized"):
			return adb.Device{}, errors.New(
				"the phone has not authorized USB debugging. Unlock it and tap Allow",
			)
		case strings.Contains(state, "offline"):
			return adb.Device{}, errors.New(
				"the phone is offline. Unplug the USB cable, plug it back in, and try again",
			)
		case strings.Contains(state, "no permissions"):
			return adb.Device{}, errors.New(
				"Linux cannot open the phone's USB interface. Add a udev rule for Android devices " +
					"(Fedora: sudo dnf install android-tools and check /usr/lib/udev/rules.d/51-android.rules) " +
					"or share over Wi-Fi instead",
			)
		default:
			return adb.Device{}, fmt.Errorf("the phone is not ready (%s)", first.State)
		}
	}

	return usable[0], nil
}

func adapterRoute(ctx context.Context, adapter string) (*netcfg.Route, error) {
	if adapter == "" {
		route, err := netcfg.DefaultRoute(ctx)
		if err != nil {
			return nil, fmt.Errorf("could not find the default network interface: %w", err)
		}
		return route, nil
	}

	if netcfg.IsTunnel(adapter) {
		return nil, errors.New("search on a physical interface, not the ShareNet tunnel")
	}

	route, err := netcfg.InterfaceInfo(ctx, adapter)
	if err != nil {
		return nil, err
	}
	if route.Source == nil {
		return nil, fmt.Errorf("interface %s has no IPv4 address", adapter)
	}
	return route, nil
}

//go:build linux

package hotspot

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/link"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/netcfg"
)

// NewOps returns the nmcli-backed operations for a real machine.
func NewOps(token string, logf func(string, ...any)) Ops {
	return Ops{
		Connect: func(ctx context.Context, ssid, key string) error {
			if current, err := currentSSID(ctx); err == nil && current == ssid {
				logf("Already on %s", ssid)
				return nil
			}
			_, err := run(ctx, 45*time.Second, "device", "wifi", "connect", ssid, "password", key)
			return err
		},
		Route: func(ctx context.Context) (*netcfg.Route, error) {
			return netcfg.DefaultRoute(ctx)
		},
		Verify: func(ctx context.Context, route *netcfg.Route) error {
			if route.Gateway == nil {
				return errors.New("the joined network has no gateway yet")
			}
			probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			return link.Check(probeCtx, link.Options{
				Host:       route.Gateway.String(),
				Port:       link.WifiPort,
				Token:      token,
				BindIP:     route.Source,
				BindDevice: route.Interface,
			})
		},
		Wait: func(ctx context.Context) {
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		},
		Log: logf,
	}
}

// Leave disconnects from the phone's network and forgets the profile, so the
// machine goes back to its usual Wi-Fi.
func Leave(ctx context.Context, logf func(string, ...any)) {
	if _, err := exec.LookPath("nmcli"); err != nil {
		return
	}

	for _, name := range sharenetConnections(ctx) {
		if _, err := run(ctx, 20*time.Second, "connection", "down", "id", name); err != nil {
			logf("Could not leave %s: %v", name, err)
		}
		if _, err := run(ctx, 20*time.Second, "connection", "delete", "id", name); err != nil {
			logf("Could not remove the %s profile: %v", name, err)
			continue
		}
		logf("Left the phone's network (%s)", name)
	}
}

// Available reports whether nmcli is usable on this machine.
func Available() bool {
	_, err := exec.LookPath("nmcli")
	return err == nil
}

func currentSSID(ctx context.Context) (string, error) {
	out, err := run(ctx, 15*time.Second, "-t", "-f", "ACTIVE,SSID", "device", "wifi")
	if err != nil {
		return "", err
	}
	return parseCurrentSSID(out), nil
}

func sharenetConnections(ctx context.Context) []string {
	out, err := run(ctx, 20*time.Second, "-t", "-f", "NAME,TYPE", "connection", "show")
	if err != nil {
		return nil
	}

	var names []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		name, kind := strings.TrimPrefix(parts[0], "*"), parts[1]
		if !strings.HasPrefix(strings.ToLower(name), strings.ToLower(SSIDPrefix)) {
			continue
		}
		if !strings.Contains(kind, "wireless") && !strings.Contains(kind, "802-11") {
			continue
		}
		names = append(names, name)
	}
	return names
}

func run(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "nmcli", args...)
	combined, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(combined))
	if err != nil {
		detail := text
		if detail == "" {
			detail = err.Error()
		}
		return text, fmt.Errorf("nmcli %s: %s", strings.Join(args, " "), firstLine(detail))
	}
	return text, nil
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

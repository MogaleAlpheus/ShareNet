// Package hotspot joins the access point the phone opens, so Wi-Fi needs no
// typed address and no Find press. The PC derives the network name and key from
// the link password, exactly like the Windows app, and proves success with a
// real link handshake on the new default gateway.
package hotspot

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/netcfg"
)

const (
	// SSIDPrefix is what the Android app puts in front of the password hint.
	SSIDPrefix = "ShareNet-"
	// JoinTimeout is how long the phone has to answer after the join.
	JoinTimeout = 25 * time.Second
)

// Join is a successful hotspot connection.
type Join struct {
	SSID      string
	Interface string
	Gateway   net.IP
}

// Ops are the platform commands the portable join loop needs. Tests replace
// them; hotspot_linux.go supplies the real nmcli implementation.
type Ops struct {
	// Connect joins the network. It should return nil once nmcli reports that
	// the station associated.
	Connect func(ctx context.Context, ssid, key string) error
	// Route describes the path to the internet after the join.
	Route func(ctx context.Context) (*netcfg.Route, error)
	// Verify proves the gateway really is the phone.
	Verify func(ctx context.Context, route *netcfg.Route) error
	// Wait pauses between attempts.
	Wait func(ctx context.Context)
	Log  func(string, ...any)
	// Timeout overrides JoinTimeout; tests use a short one.
	Timeout time.Duration
}

// Normalize strips everything but letters and digits, then upper-cases, which is
// what the phone does with the password it shows.
func Normalize(token string) string {
	var builder strings.Builder
	for _, r := range token {
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// SSID derives the phone's network name from the password.
func SSID(token string) (string, error) {
	normalized := Normalize(token)
	if len(normalized) < 4 {
		return "", errors.New("enter the password shown in the ShareNet app on the phone")
	}
	return SSIDPrefix + normalized[:4], nil
}

// Key derives the WPA2 key, which is the normalized password.
func Key(token string) (string, error) {
	normalized := Normalize(token)
	if len(normalized) < 8 {
		return "", errors.New(
			"the phone's hotspot needs the full password (8-63 letters and digits) shown in the app",
		)
	}
	return normalized, nil
}

// parseCurrentSSID reads `nmcli -t -f ACTIVE,SSID device wifi` output and
// returns the network the station is associated with.
func parseCurrentSSID(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 || !strings.EqualFold(strings.TrimSpace(parts[0]), "yes") {
			continue
		}
		return strings.ReplaceAll(parts[1], `\:`, ":")
	}
	return ""
}

// JoinNetwork joins the phone's access point and waits for the handshake.
func JoinNetwork(ctx context.Context, token string, ops Ops) (*Join, error) {
	ssid, err := SSID(token)
	if err != nil {
		return nil, err
	}
	key, err := Key(token)
	if err != nil {
		return nil, err
	}

	ops.Log("Joining the phone's network %s…", ssid)
	if err := ops.Connect(ctx, ssid, key); err != nil {
		return nil, fmt.Errorf("could not join %s: %w", ssid, err)
	}

	timeout := ops.Timeout
	if timeout <= 0 {
		timeout = JoinTimeout
	}
	deadline := time.Now().Add(timeout)
	misses := 0
	for {
		route, err := ops.Route(ctx)
		if err == nil && route != nil && route.Interface != "" {
			gateway := route.Gateway
			if gateway == nil {
				gateway = route.Source
			}
			if gateway != nil {
				if err := ops.Verify(ctx, route); err == nil {
					ops.Log("Joined %s; the phone is %s on %s", ssid, gateway, route.Interface)
					return &Join{SSID: ssid, Interface: route.Interface, Gateway: gateway}, nil
				} else {
					misses++
					if misses <= 2 {
						ops.Log("Waiting for the phone on %s (%v)", route.Interface, err)
					}
				}
			}
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf(
				"the %s network did not answer. Start sharing over Wi-Fi in the app, "+
					"check the password, and try again", ssid,
			)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		ops.Wait(ctx)
	}
}

//go:build !linux

package netcfg

import (
	"context"
	"errors"
	"net"
	"time"
)

// TunnelDevice is the interface name the engine creates on Linux.
const TunnelDevice = "sharenet"

var errUnsupported = errors.New("the ShareNet tunnel only runs on Linux")

// IsRoot reports whether the process can configure the network.
func IsRoot() bool { return false }

// Check reports the platform limitation.
func Check() error { return errUnsupported }

// Cleanup is a no-op off Linux.
func Cleanup(ctx context.Context, logf func(string, ...any)) {}

// WaitInterface is unavailable off Linux.
func WaitInterface(ctx context.Context, name string, timeout time.Duration) error {
	return errUnsupported
}

// Configure is unavailable off Linux.
func Configure(ctx context.Context, mtu int, includeIPv6 bool, logf func(string, ...any)) error {
	return errUnsupported
}

// DefaultRoute is unavailable off Linux.
func DefaultRoute(ctx context.Context) (*Route, error) { return nil, errUnsupported }

// RouteFor is unavailable off Linux.
func RouteFor(ctx context.Context, target net.IP) (*Route, error) { return nil, errUnsupported }

// InterfaceInfo is unavailable off Linux.
func InterfaceInfo(ctx context.Context, name string) (*Route, error) { return nil, errUnsupported }

// IsTunnel reports whether an interface name is the tunnel.
func IsTunnel(name string) bool { return name == TunnelDevice }

// AddPhoneRoute is unavailable off Linux.
func AddPhoneRoute(ctx context.Context, route *Route, phone net.IP) error { return errUnsupported }

// RemovePhoneRoute is a no-op off Linux.
func RemovePhoneRoute(ctx context.Context, phone net.IP) {}

// Adapters is empty off Linux.
func Adapters(ctx context.Context) ([]Adapter, error) { return nil, errUnsupported }

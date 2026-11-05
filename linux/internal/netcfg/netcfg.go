// Package netcfg creates and configures the virtual adapter: addresses,
// half-default routes, the host route back to the phone, and DNS. The Linux
// implementation is in netcfg_linux.go; other platforms only build enough to
// compile the CLI and run the portable tests.
package netcfg

import "net"

// Route is the physical path the kernel already uses to reach an address.
type Route struct {
	// Interface is the physical adapter (for example wlp2s0).
	Interface string
	// Source is the address the kernel would use for the connection.
	Source net.IP
	// Gateway is empty when the target is on the local link.
	Gateway net.IP
	// Prefix is the interface's own network.
	Prefix *net.IPNet
}

// Adapter is one physical interface the UI can offer for Wi-Fi.
type Adapter struct {
	Name     string
	Address  string
	Prefix   string
	Gateway  string
	Wireless bool
}

// Display renders the adapter the way the Windows adapter combo does.
func (a Adapter) Display() string {
	label := a.Name
	if a.Address != "" {
		label += " — " + a.Address
	}
	if a.Wireless {
		label += " (Wi-Fi)"
	}
	return label
}

// SameLink reports whether target is a neighbour on the interface's subnet.
func (r *Route) SameLink(target net.IP) bool {
	if r == nil || r.Prefix == nil || target == nil {
		return false
	}
	return r.Prefix.Contains(target)
}

//go:build linux

package netcfg

import (
	"context"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
)

// Adapters lists the physical interfaces that can carry Wi-Fi, with the address
// and gateway the UI shows in its adapter picker.
func Adapters(ctx context.Context) ([]Adapter, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("could not list network interfaces: %w", err)
	}

	gateways := defaultGateways(ctx)
	adapters := make([]Adapter, 0, len(interfaces))
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if IsTunnel(iface.Name) || strings.HasPrefix(iface.Name, "veth") ||
			strings.HasPrefix(iface.Name, "docker") || strings.HasPrefix(iface.Name, "br-") {
			continue
		}

		route, err := InterfaceInfo(ctx, iface.Name)
		if err != nil || route.Source == nil {
			continue
		}

		adapter := Adapter{
			Name:     iface.Name,
			Address:  route.Source.String(),
			Wireless: isWireless(iface.Name),
			Gateway:  gateways[iface.Name],
		}
		if route.Prefix != nil {
			adapter.Prefix = route.Prefix.String()
		}
		adapters = append(adapters, adapter)
	}

	sort.Slice(adapters, func(i, j int) bool {
		if adapters[i].Wireless != adapters[j].Wireless {
			return adapters[i].Wireless
		}
		return adapters[i].Name < adapters[j].Name
	})
	return adapters, nil
}

func isWireless(name string) bool {
	for _, marker := range []string{"wireless", "phy80211"} {
		if _, err := os.Stat("/sys/class/net/" + name + "/" + marker); err == nil {
			return true
		}
	}
	return false
}

// defaultGateways maps interface name to default gateway.
func defaultGateways(ctx context.Context) map[string]string {
	out, err := output(ctx, "ip", "-4", "route", "show", "default")
	if err != nil {
		return nil
	}

	gateways := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		device, gateway := "", ""
		for i := 0; i+1 < len(fields); i++ {
			switch fields[i] {
			case "dev":
				device = fields[i+1]
			case "via":
				gateway = fields[i+1]
			}
		}
		if device != "" && gateway != "" {
			gateways[device] = gateway
		}
	}
	return gateways
}

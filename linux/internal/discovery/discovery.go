// Package discovery finds a ShareNet phone on the local network the way the
// Windows and Android apps do: listen for the UDP beacon, then probe TCP 1080
// across the subscriber's own subnet.
package discovery

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/link"
)

const (
	// BeaconPrefix is the first field of the phone's presence datagram.
	BeaconPrefix = "SHARENET1"
	// BeaconWindow is how long a scan listens before probing.
	BeaconWindow = 1500 * time.Millisecond
	// MaxCandidates bounds how many phones a scan reports.
	MaxCandidates = 8
)

// Phone is one phone discovered on the local network.
type Phone struct {
	IP    net.IP
	Port  int
	Model string
}

// String renders the phone for the CLI.
func (p Phone) String() string {
	if p.Model != "" {
		return fmt.Sprintf("%s (%s)", p.IP, p.Model)
	}
	return p.IP.String()
}

// Scan looks for phones reachable from this host on subnet. self is the local
// address on that subnet, used to skip probing ourselves.
func Scan(ctx context.Context, subnet *net.IPNet, self net.IP, logf func(string, ...any)) []Phone {
	var (
		mu    sync.Mutex
		found = make(map[string]Phone)
	)

	add := func(phone Phone) {
		key := phone.IP.String()
		mu.Lock()
		defer mu.Unlock()
		if _, exists := found[key]; exists {
			return
		}
		found[key] = phone
		logf("Discovery: found %s", phone)
	}

	beaconCtx, cancel := context.WithTimeout(ctx, BeaconWindow)
	if phones := listenBeacons(beaconCtx); len(phones) > 0 {
		for _, phone := range phones {
			add(phone)
		}
	}
	cancel()

	hosts := probeHosts(subnet, self)
	if len(hosts) > 0 {
		logf("Discovery: probing %d addresses on %s", len(hosts), subnet)
	}

	semaphore := make(chan struct{}, 64)
	var wg sync.WaitGroup
	for _, ip := range hosts {
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		semaphore <- struct{}{}
		go func(ip net.IP) {
			defer wg.Done()
			defer func() { <-semaphore }()

			dialer := net.Dialer{Timeout: 350 * time.Millisecond}
			conn, err := dialer.DialContext(ctx, "tcp4",
				net.JoinHostPort(ip.String(), strconv.Itoa(link.WifiPort)))
			if err != nil {
				return
			}
			_ = conn.Close()
			add(Phone{IP: ip, Port: link.WifiPort})
		}(ip)
	}
	wg.Wait()

	phones := make([]Phone, 0, len(found))
	for _, phone := range found {
		phones = append(phones, phone)
	}
	sort.Slice(phones, func(i, j int) bool {
		return binary.BigEndian.Uint32(phones[i].IP.To4()) < binary.BigEndian.Uint32(phones[j].IP.To4())
	})

	if len(phones) > MaxCandidates {
		phones = phones[:MaxCandidates]
	}
	return phones
}

func listenBeacons(ctx context.Context) []Phone {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: link.BeaconPort})
	if err != nil {
		return nil
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
	}

	var phones []Phone
	buffer := make([]byte, 1500)
	for {
		n, from, err := conn.ReadFromUDP(buffer)
		if err != nil {
			return phones
		}
		if phone, ok := parseBeacon(buffer[:n], from.IP); ok {
			phones = append(phones, phone)
		}
	}
}

// parseBeacon reads the "SHARENET1|port|model" datagram.
func parseBeacon(payload []byte, from net.IP) (Phone, bool) {
	parts := strings.Split(strings.TrimSpace(string(payload)), "|")
	if len(parts) < 2 || !strings.EqualFold(parts[0], BeaconPrefix) {
		return Phone{}, false
	}

	port, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || port < 1 || port > 65535 {
		return Phone{}, false
	}

	model := ""
	if len(parts) > 2 {
		model = strings.TrimSpace(strings.Join(parts[2:], "|"))
	}
	return Phone{IP: from, Port: port, Model: model}, true
}

// probeHosts lists the addresses worth a TCP probe: at most the /24 around the
// local address, so a /16 home network does not turn into 65k dials.
func probeHosts(subnet *net.IPNet, self net.IP) []net.IP {
	if subnet == nil {
		return nil
	}

	base := subnet.IP.To4()
	if base == nil {
		return nil
	}

	ones, bits := subnet.Mask.Size()
	if bits != 32 {
		return nil
	}
	if ones < 24 {
		start := self.To4()
		if start == nil {
			start = base
		}
		base = net.IPv4(start[0], start[1], start[2], 0).To4()
		ones = 24
	}

	size := 1 << (32 - ones)
	if size > 1024 {
		size = 1024
	}
	if size < 4 {
		return nil
	}

	network := binary.BigEndian.Uint32(base)
	hosts := make([]net.IP, 0, size-2)
	for i := 1; i < size-1; i++ {
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, network+uint32(i))
		if self != nil && ip.Equal(self.To4()) {
			continue
		}
		hosts = append(hosts, ip)
	}
	return hosts
}

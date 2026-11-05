//go:build linux

package link

import (
	"errors"
	"net"
	"syscall"
)

// bindToDevice pins a socket to one interface (SO_BINDTODEVICE). Linux uses the
// strong host model, but an explicit bind still keeps a reconnect to the phone
// off the tunnel even when a half-default route covers the address.
func bindToDevice(device string) func(network, address string, c syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		var sockErr error
		if err := c.Control(func(fd uintptr) {
			sockErr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, device)
		}); err != nil {
			return err
		}
		// An unprivileged run may lack CAP_NET_RAW; the source bind and the
		// /32 host route to the phone already keep the link local.
		if errors.Is(sockErr, syscall.EPERM) || errors.Is(sockErr, syscall.EACCES) ||
			errors.Is(sockErr, syscall.ENOPROTOOPT) {
			return nil
		}
		return sockErr
	}
}

// tuneKeepAlive detects a dead link (unplugged cable, dropped Wi-Fi) without
// waiting for a write timeout: 10s idle, 3s interval, 3 probes.
func tuneKeepAlive(conn *net.TCPConn) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return
	}
	_ = raw.Control(func(fd uintptr) {
		f := int(fd)
		_ = syscall.SetsockoptInt(f, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 1)
		_ = syscall.SetsockoptInt(f, syscall.IPPROTO_TCP, syscall.TCP_KEEPIDLE, 10)
		_ = syscall.SetsockoptInt(f, syscall.IPPROTO_TCP, syscall.TCP_KEEPINTVL, 3)
		_ = syscall.SetsockoptInt(f, syscall.IPPROTO_TCP, syscall.TCP_KEEPCNT, 3)
	})
}

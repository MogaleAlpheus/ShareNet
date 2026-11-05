//go:build !linux

package link

import (
	"net"
	"syscall"
)

// Non-Linux builds exist only so the protocol and SOCKS relay can be tested on
// a development machine. There is no tun interface to configure here.

func bindToDevice(string) func(network, address string, c syscall.RawConn) error {
	return nil
}

func tuneKeepAlive(*net.TCPConn) {}

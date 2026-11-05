// Package mockphone is a stand-in for the Android app used by the unit tests.
// It speaks link protocol v1 over TCP and echoes whatever it is asked to relay,
// so the link client and the SOCKS bridge can be tested without a phone.
package mockphone

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/link"
)

// Phone is a running mock link server.
type Phone struct {
	listener net.Listener
	token    string

	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	accepted atomic.Int64
	closed   atomic.Bool
	wg       sync.WaitGroup
}

// Start listens on loopback and requires token for new links ("" accepts none).
func Start(token string) (*Phone, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	p := &Phone{listener: ln, token: link.NormalizeToken(token), conns: make(map[net.Conn]struct{})}
	p.wg.Add(1)
	go p.acceptLoop()
	return p, nil
}

// Host returns the loopback address the mock is listening on.
func (p *Phone) Host() string { return "127.0.0.1" }

// Port returns the mock's listening port.
func (p *Phone) Port() int { return p.listener.Addr().(*net.TCPAddr).Port }

// Options returns link options that reach this mock.
func (p *Phone) Options(token string) link.Options {
	return link.Options{Host: p.Host(), Port: p.Port(), Token: token}
}

// Accepted reports how many link connections the mock has seen.
func (p *Phone) Accepted() int64 { return p.accepted.Load() }

// Close stops the listener and every open link.
func (p *Phone) Close() error {
	if p.closed.Swap(true) {
		return nil
	}
	err := p.listener.Close()

	p.mu.Lock()
	for conn := range p.conns {
		_ = conn.Close()
	}
	p.mu.Unlock()

	p.wg.Wait()
	return err
}

func (p *Phone) acceptLoop() {
	defer p.wg.Done()
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		p.conns[conn] = struct{}{}
		p.mu.Unlock()

		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			defer func() {
				p.mu.Lock()
				delete(p.conns, conn)
				p.mu.Unlock()
				_ = conn.Close()
			}()
			p.serve(conn)
		}()
	}
}

func (p *Phone) serve(conn net.Conn) {
	reader := bufio.NewReader(conn)
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	if err := p.handshake(conn, reader); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})

	mode, err := reader.ReadByte()
	if err != nil {
		return
	}

	switch mode {
	case link.ModeTCP:
		p.serveTCP(conn, reader)
	case link.ModeUDP:
		p.serveUDP(conn, reader)
	}
}

func (p *Phone) handshake(conn net.Conn, reader *bufio.Reader) error {
	var head [6]byte
	if _, err := io.ReadFull(reader, head[:]); err != nil {
		return err
	}
	if binary.BigEndian.Uint32(head[:4]) != link.Magic {
		return errors.New("bad magic")
	}
	if head[4] != link.Version {
		_, _ = conn.Write([]byte{link.StatusBadVersion})
		return errors.New("bad version")
	}

	authMode := head[5]
	switch authMode {
	case link.AuthNone:
		if p.token != "" {
			_, _ = conn.Write([]byte{link.StatusBadAuth})
			return errors.New("token required")
		}
	case link.AuthToken:
		length, err := reader.ReadByte()
		if err != nil {
			return err
		}
		raw := make([]byte, length)
		if _, err := io.ReadFull(reader, raw); err != nil {
			return err
		}
		if string(raw) != p.token {
			_, _ = conn.Write([]byte{link.StatusBadAuth})
			return errors.New("bad token")
		}
	default:
		_, _ = conn.Write([]byte{link.StatusBadRequest})
		return fmt.Errorf("bad auth mode %d", authMode)
	}

	p.accepted.Add(1)
	_, err := conn.Write([]byte{link.StatusOK})
	return err
}

func (p *Phone) serveTCP(conn net.Conn, reader *bufio.Reader) {
	target, err := link.ParseAddress(reader)
	if err != nil {
		return
	}

	// Port 9 is the mock's "unreachable" target.
	if target.Port == 9 {
		_, _ = conn.Write([]byte{link.StatusConnectFailed})
		return
	}
	if _, err := conn.Write([]byte{link.StatusOK}); err != nil {
		return
	}

	buffer := make([]byte, 32*1024)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			if _, werr := conn.Write(buffer[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (p *Phone) serveUDP(conn net.Conn, reader *bufio.Reader) {
	for {
		frame, err := link.ReadUDPFrame(reader)
		if err != nil {
			return
		}
		// Echo the datagram back with the same remote address, like the phone
		// returning a reply from the internet.
		if err := link.WriteUDPFrame(conn, frame.Address, frame.Payload); err != nil {
			return
		}
	}
}

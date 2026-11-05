// Package socks is the internal loopback SOCKS5 bridge. Only the tun2socks
// engine talks to it; applications never see or configure a proxy.
//
// CONNECT opens one phone link per TCP stream. UDP ASSOCIATE opens one link UDP
// association per SOCKS association and forwards datagrams in both directions,
// which is what carries DNS through the tunnel.
package socks

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/link"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/stats"
)

const (
	cmdConnect      = 1
	cmdUDPAssociate = 3

	replySuccess             = 0
	replyFailure             = 1
	replyConnectionRefused   = 5
	replyCommandNotSupported = 7
)

// DialLink opens one fresh phone link. The bridge calls it per TCP stream and
// per UDP association, exactly like the Windows app.
type DialLink func(ctx context.Context) (*link.Link, error)

// Server is a running bridge on 127.0.0.1.
type Server struct {
	listener net.Listener
	dial     DialLink
	logf     func(string)
	stats    *stats.Stats

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

// Start binds the bridge to an ephemeral loopback port and begins accepting.
func Start(dial DialLink, logf func(string), st *stats.Stats) (*Server, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("could not start the SOCKS bridge: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		listener: listener,
		dial:     dial,
		logf:     logf,
		stats:    st,
		ctx:      ctx,
		cancel:   cancel,
		conns:    make(map[net.Conn]struct{}),
	}

	s.wg.Add(1)
	go s.acceptLoop()
	return s, nil
}

// Port is the loopback port the engine should use.
func (s *Server) Port() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

// Close stops accepting, drops every in-flight connection, and waits for the
// handlers to finish.
func (s *Server) Close() error {
	s.cancel()
	err := s.listener.Close()

	s.mu.Lock()
	for conn := range s.conns {
		_ = conn.Close()
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}

	return err
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}

		s.mu.Lock()
		s.conns[conn] = struct{}{}
		s.mu.Unlock()

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() {
				s.mu.Lock()
				delete(s.conns, conn)
				s.mu.Unlock()
				_ = conn.Close()
			}()
			s.handle(conn)
		}()
	}
}

func (s *Server) handle(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
		_ = tcp.SetReadBuffer(1 << 20)
		_ = tcp.SetWriteBuffer(1 << 20)
	}

	reader := bufio.NewReader(conn)
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))

	if err := greet(conn, reader); err != nil {
		return
	}
	command, target, err := request(reader)
	if err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})

	switch command {
	case cmdConnect:
		s.connect(conn, reader, target)
	case cmdUDPAssociate:
		s.associate(conn, target)
	default:
		_ = writeReply(conn, replyCommandNotSupported, nil, 0)
	}
}

func greet(conn net.Conn, reader *bufio.Reader) error {
	head := make([]byte, 2)
	if _, err := io.ReadFull(reader, head); err != nil {
		return err
	}
	if head[0] != 5 {
		return errors.New("not a SOCKS5 client")
	}

	methods := make([]byte, head[1])
	if _, err := io.ReadFull(reader, methods); err != nil {
		return err
	}
	if !bytes.Contains(methods, []byte{0}) {
		_, _ = conn.Write([]byte{5, 0xFF})
		return errors.New("the client requires authentication")
	}

	_, err := conn.Write([]byte{5, 0})
	return err
}

func request(reader *bufio.Reader) (byte, link.Address, error) {
	head := make([]byte, 3)
	if _, err := io.ReadFull(reader, head); err != nil {
		return 0, link.Address{}, err
	}
	if head[0] != 5 || head[2] != 0 {
		return 0, link.Address{}, errors.New("malformed SOCKS5 request")
	}

	target, err := link.ParseAddress(reader)
	if err != nil {
		return 0, link.Address{}, err
	}
	return head[1], target, nil
}

func (s *Server) connect(conn net.Conn, reader *bufio.Reader, target link.Address) {
	linkConn, err := s.dial(s.ctx)
	if err != nil {
		_ = writeReply(conn, replyFailure, nil, 0)
		s.logf(fmt.Sprintf("TCP %s failed: %v", target, err))
		return
	}
	defer linkConn.Close()

	stream, err := linkConn.OpenTCP(s.ctx, target.Host, target.Port)
	if err != nil {
		reply := byte(replyFailure)
		if linkErr, ok := err.(*link.Error); ok && linkErr.Status == link.StatusConnectFailed {
			reply = replyConnectionRefused
		}
		_ = writeReply(conn, reply, nil, 0)
		s.logf(fmt.Sprintf("TCP %s failed: %v", target, err))
		return
	}

	if err := writeReply(conn, replySuccess, nil, 0); err != nil {
		return
	}
	s.logf(fmt.Sprintf("TCP %s", target))

	s.relay(conn, reader, stream)
}

func (s *Server) relay(conn net.Conn, reader *bufio.Reader, stream *link.TCPStream) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		buffer := getBuffer()
		defer putBuffer(buffer)

		_, _ = io.CopyBuffer(
			countingWriter{w: stream, add: s.stats.AddUpload},
			reader,
			buffer,
		)
		_ = stream.CloseWrite()
	}()

	go func() {
		defer wg.Done()
		buffer := getBuffer()
		defer putBuffer(buffer)

		_, _ = io.CopyBuffer(
			countingWriter{w: conn, add: s.stats.AddDownload},
			stream,
			buffer,
		)
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()

	wg.Wait()
}

func (s *Server) associate(control net.Conn, hint link.Address) {
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		_ = writeReply(control, replyFailure, nil, 0)
		return
	}
	defer udp.Close()

	local := udp.LocalAddr().(*net.UDPAddr)
	if err := writeReply(control, replySuccess, local.IP, local.Port); err != nil {
		return
	}
	s.logf(fmt.Sprintf("UDP association on 127.0.0.1:%d", local.Port))

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()

	// The TCP control connection defines the lifetime of the association.
	go func() {
		_, _ = io.Copy(io.Discard, control)
		cancel()
	}()

	// Unblock the datagram loop as soon as the association ends.
	go func() {
		<-ctx.Done()
		_ = udp.Close()
	}()

	var (
		mu        sync.Mutex
		client    *net.UDPAddr
		assoc     *link.UDPAssoc
		assocLink *link.Link
	)
	if hint.IP != nil && !hint.IP.IsUnspecified() && hint.Port != 0 {
		client = &net.UDPAddr{IP: hint.IP, Port: hint.Port}
	}

	var downstream sync.WaitGroup
	defer downstream.Wait()
	// Registered after Wait so it runs first: closing the phone link unblocks
	// the downstream reader, which would otherwise wait for the phone forever.
	defer func() {
		if assocLink != nil {
			_ = assocLink.Close()
		}
	}()

	startDownstream := func(a *link.UDPAssoc) {
		downstream.Add(1)
		go func() {
			defer downstream.Done()
			for {
				frame, err := a.Receive()
				if err != nil {
					return
				}

				mu.Lock()
				destination := client
				mu.Unlock()
				if destination == nil {
					continue
				}

				s.stats.AddDownload(len(frame.Payload))
				datagram, err := encodeDatagram(frame)
				if err != nil {
					continue
				}
				if _, err := udp.WriteToUDP(datagram, destination); err != nil {
					return
				}
			}
		}()
	}

	buffer := make([]byte, 64*1024)
	for {
		_ = udp.SetReadDeadline(time.Now().Add(30 * time.Minute))
		n, from, err := udp.ReadFromUDP(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				s.logf("UDP association idle timeout")
				return
			}
			s.logf(fmt.Sprintf("UDP association ended: %v", err))
			return
		}

		address, payload, ok := parseDatagram(buffer[:n])
		if !ok {
			continue
		}

		mu.Lock()
		client = from
		mu.Unlock()

		if assoc == nil {
			linkConn, err := s.dial(ctx)
			if err != nil {
				s.logf(fmt.Sprintf("UDP link failed: %v", err))
				return
			}
			opened, err := linkConn.OpenUDP()
			if err != nil {
				linkConn.Close()
				s.logf(fmt.Sprintf("UDP association failed: %v", err))
				return
			}
			assocLink = linkConn
			assoc = opened
			startDownstream(opened)
		}

		s.stats.AddUpload(len(payload))
		if err := assoc.Send(address, payload); err != nil {
			s.logf(fmt.Sprintf("UDP send failed: %v", err))
			return
		}
	}
}

func parseDatagram(packet []byte) (link.Address, []byte, bool) {
	if len(packet) < 4 || packet[2] != 0 {
		// Fragmented datagrams are not supported, malformed ones are dropped.
		return link.Address{}, nil, false
	}

	reader := bufio.NewReader(bytes.NewReader(packet[3:]))
	address, err := link.ParseAddress(reader)
	if err != nil || address.Port == 0 {
		return link.Address{}, nil, false
	}

	payload, err := io.ReadAll(reader)
	if err != nil {
		return link.Address{}, nil, false
	}
	return address, payload, true
}

func encodeDatagram(frame *link.UDPFrame) ([]byte, error) {
	datagram := []byte{0, 0, 0}
	datagram, err := link.AppendAddress(datagram, frame.Address.Host, frame.Address.Port)
	if err != nil {
		return nil, err
	}
	return append(datagram, frame.Payload...), nil
}

func writeReply(w io.Writer, reply byte, ip net.IP, port int) error {
	address := ip
	if address == nil || address.To4() == nil && address.To16() == nil {
		address = net.IPv4zero
	}

	response := []byte{5, reply, 0}
	response, err := link.AppendAddress(response, address.String(), port)
	if err != nil {
		return err
	}

	_, err = w.Write(response)
	return err
}

type countingWriter struct {
	w   io.Writer
	add func(int)
}

func (c countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if n > 0 {
		c.add(n)
	}
	return n, err
}

var bufferPool = sync.Pool{
	New: func() any {
		buffer := make([]byte, 128*1024)
		return &buffer
	},
}

func getBuffer() []byte {
	return *bufferPool.Get().(*[]byte)
}

func putBuffer(buffer []byte) {
	bufferPool.Put(&buffer)
}

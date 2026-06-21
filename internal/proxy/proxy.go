package proxy

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"

	"cenanozen.com/proxyproxy/internal/stats"
)

type countingConn struct {
	net.Conn
	sent *int64
	recv *int64
}

func (c *countingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		atomic.AddInt64(c.recv, int64(n))
	}
	return n, err
}

func (c *countingConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		atomic.AddInt64(c.sent, int64(n))
	}
	return n, err
}

type Upstream struct {
	Address string
	Dialer  proxy.Dialer
}

type Server struct {
	listen    string
	upstreams []*Upstream
	rr        uint64
	stats     *stats.Collector
}

func NewServer(listen string, upstreamURLs []string, st *stats.Collector) (*Server, error) {
	s := &Server{listen: listen, stats: st}
	for _, u := range upstreamURLs {
		up, err := buildUpstream(u)
		if err != nil {
			return nil, fmt.Errorf("upstream %s: %w", u, err)
		}
		s.upstreams = append(s.upstreams, up)
		st.Register(up.Address)
	}
	if len(s.upstreams) == 0 {
		return nil, errors.New("no upstream proxies configured")
	}
	return s, nil
}

func buildUpstream(raw string) (*Upstream, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("empty upstream")
	}
	var addr, user, pass string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(u.Scheme, "socks5") && !strings.EqualFold(u.Scheme, "socks5h") {
			return nil, fmt.Errorf("unsupported scheme %q (use socks5://)", u.Scheme)
		}
		addr = u.Host
		if u.User != nil {
			user = u.User.Username()
			pass, _ = u.User.Password()
		}
	} else {
		addr = raw
	}
	var auth *proxy.Auth
	if user != "" {
		auth = &proxy.Auth{User: user, Password: pass}
	}
	d, err := proxy.SOCKS5("tcp", addr, auth, &net.Dialer{Timeout: 30 * time.Second})
	if err != nil {
		return nil, err
	}
	return &Upstream{Address: addr, Dialer: d}, nil
}

func (s *Server) pick() *Upstream {
	n := uint64(len(s.upstreams))
	idx := atomic.AddUint64(&s.rr, 1) % n
	return s.upstreams[idx]
}

func (s *Server) ListenAddr() string { return s.listen }

func (s *Server) Serve(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(client net.Conn) {
	defer client.Close()
	clientIP := client.RemoteAddr().String()
	host, _, err := net.SplitHostPort(clientIP)
	if err == nil {
		clientIP = host
	}

	br := bufioReader(client)
	if err := socksHandshake(br, client); err != nil {
		return
	}
	dst, atyp, err := readRequest(br, client)
	if err != nil {
		return
	}

	up := s.pick()
	s.stats.ConnectionStart(up.Address)
	defer s.stats.ConnectionEnd(up.Address)
	s.stats.Request(up.Address, clientIP)

	remote, err := up.Dialer.Dial("tcp", dst)
	if err != nil {
		s.stats.Error(up.Address)
		writeReply(client, 0x05, replyHostUnreachable, atyp)
		return
	}
	defer remote.Close()

	if err := writeReply(client, 0x05, replySuccess, atyp); err != nil {
		return
	}

	var sent, recv int64
	rc := &countingConn{Conn: remote, sent: &sent, recv: &recv}
	pipe(client, rc)
	s.stats.Traffic(up.Address, sent, recv, clientIP)
}

func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(b, a)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(a, b)
		done <- struct{}{}
	}()
	<-done
}

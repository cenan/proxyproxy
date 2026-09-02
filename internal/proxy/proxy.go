package proxy

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
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
	Address       string
	Dialer        proxy.Dialer
	cooldownUntil atomic.Int64
	disabled      atomic.Bool
}

type Server struct {
	listen    string
	upstreams []*Upstream
	rr        uint64
	cooldown  time.Duration
	stats     *stats.Collector
	statePath string
	stateMu   sync.Mutex
}

func NewServer(listen string, upstreamURLs []string, cooldown time.Duration, st *stats.Collector, statePath string) (*Server, error) {
	s := &Server{listen: listen, cooldown: cooldown, stats: st, statePath: statePath}
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
	s.loadState()
	return s, nil
}

func buildUpstream(raw string) (*Upstream, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("empty upstream")
	}
	var addr, user, pass string
	scheme := ""
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		scheme = strings.ToLower(u.Scheme)
		addr = u.Host
		if u.User != nil {
			user = u.User.Username()
			pass, _ = u.User.Password()
		}
	} else {
		addr = raw
	}

	switch scheme {
	case "", "socks5", "socks5h":
		var auth *proxy.Auth
		if user != "" {
			auth = &proxy.Auth{User: user, Password: pass}
		}
		d, err := proxy.SOCKS5("tcp", addr, auth, &net.Dialer{Timeout: 30 * time.Second})
		if err != nil {
			return nil, err
		}
		return &Upstream{Address: addr, Dialer: d}, nil
	case "http", "https":
		d := newHTTPDialer(addr, scheme == "https", user, pass, 30*time.Second)
		return &Upstream{Address: addr, Dialer: d}, nil
	default:
		return nil, fmt.Errorf("unsupported scheme %q (use socks5:// or http://)", scheme)
	}
}

func (s *Server) pick() *Upstream {
	n := uint64(len(s.upstreams))
	if n == 0 {
		return nil
	}
	now := time.Now().UnixNano()
	for {
		cur := atomic.LoadUint64(&s.rr)
		for i := uint64(0); i < n; i++ {
			idx := (cur + i) % n
			up := s.upstreams[idx]
			if up.disabled.Load() || up.cooldownUntil.Load() > now {
				continue
			}
			if atomic.CompareAndSwapUint64(&s.rr, cur, (idx+1)%n) {
				return up
			}
			break
		}
		if !atomic.CompareAndSwapUint64(&s.rr, cur, (cur+1)%n) {
			continue
		}
		return nil
	}
}

func (s *Server) applyCooldown(up *Upstream) {
	if s.cooldown <= 0 {
		return
	}
	until := time.Now().Add(s.cooldown).UnixNano()
	up.cooldownUntil.Store(until)
	s.stats.RecordCooldown(up.Address, until)
}

func (s *Server) SetDisabled(address string, disabled bool) {
	for _, up := range s.upstreams {
		if up.Address == address {
			up.disabled.Store(disabled)
			s.stats.RecordDisabled(address, disabled)
			s.saveState()
			return
		}
	}
}

func (s *Server) EnableAll() {
	for _, up := range s.upstreams {
		up.disabled.Store(false)
		s.stats.RecordDisabled(up.Address, false)
	}
	s.saveState()
}

func (s *Server) loadState() {
	if s.statePath == "" {
		return
	}
	data, err := os.ReadFile(s.statePath)
	if err != nil {
		return
	}
	var addrs []string
	if err := json.Unmarshal(data, &addrs); err != nil {
		return
	}
	set := make(map[string]struct{}, len(addrs))
	for _, a := range addrs {
		set[a] = struct{}{}
	}
	for _, up := range s.upstreams {
		if _, ok := set[up.Address]; ok {
			up.disabled.Store(true)
			s.stats.RecordDisabled(up.Address, true)
		}
	}
}

func (s *Server) saveState() {
	if s.statePath == "" {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	addrs := make([]string, 0)
	for _, up := range s.upstreams {
		if up.disabled.Load() {
			addrs = append(addrs, up.Address)
		}
	}
	data, err := json.MarshalIndent(addrs, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(s.statePath, data, 0o644)
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
	if up == nil {
		writeReply(client, 0x05, replyConnRefused, atyp)
		return
	}
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
	pipe(client, rc, func(code int) {
		if isCooldownStatus(code) {
			s.applyCooldown(up)
		}
	})
	s.stats.Traffic(up.Address, sent, recv, clientIP)
}

func pipe(client net.Conn, remote *countingConn, onStatus func(int)) {
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(remote, client)
		done <- struct{}{}
	}()
	go func() {
		copyResponse(client, remote, onStatus)
		done <- struct{}{}
	}()
	<-done
}

func copyResponse(dst io.Writer, src *countingConn, onStatus func(int)) {
	br := bufio.NewReader(src)
	peeked, err := br.Peek(16)
	if err == nil && len(peeked) > 0 {
		if code, ok := parseHTTPStatus(peeked); ok && onStatus != nil {
			onStatus(code)
		}
	}
	io.Copy(dst, br)
}

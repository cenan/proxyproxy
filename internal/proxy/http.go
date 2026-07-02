package proxy

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

type httpDialer struct {
	addr    string
	auth    string
	useTLS  bool
	timeout time.Duration
}

func newHTTPDialer(addr string, useTLS bool, user, pass string, timeout time.Duration) *httpDialer {
	d := &httpDialer{addr: addr, useTLS: useTLS, timeout: timeout}
	if user != "" {
		d.auth = base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	}
	return d
}

func (d *httpDialer) Dial(network, target string) (net.Conn, error) {
	nd := &net.Dialer{Timeout: d.timeout}
	raw, err := nd.Dial("tcp", d.addr)
	if err != nil {
		return nil, err
	}
	conn := raw
	if d.useTLS {
		host := d.addr
		if h, _, e := net.SplitHostPort(host); e == nil {
			host = h
		}
		tlsConn := tls.Client(raw, &tls.Config{ServerName: host})
		raw.SetDeadline(time.Now().Add(d.timeout))
		if err := tlsConn.Handshake(); err != nil {
			raw.Close()
			return nil, fmt.Errorf("http proxy tls handshake: %w", err)
		}
		raw.SetDeadline(time.Time{})
		conn = tlsConn
	}
	br := bufio.NewReader(conn)
	if err := d.connect(conn, br, target); err != nil {
		conn.Close()
		return nil, err
	}
	return &bufferedConn{br: br, Conn: conn}, nil
}

func (d *httpDialer) connect(conn net.Conn, br *bufio.Reader, target string) error {
	conn.SetDeadline(time.Now().Add(d.timeout))
	defer conn.SetDeadline(time.Time{})

	var b strings.Builder
	b.WriteString("CONNECT " + target + " HTTP/1.1\r\n")
	b.WriteString("Host: " + target + "\r\n")
	if d.auth != "" {
		b.WriteString("Proxy-Authorization: Basic " + d.auth + "\r\n")
	}
	b.WriteString("\r\n")
	if _, err := conn.Write([]byte(b.String())); err != nil {
		return fmt.Errorf("http proxy write: %w", err)
	}

	line, err := br.ReadString('\n')
	if err != nil {
		return fmt.Errorf("http proxy read: %w", err)
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return fmt.Errorf("bad http proxy response: %q", strings.TrimSpace(line))
	}
	code, err := strconv.Atoi(fields[1])
	if err != nil {
		return fmt.Errorf("bad http proxy status: %q", fields[1])
	}
	if code != 200 {
		return fmt.Errorf("http proxy rejected CONNECT: %s", strings.TrimSpace(line))
	}
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			return fmt.Errorf("http proxy read headers: %w", err)
		}
		if l == "\r\n" || l == "\n" {
			break
		}
	}
	return nil
}

type bufferedConn struct {
	br *bufio.Reader
	net.Conn
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.br.Read(p) }
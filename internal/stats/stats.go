package stats

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type ProxyStats struct {
	Address       string `json:"address"`
	Requests      int64  `json:"requests"`
	BytesSent     int64  `json:"bytes_sent"`
	BytesRecv     int64  `json:"bytes_recv"`
	Connections   int64  `json:"connections"`
	ActiveConns   int64  `json:"active_conns"`
	Errors        int64  `json:"errors"`
	LastUsed      int64  `json:"last_used"`
	CooldownUntil int64  `json:"cooldown_until"`
}

type ClientRecord struct {
	IP        string `json:"ip"`
	Requests  int64  `json:"requests"`
	LastSeen  int64  `json:"last_seen"`
	BytesSent int64  `json:"bytes_sent"`
	BytesRecv int64  `json:"bytes_recv"`
}

type Snapshot struct {
	TotalRequests  int64          `json:"total_requests"`
	TotalBytesSent int64          `json:"total_bytes_sent"`
	TotalBytesRecv int64          `json:"total_bytes_recv"`
	TotalConns     int64          `json:"total_connections"`
	TotalErrors    int64          `json:"total_errors"`
	StartTime      int64          `json:"start_time"`
	Proxies        []ProxyStats   `json:"proxies"`
	Clients        []ClientRecord `json:"clients,omitempty"`
}

type Collector struct {
	logIPs  bool
	mu      sync.RWMutex
	byAddr  map[string]*ProxyStats
	order   []string
	clients map[string]*ClientRecord
	start   time.Time
}

func New(logIPs bool) *Collector {
	return &Collector{
		logIPs:  logIPs,
		byAddr:  make(map[string]*ProxyStats),
		clients: make(map[string]*ClientRecord),
		start:   time.Now(),
	}
}

func (c *Collector) Register(address string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.byAddr[address]; ok {
		return
	}
	c.byAddr[address] = &ProxyStats{Address: address, LastUsed: 0}
	c.order = append(c.order, address)
}

func (c *Collector) ConnectionStart(address string) {
	c.mu.RLock()
	p := c.byAddr[address]
	c.mu.RUnlock()
	if p == nil {
		return
	}
	atomic.AddInt64(&p.Connections, 1)
	atomic.AddInt64(&p.ActiveConns, 1)
	atomic.StoreInt64(&p.LastUsed, time.Now().Unix())
}

func (c *Collector) ConnectionEnd(address string) {
	c.mu.RLock()
	p := c.byAddr[address]
	c.mu.RUnlock()
	if p == nil {
		return
	}
	atomic.AddInt64(&p.ActiveConns, -1)
}

func (c *Collector) Request(address string, clientIP string) {
	c.mu.RLock()
	p := c.byAddr[address]
	c.mu.RUnlock()
	if p != nil {
		atomic.AddInt64(&p.Requests, 1)
		atomic.StoreInt64(&p.LastUsed, time.Now().Unix())
	}
	if c.logIPs {
		c.recordClient(clientIP)
	}
}

func (c *Collector) recordClient(ipStr string) {
	if ipStr == "" {
		return
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		host, _, err := net.SplitHostPort(ipStr)
		if err != nil {
			return
		}
		ip = net.ParseIP(host)
		if ip == nil {
			return
		}
		ipStr = ip.String()
	} else {
		ipStr = ip.String()
	}

	c.mu.Lock()
	cl, ok := c.clients[ipStr]
	if !ok {
		cl = &ClientRecord{IP: ipStr}
		c.clients[ipStr] = cl
	}
	c.mu.Unlock()
	atomic.AddInt64(&cl.Requests, 1)
	atomic.StoreInt64(&cl.LastSeen, time.Now().Unix())
}

func (c *Collector) Traffic(address string, sent, recv int64, clientIP string) {
	c.mu.RLock()
	p := c.byAddr[address]
	c.mu.RUnlock()
	if p != nil {
		atomic.AddInt64(&p.BytesSent, sent)
		atomic.AddInt64(&p.BytesRecv, recv)
	}
	if c.logIPs && clientIP != "" {
		c.mu.RLock()
		cl := c.clients[clientIP]
		c.mu.RUnlock()
		if cl != nil {
			atomic.AddInt64(&cl.BytesSent, sent)
			atomic.AddInt64(&cl.BytesRecv, recv)
		}
	}
}

func (c *Collector) Error(address string) {
	c.mu.RLock()
	p := c.byAddr[address]
	c.mu.RUnlock()
	if p != nil {
		atomic.AddInt64(&p.Errors, 1)
	}
}

func (c *Collector) RecordCooldown(address string, until int64) {
	c.mu.RLock()
	p := c.byAddr[address]
	c.mu.RUnlock()
	if p != nil {
		atomic.StoreInt64(&p.CooldownUntil, until)
	}
}

func (c *Collector) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	s := Snapshot{
		StartTime: c.start.Unix(),
		Proxies:   make([]ProxyStats, 0, len(c.order)),
	}
	for _, addr := range c.order {
		p := c.byAddr[addr]
		ps := ProxyStats{
			Address:       p.Address,
			Requests:      atomic.LoadInt64(&p.Requests),
			BytesSent:     atomic.LoadInt64(&p.BytesSent),
			BytesRecv:     atomic.LoadInt64(&p.BytesRecv),
			Connections:   atomic.LoadInt64(&p.Connections),
			ActiveConns:   atomic.LoadInt64(&p.ActiveConns),
			Errors:        atomic.LoadInt64(&p.Errors),
			LastUsed:      atomic.LoadInt64(&p.LastUsed),
			CooldownUntil: atomic.LoadInt64(&p.CooldownUntil),
		}
		s.TotalRequests += ps.Requests
		s.TotalBytesSent += ps.BytesSent
		s.TotalBytesRecv += ps.BytesRecv
		s.TotalConns += ps.Connections
		s.TotalErrors += ps.Errors
		s.Proxies = append(s.Proxies, ps)
	}
	if c.logIPs {
		for _, cl := range c.clients {
			s.Clients = append(s.Clients, ClientRecord{
				IP:        cl.IP,
				Requests:  atomic.LoadInt64(&cl.Requests),
				LastSeen:  atomic.LoadInt64(&cl.LastSeen),
				BytesSent: atomic.LoadInt64(&cl.BytesSent),
				BytesRecv: atomic.LoadInt64(&cl.BytesRecv),
			})
		}
	}
	return s
}

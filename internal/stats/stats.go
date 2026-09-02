package stats

import (
	"encoding/json"
	"net"
	"os"
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
	Disabled      bool   `json:"disabled"`

	disabled int64
	all      *AllTimeProxy
}

type AllTimeProxy struct {
	Address     string
	Requests    atomic.Int64
	BytesSent   atomic.Int64
	BytesRecv   atomic.Int64
	Connections atomic.Int64
	Errors      atomic.Int64
}

type ProxyAllTime struct {
	Address     string `json:"address"`
	Requests    int64  `json:"requests"`
	BytesSent   int64  `json:"bytes_sent"`
	BytesRecv   int64  `json:"bytes_recv"`
	Connections int64  `json:"connections"`
	Errors      int64  `json:"errors"`
}

type ClientRecord struct {
	IP        string `json:"ip"`
	Requests  int64  `json:"requests"`
	LastSeen  int64  `json:"last_seen"`
	BytesSent int64  `json:"bytes_sent"`
	BytesRecv int64  `json:"bytes_recv"`
}

type AllTimeSummary struct {
	FirstSeen      int64          `json:"first_seen"`
	TotalRequests  int64          `json:"total_requests"`
	TotalBytesSent int64          `json:"total_bytes_sent"`
	TotalBytesRecv int64          `json:"total_bytes_recv"`
	TotalConns     int64          `json:"total_connections"`
	TotalErrors    int64          `json:"total_errors"`
	Proxies        []ProxyAllTime `json:"proxies"`
}

type Snapshot struct {
	TotalRequests  int64          `json:"total_requests"`
	TotalBytesSent int64          `json:"total_bytes_sent"`
	TotalBytesRecv int64          `json:"total_bytes_recv"`
	TotalConns     int64          `json:"total_connections"`
	TotalErrors    int64          `json:"total_errors"`
	StartTime      int64          `json:"start_time"`
	AllTime        AllTimeSummary `json:"all_time"`
	Proxies        []ProxyStats   `json:"proxies"`
	Clients        []ClientRecord `json:"clients,omitempty"`
}

type persistedProxy struct {
	Requests    int64 `json:"requests"`
	BytesSent   int64 `json:"bytes_sent"`
	BytesRecv   int64 `json:"bytes_recv"`
	Connections int64 `json:"connections"`
	Errors      int64 `json:"errors"`
}

type persisted struct {
	FirstSeen int64                     `json:"first_seen"`
	Proxies   map[string]persistedProxy `json:"proxies"`
}

type Collector struct {
	logIPs  bool
	mu      sync.RWMutex
	byAddr  map[string]*ProxyStats
	order   []string
	clients map[string]*ClientRecord
	start   time.Time

	statsPath string
	firstSeen int64
	dirty     atomic.Bool
	saveMu    sync.Mutex
}

func New(logIPs bool, statsPath string) *Collector {
	c := &Collector{
		logIPs:    logIPs,
		byAddr:    make(map[string]*ProxyStats),
		clients:   make(map[string]*ClientRecord),
		start:     time.Now(),
		statsPath: statsPath,
		firstSeen: time.Now().Unix(),
	}
	c.load()
	return c
}

func (c *Collector) load() {
	if c.statsPath == "" {
		return
	}
	data, err := os.ReadFile(c.statsPath)
	if err != nil {
		return
	}
	var p persisted
	if err := json.Unmarshal(data, &p); err != nil {
		return
	}
	if p.FirstSeen > 0 {
		c.firstSeen = p.FirstSeen
	}
	for addr, pp := range p.Proxies {
		at := &AllTimeProxy{Address: addr}
		at.Requests.Store(pp.Requests)
		at.BytesSent.Store(pp.BytesSent)
		at.BytesRecv.Store(pp.BytesRecv)
		at.Connections.Store(pp.Connections)
		at.Errors.Store(pp.Errors)
		c.byAddr[addr] = &ProxyStats{Address: addr, all: at}
		c.order = append(c.order, addr)
	}
}

func (c *Collector) Persist() error {
	if c.statsPath == "" {
		return nil
	}
	if !c.dirty.Load() {
		return nil
	}
	c.saveMu.Lock()
	defer c.saveMu.Unlock()

	p := persisted{
		FirstSeen: c.firstSeen,
		Proxies:   make(map[string]persistedProxy),
	}
	c.mu.RLock()
	for _, addr := range c.order {
		at := c.byAddr[addr].all
		if at == nil {
			continue
		}
		p.Proxies[addr] = persistedProxy{
			Requests:    at.Requests.Load(),
			BytesSent:   at.BytesSent.Load(),
			BytesRecv:   at.BytesRecv.Load(),
			Connections: at.Connections.Load(),
			Errors:      at.Errors.Load(),
		}
	}
	c.mu.RUnlock()

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.statsPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.statsPath); err != nil {
		return err
	}
	c.dirty.Store(false)
	return nil
}

func (c *Collector) Register(address string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.byAddr[address]; ok {
		return
	}
	c.byAddr[address] = &ProxyStats{
		Address:  address,
		LastUsed: 0,
		all:      &AllTimeProxy{Address: address},
	}
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
	p.all.Connections.Add(1)
	c.dirty.Store(true)
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
		p.all.Requests.Add(1)
		c.dirty.Store(true)
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
		p.all.BytesSent.Add(sent)
		p.all.BytesRecv.Add(recv)
		c.dirty.Store(true)
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
	if p == nil {
		return
	}
	atomic.AddInt64(&p.Errors, 1)
	p.all.Errors.Add(1)
	c.dirty.Store(true)
}

func (c *Collector) RecordCooldown(address string, until int64) {
	c.mu.RLock()
	p := c.byAddr[address]
	c.mu.RUnlock()
	if p != nil {
		atomic.StoreInt64(&p.CooldownUntil, until)
	}
}

func (c *Collector) RecordDisabled(address string, disabled bool) {
	c.mu.RLock()
	p := c.byAddr[address]
	c.mu.RUnlock()
	if p != nil {
		if disabled {
			atomic.StoreInt64(&p.disabled, 1)
		} else {
			atomic.StoreInt64(&p.disabled, 0)
		}
	}
}

func (c *Collector) IsDisabled(address string) bool {
	c.mu.RLock()
	p := c.byAddr[address]
	c.mu.RUnlock()
	if p == nil {
		return false
	}
	return atomic.LoadInt64(&p.disabled) == 1
}

func (c *Collector) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	s := Snapshot{
		StartTime: c.start.Unix(),
		Proxies:   make([]ProxyStats, 0, len(c.order)),
		AllTime: AllTimeSummary{
			FirstSeen: c.firstSeen,
			Proxies:   make([]ProxyAllTime, 0, len(c.order)),
		},
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
			Disabled:      atomic.LoadInt64(&p.disabled) == 1,
		}
		s.TotalRequests += ps.Requests
		s.TotalBytesSent += ps.BytesSent
		s.TotalBytesRecv += ps.BytesRecv
		s.TotalConns += ps.Connections
		s.TotalErrors += ps.Errors
		s.Proxies = append(s.Proxies, ps)

		at := p.all
		if at != nil {
			pa := ProxyAllTime{
				Address:     at.Address,
				Requests:    at.Requests.Load(),
				BytesSent:   at.BytesSent.Load(),
				BytesRecv:   at.BytesRecv.Load(),
				Connections: at.Connections.Load(),
				Errors:      at.Errors.Load(),
			}
			s.AllTime.TotalRequests += pa.Requests
			s.AllTime.TotalBytesSent += pa.BytesSent
			s.AllTime.TotalBytesRecv += pa.BytesRecv
			s.AllTime.TotalConns += pa.Connections
			s.AllTime.TotalErrors += pa.Errors
			s.AllTime.Proxies = append(s.AllTime.Proxies, pa)
		}
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
package web

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"cenanozen.com/proxyproxy/internal/stats"
)

//go:embed index.html
var indexHTML string

type Server struct {
	addr  string
	stats *stats.Collector
}

func NewServer(addr string, st *stats.Collector) *Server {
	return &Server{addr: addr, stats: st}
}

func (s *Server) ListenAddr() string { return s.addr }

func (s *Server) Serve() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/stats", s.handleAPI)
	srv := &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	snap := s.stats.Snapshot()
	_ = json.NewEncoder(w).Encode(snap)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, indexHTML)
}

func fmtBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

var _ = fmtBytes

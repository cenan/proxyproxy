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

type UpstreamController interface {
	SetDisabled(address string, disabled bool)
	EnableAll()
}

type Server struct {
	addr  string
	stats *stats.Collector
	ctrl  UpstreamController
}

func NewServer(addr string, st *stats.Collector, ctrl UpstreamController) *Server {
	return &Server{addr: addr, stats: st, ctrl: ctrl}
}

func (s *Server) ListenAddr() string { return s.addr }

func (s *Server) Serve() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/stats", s.handleAPI)
	mux.HandleFunc("POST /api/disable", s.handleDisable)
	mux.HandleFunc("POST /api/enable", s.handleEnable)
	mux.HandleFunc("POST /api/enable-all", s.handleEnableAll)
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

func (s *Server) handleDisable(w http.ResponseWriter, r *http.Request) {
	s.setDisabled(w, r, true)
}

func (s *Server) handleEnable(w http.ResponseWriter, r *http.Request) {
	s.setDisabled(w, r, false)
}

func (s *Server) handleEnableAll(w http.ResponseWriter, r *http.Request) {
	s.ctrl.EnableAll()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"ok":true}`)
}

func (s *Server) setDisabled(w http.ResponseWriter, r *http.Request, disabled bool) {
	addr := r.URL.Query().Get("addr")
	if addr == "" {
		http.Error(w, "missing addr", http.StatusBadRequest)
		return
	}
	s.ctrl.SetDisabled(addr, disabled)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"ok":true}`)
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

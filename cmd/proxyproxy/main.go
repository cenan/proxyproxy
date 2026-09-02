package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cenanozen.com/proxyproxy/internal/config"
	"cenanozen.com/proxyproxy/internal/proxy"
	"cenanozen.com/proxyproxy/internal/stats"
	"cenanozen.com/proxyproxy/internal/web"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	st := stats.New(cfg.LogIPs)

	cooldown := time.Duration(cfg.CooldownSeconds) * time.Second
	srv, err := proxy.NewServer(cfg.Listen, cfg.UpstreamProxies, cooldown, st, cfg.StateFile)
	if err != nil {
		log.Fatalf("proxy: %v", err)
	}

	ln, err := net.Listen("tcp", srv.ListenAddr())
	if err != nil {
		log.Fatalf("listen %s: %v", srv.ListenAddr(), err)
	}
	log.Printf("SOCKS5 proxy listening on %s (%d upstreams)", srv.ListenAddr(), len(cfg.UpstreamProxies))
	go func() {
		if err := srv.Serve(ln); err != nil {
			log.Fatalf("proxy serve: %v", err)
		}
	}()

	webSrv := web.NewServer(cfg.WebListen, st, srv)
	go func() {
		log.Printf("web UI listening on %s", webSrv.ListenAddr())
		if err := webSrv.Serve(); err != nil {
			log.Fatalf("web serve: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down")
	_ = ln.Close()
}

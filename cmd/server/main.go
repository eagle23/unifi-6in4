package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/eagle23/unifi-tunnel-4to6/internal/api"
	"github.com/eagle23/unifi-tunnel-4to6/internal/config"
	"github.com/eagle23/unifi-tunnel-4to6/internal/ra"
	"github.com/eagle23/unifi-tunnel-4to6/internal/tunnel"
)

//go:embed web
var webContent embed.FS

type configStore struct {
	mu   sync.RWMutex
	cfg  *config.Config
	path string
}

func (s *configStore) Get() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *configStore) Update(cfg *config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := config.Save(s.path, cfg); err != nil {
		return err
	}
	s.cfg = cfg
	return nil
}

func main() {
	configPath := flag.String("config", "", "path to config.json")
	scriptPath := flag.String("script", "", "path to tunnel.sh")
	flag.Parse()

	baseDir := filepath.Dir(os.Args[0])
	if *configPath == "" {
		*configPath = filepath.Join(baseDir, "config.json")
	}
	if *scriptPath == "" {
		*scriptPath = filepath.Join(baseDir, "tunnel.sh")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg = config.Defaults()
			if err := config.Save(*configPath, cfg); err != nil {
				log.Fatalf("create default config: %v", err)
			}
			log.Printf("created default config at %s", *configPath)
		} else {
			log.Fatalf("load config: %v", err)
		}
	}

	store := &configStore{cfg: cfg, path: *configPath}
	mgr := tunnel.NewManager(*scriptPath)
	handler := api.NewHandler(mgr, store)

	webFS, err := fs.Sub(webContent, "web")
	if err != nil {
		log.Fatalf("embed web: %v", err)
	}
	srv := api.NewServer(handler, cfg.Server.AuthToken, webFS)

	if cfg.LAN.Enabled && len(cfg.LAN.Networks) > 0 {
		startRA(cfg)
	}

	if cfg.Health.Enabled {
		go healthLoop(mgr, store)
	}

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Server.Port)
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, srv))
}

func startRA(cfg *config.Config) {
	var networks []ra.NetworkEntry
	for _, nw := range cfg.LAN.Networks {
		prefix, err := netip.ParsePrefix(nw.Prefix)
		if err != nil {
			log.Printf("ra: invalid prefix %q: %v", nw.Prefix, err)
			continue
		}
		networks = append(networks, ra.NetworkEntry{
			Interface: nw.Interface,
			Prefix:    prefix,
		})
	}
	var dns []netip.Addr
	for _, d := range cfg.LAN.DNS {
		addr, err := netip.ParseAddr(d)
		if err != nil {
			log.Printf("ra: invalid dns %q: %v", d, err)
			continue
		}
		dns = append(dns, addr)
	}
	if len(networks) > 0 {
		_, err := ra.NewAdvertiser(ra.AdvertiserConfig{
			Networks: networks,
			DNS:      dns,
			Interval: 10 * time.Second,
		})
		if err != nil {
			log.Printf("ra: failed to start advertiser: %v", err)
		} else {
			log.Printf("ra: advertising on %d interfaces", len(networks))
		}
	}
}

func healthLoop(mgr *tunnel.Manager, store *configStore) {
	for {
		cfg := store.Get()
		interval := time.Duration(cfg.Health.IntervalSec) * time.Second
		if interval < 5*time.Second {
			interval = 30 * time.Second
		}
		time.Sleep(interval)
		st, err := mgr.Status()
		if err != nil {
			log.Printf("health: status error: %v", err)
			continue
		}
		if st.TunnelUp && !st.PingOK && cfg.Health.AutoRestart {
			log.Printf("health: ping failed, restarting tunnel")
			if err := mgr.Restart(); err != nil {
				log.Printf("health: restart error: %v", err)
			}
		}
	}
}

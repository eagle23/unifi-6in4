package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/eagle23/unifi-tunnel-4to6/internal/api"
	"github.com/eagle23/unifi-tunnel-4to6/internal/control"
	"github.com/eagle23/unifi-tunnel-4to6/internal/logging"
	"github.com/eagle23/unifi-tunnel-4to6/internal/tunnel"
	webui "github.com/eagle23/unifi-tunnel-4to6/web"
)

func main() {
	switch command := firstArgument(); command {
	case "ctl":
		runCtl(os.Args[2:])
	case "serve":
		runServe(os.Args[2:])
	default:
		runServe(os.Args[1:])
	}
}

func runServe(args []string) {
	flagSet := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := flagSet.String("config", "", "path to config.json")
	flagSet.Parse(args)
	baseDir := executableDir()
	if *configPath == "" {
		*configPath = filepath.Join(baseDir, "config.json")
	}
	statePath := filepath.Join(baseDir, "state.json")
	if err := logging.Init(filepath.Dir(*configPath)); err != nil {
		log.Fatalf("init logging: %v", err)
	}
	defer logging.Close()
	slog.Info("daemon starting", "config", *configPath, "log_file", logging.Path())
	service, err := control.NewService(control.ServiceParams{
		ConfigPath: *configPath,
		StatePath:  statePath,
		Backend:    control.NewSystemBackend(),
	})
	if err != nil {
		slog.Error("create control service failed", "error", err)
		os.Exit(1)
	}
	if err := service.Start(); err != nil {
		slog.Warn("initial reconcile failed", "error", err)
	}
	defer service.Stop()
	handler := api.NewHandler(service)
	server := api.NewServer(handler, service.CurrentToken, webui.FileSystem())
	controlSocketPath := filepath.Join(filepath.Dir(*configPath), "daemon.sock")
	controlHandler := api.NewControlServer(handler)
	controlListener, err := listenUnixSocket(controlSocketPath)
	if err != nil {
		slog.Error("listen control socket failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		_ = controlListener.Close()
		_ = os.Remove(controlSocketPath)
	}()
	go func() {
		if serveErr := http.Serve(controlListener, controlHandler); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			slog.Error("control socket server failed", "error", serveErr)
		}
	}()
	addr := fmt.Sprintf("0.0.0.0:%d", service.GetConfig().Server.Port)
	slog.Info("HTTP server listening", "addr", addr, "control_socket", controlSocketPath)
	if err := http.ListenAndServe(addr, server); err != nil {
		slog.Error("HTTP server stopped", "error", err)
		os.Exit(1)
	}
}

func runCtl(args []string) {
	flagSet := flag.NewFlagSet("ctl", flag.ExitOnError)
	configPath := flagSet.String("config", "", "path to config.json")
	flagSet.Parse(args)
	if flagSet.NArg() == 0 {
		log.Fatalf("usage: %s ctl {status|up|down|restart|health}", filepath.Base(os.Args[0]))
	}
	baseDir := executableDir()
	if *configPath == "" {
		*configPath = filepath.Join(baseDir, "config.json")
	}
	socketPath := filepath.Join(filepath.Dir(*configPath), "daemon.sock")
	client := tunnel.NewClient(tunnel.ClientConfig{
		SocketPath: socketPath,
	})
	switch flagSet.Arg(0) {
	case "status":
		status, err := client.Status()
		if err != nil {
			log.Fatal(err)
		}
		printJSON(status)
	case "health":
		status, err := client.Health()
		if err != nil {
			log.Fatal(err)
		}
		printJSON(status)
	case "up":
		if err := client.Up(); err != nil {
			log.Fatal(err)
		}
	case "down":
		if err := client.Down(); err != nil {
			log.Fatal(err)
		}
	case "restart":
		if err := client.Restart(); err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatalf("unknown ctl command %q", flagSet.Arg(0))
	}
}

func executableDir() string {
	return filepath.Dir(os.Args[0])
}

func firstArgument() string {
	if len(os.Args) < 2 {
		return ""
	}
	return os.Args[1]
}

func printJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		log.Fatal(err)
	}
}

func listenUnixSocket(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return listener, nil
}

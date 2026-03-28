package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/eagle23/unifi-tunnel-4to6/internal/api"
	"github.com/eagle23/unifi-tunnel-4to6/internal/control"
	"github.com/eagle23/unifi-tunnel-4to6/internal/tunnel"
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
	service, err := control.NewService(control.ServiceParams{
		ConfigPath: *configPath,
		StatePath:  statePath,
		Backend:    control.NewSystemBackend(),
	})
	if err != nil {
		log.Fatalf("create control service: %v", err)
	}
	if err := service.Start(); err != nil {
		log.Printf("initial reconcile failed: %v", err)
	}
	defer service.Stop()
	handler := api.NewHandler(service)
	webDir := filepath.Join(baseDir, "web")
	var webFS http.FileSystem
	if info, err := os.Stat(webDir); err == nil && info.IsDir() {
		webFS = http.Dir(webDir)
	} else {
		log.Printf("web directory not found at %s, UI will not be served", webDir)
	}
	server := api.NewServer(handler, service.CurrentToken, webFS)
	controlSocketPath := filepath.Join(filepath.Dir(*configPath), "daemon.sock")
	controlHandler := api.NewControlServer(handler)
	controlListener, err := listenUnixSocket(controlSocketPath)
	if err != nil {
		log.Fatalf("listen control socket: %v", err)
	}
	defer func() {
		_ = controlListener.Close()
		_ = os.Remove(controlSocketPath)
	}()
	go func() {
		if serveErr := http.Serve(controlListener, controlHandler); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Printf("control socket server failed: %v", serveErr)
		}
	}()
	addr := fmt.Sprintf("0.0.0.0:%d", service.GetConfig().Server.Port)
	log.Printf("listening on %s", addr)
	log.Printf("control socket listening on %s", controlSocketPath)
	log.Fatal(http.ListenAndServe(addr, server))
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

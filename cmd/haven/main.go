// Command haven runs the Haven media server.
//
//	haven                 run in the foreground
//	haven install         install as a system service (systemd / launchd / Windows service)
//	haven uninstall|start|stop|restart|status
//	haven version
//
// Flags: -data <dir> (default: platform data dir, or $HAVEN_DATA_DIR), -v (debug logs)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/kardianos/service"

	"github.com/LTAGROUP/haven/internal/api"
	"github.com/LTAGROUP/haven/internal/auth"
	"github.com/LTAGROUP/haven/internal/config"
	"github.com/LTAGROUP/haven/internal/db"
	"github.com/LTAGROUP/haven/internal/images"
	"github.com/LTAGROUP/haven/internal/library"
	"github.com/LTAGROUP/haven/internal/metadata/tmdb"
	"github.com/LTAGROUP/haven/internal/stream"
	"github.com/LTAGROUP/haven/internal/torrent"
	"github.com/LTAGROUP/haven/internal/webhook"
	"path/filepath"
)

var version = "0.1.0-dev"

func main() {
	dataDir := flag.String("data", config.DefaultDataDir(), "data directory")
	verbose := flag.Bool("v", false, "debug logging")
	flag.Parse()

	level := slog.LevelInfo
	if *verbose || os.Getenv("HAVEN_DEBUG") != "" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	prg := &program{dataDir: *dataDir}
	svcArgs := []string{"-data", *dataDir}
	svc, err := service.New(prg, &service.Config{
		Name:        "haven",
		DisplayName: "Haven Media Server",
		Description: "Haven personal media server",
		Arguments:   svcArgs,
		Option: service.KeyValue{
			"UserService": false,
			"Restart":     "on-failure",
		},
	})
	if err != nil {
		slog.Error("service setup", "err", err)
		os.Exit(1)
	}

	if cmd := flag.Arg(0); cmd != "" {
		switch cmd {
		case "version":
			fmt.Println("haven", version)
			return
		case "status":
			st, err := svc.Status()
			if err != nil {
				fmt.Println("status:", err)
				os.Exit(1)
			}
			fmt.Println(map[service.Status]string{service.StatusRunning: "running", service.StatusStopped: "stopped"}[st])
			return
		case "install", "uninstall", "start", "stop", "restart":
			if err := service.Control(svc, cmd); err != nil {
				fmt.Fprintf(os.Stderr, "%s failed: %v\n", cmd, err)
				os.Exit(1)
			}
			fmt.Printf("haven: %s ok\n", cmd)
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
			os.Exit(2)
		}
	}
	if err := svc.Run(); err != nil {
		slog.Error("haven stopped", "err", err)
		os.Exit(1)
	}
}

type program struct {
	dataDir string
	cancel  context.CancelFunc
	done    chan struct{}
}

func (p *program) Start(s service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	ready := make(chan error, 1)
	go func() {
		defer close(p.done)
		if err := run(ctx, p.dataDir, ready); err != nil {
			slog.Error("server error", "err", err)
			if service.Interactive() {
				os.Exit(1)
			}
		}
	}()
	if service.Interactive() {
		go func() {
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
			<-sig
			slog.Info("shutting down")
			cancel()
			<-p.done
			os.Exit(0)
		}()
	}
	select {
	case err := <-ready:
		return err
	case <-time.After(30 * time.Second):
		return nil
	}
}

func (p *program) Stop(s service.Service) error {
	if p.cancel != nil {
		p.cancel()
		select {
		case <-p.done:
		case <-time.After(15 * time.Second):
		}
	}
	return nil
}

func run(ctx context.Context, dataDir string, ready chan<- error) error {
	cfg, err := config.Load(dataDir)
	if err != nil {
		ready <- err
		return err
	}
	slog.Info("starting haven", "version", version, "data", dataDir, "listen", cfg.Listen, "auth", cfg.Auth.Mode)

	d, err := db.Open(dataDir)
	if err != nil {
		ready <- err
		return err
	}
	defer d.Close()

	tm := tmdb.New(cfg.Metadata.TMDBAPIKey, cfg.Metadata.Language, d)
	if !tm.Enabled() {
		slog.Warn("no TMDB API key configured: items will use names parsed from files (set metadata.tmdb_api_key)")
	}
	hub := library.NewHub()
	scanner := library.NewScanner(d, tm, hub, library.Options{
		FFprobe: cfg.Transcode.FFprobePath, MatchWorkers: cfg.Metadata.Workers, ProbeWorkers: cfg.Scanner.ProbeWorkers,
	})
	scanner.Start(ctx)

	poll := time.Duration(0)
	if cfg.Scanner.IntervalMin > 0 && !cfg.Scanner.Watch {
		poll = time.Duration(cfg.Scanner.IntervalMin) * time.Minute
	}
	watcher := library.NewWatcher(scanner, d, poll)
	if cfg.Scanner.Watch {
		go watcher.Run(ctx)
	}

	hw := stream.DetectHW(cfg.Transcode.FFmpegPath, cfg.Transcode.HWAccel, cfg.Transcode.VAAPIDevice)
	streams := stream.NewManager(stream.Config{
		FFmpeg: cfg.Transcode.FFmpegPath, FFprobe: cfg.Transcode.FFprobePath, ScratchDir: cfg.Transcode.ScratchDir, HW: hw,
	})

	_, portStr, _ := net.SplitHostPort(cfg.Listen)
	port, _ := strconv.Atoi(portStr)
	serverInfo := func() webhook.Server {
		url := cfg.PublicURL
		if url == "" {
			if urls := api.LocalURLs(cfg.Listen); len(urls) > 0 {
				url = urls[0]
			}
		}
		return webhook.Server{ID: cfg.MachineID, Name: cfg.ServerName, URL: url}
	}
	hooks := webhook.New(func() []config.WebhookConfig { return cfg.Webhooks })
	torrents, err := torrent.NewManager(torrent.Deps{
		Config: func() config.TorrentConfig { return cfg.Torrents }, DB: d, Scanner: scanner, Hooks: hooks,
		Sessions: streams, Server: serverInfo, HTTPPort: port,
	})
	if err != nil {
		slog.Error("torrent support disabled", "err", err)
		torrents, _ = torrent.NewManager(torrent.Deps{Config: func() config.TorrentConfig { return config.TorrentConfig{} },
			DB: d, Scanner: scanner, Hooks: hooks, Sessions: streams, Server: serverInfo, HTTPPort: port})
	}
	defer torrents.Close()
	torrents.Start(ctx)

	srv := &api.Server{
		Version: version, Cfg: cfg, DB: d, Scanner: scanner, Watcher: watcher, TMDB: tm, Streams: streams,
		Torrents: torrents, Hub: hub, Hooks: hooks, Images: images.New(filepath.Join(dataDir, "images")),
	}
	switch cfg.Auth.Mode {
	case "supabase":
		if cfg.Auth.Supabase.URL == "" || cfg.Auth.Supabase.PublishableKey == "" {
			err := errors.New("auth.mode is supabase but auth.supabase.url / publishable_key are not set")
			ready <- err
			return err
		}
		srv.Supa = auth.NewSupabase(d, cfg)
		srv.Auth = srv.Supa
		go srv.RunHeartbeat(ctx)
	default:
		srv.Local = auth.NewLocal(d, cfg.Auth.SigningKey)
		srv.Auth = srv.Local
	}

	// Startup scan of every library (incremental: unchanged files are skipped).
	go func() {
		libs, _ := d.ListLibraries(ctx)
		for _, l := range libs {
			if _, err := scanner.ScanLibrary(ctx, l); err != nil {
				slog.Warn("startup scan", "library", l.Name, "err", err)
			}
		}
	}()

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 15 * time.Second,
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		ready <- err
		return err
	}
	ready <- nil
	for _, u := range api.LocalURLs(cfg.Listen) {
		slog.Info("listening", "url", u)
	}
	errc := make(chan error, 1)
	go func() { errc <- httpSrv.Serve(ln) }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdown)
	case err := <-errc:
		return err
	}
}

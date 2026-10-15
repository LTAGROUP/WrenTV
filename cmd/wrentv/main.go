// Command wrentv runs the Wren TV media server.
//
//	wrentv                run in the foreground
//	wrentv install        install as a system service (systemd / launchd / Windows service),
//	                      replacing a Haven-era "haven" service if one is installed
//	wrentv uninstall|start|stop|restart|status
//	wrentv version
//
// Flags: -data <dir> (default: platform data dir, or $WRENTV_DATA_DIR), -v (debug logs)
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

	"github.com/LTAGROUP/wrentv/internal/api"
	"github.com/LTAGROUP/wrentv/internal/auth"
	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
	"github.com/LTAGROUP/wrentv/internal/images"
	"github.com/LTAGROUP/wrentv/internal/library"
	"github.com/LTAGROUP/wrentv/internal/markers"
	"github.com/LTAGROUP/wrentv/internal/metadata/omdb"
	"github.com/LTAGROUP/wrentv/internal/metadata/tmdb"
	"github.com/LTAGROUP/wrentv/internal/stream"
	"github.com/LTAGROUP/wrentv/internal/torrent"
	"github.com/LTAGROUP/wrentv/internal/webhook"
	"path/filepath"
)

var version = "0.1.0-dev"

func main() {
	dataDir := flag.String("data", config.DefaultDataDir(), "data directory")
	verbose := flag.Bool("v", false, "debug logging")
	flag.Parse()

	level := slog.LevelInfo
	if *verbose || config.Getenv("DEBUG") != "" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	prg := &program{dataDir: *dataDir}
	svcArgs := []string{"-data", *dataDir}
	svc, err := service.New(prg, &service.Config{
		Name:        "wrentv",
		DisplayName: "Wren TV Media Server",
		Description: "Wren TV personal media server",
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
			fmt.Println("Wren TV", version)
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
			if cmd == "install" {
				removeLegacyService()
			}
			if err := service.Control(svc, cmd); err != nil {
				fmt.Fprintf(os.Stderr, "%s failed: %v\n", cmd, err)
				os.Exit(1)
			}
			fmt.Printf("wrentv: %s ok\n", cmd)
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
			os.Exit(2)
		}
	}
	if err := svc.Run(); err != nil {
		slog.Error("Wren TV stopped", "err", err)
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

// removeLegacyService stops and uninstalls the service Haven installed under
// the name "haven", so the new one can take over its port. Its data is left
// for MigrateDataDir to adopt.
func removeLegacyService() {
	old, err := service.New(&program{}, &service.Config{Name: "haven"})
	if err != nil {
		return
	}
	if _, err := old.Status(); errors.Is(err, service.ErrNotInstalled) {
		return
	}
	_ = old.Stop()
	if err := old.Uninstall(); err != nil {
		fmt.Fprintf(os.Stderr, "could not uninstall the old haven service: %v\n", err)
		return
	}
	fmt.Println("wrentv: removed the old haven service")
}

func run(ctx context.Context, dataDir string, ready chan<- error) error {
	dataDir, notes, err := config.MigrateDataDir(dataDir)
	for _, n := range notes {
		slog.Info("migrated from Haven", "change", n)
	}
	if err != nil {
		ready <- err
		return err
	}
	cfg, err := config.Load(dataDir)
	if err != nil {
		ready <- err
		return err
	}
	slog.Info("starting Wren TV", "version", version, "data", dataDir, "listen", cfg.Listen, "auth", cfg.Auth.Mode)

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
	analyzer := markers.New(d, cfg.Transcode.FFmpegPath, func() bool { return cfg.Markers.Fingerprint })
	scanner.OnProbed = analyzer.Kick
	go analyzer.Run(ctx)
	scanner.Start(ctx)

	watcher := library.NewWatcher(scanner, d, time.Duration(cfg.Scanner.PollSec)*time.Second)
	if cfg.Scanner.Watch {
		go watcher.Run(ctx)
	}
	if cfg.Scanner.IntervalMin > 0 {
		go scanner.Periodic(ctx, time.Duration(cfg.Scanner.IntervalMin)*time.Minute)
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
			if urls := api.LocalURLs(cfg); len(urls) > 0 {
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
		OMDb: omdb.New(func() string { return cfg.Metadata.OMDbAPIKey }, d), Markers: analyzer,
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
		if !srv.Supa.Claimed() {
			if cfg.Auth.Supabase.ClaimCode == "" {
				cfg.Auth.Supabase.ClaimCode = auth.NewClaimCode()
				_ = cfg.Save()
			}
			slog.Warn("server is not claimed yet: open the web app, sign in and enter this claim code",
				"claim_code", cfg.Auth.Supabase.ClaimCode)
		}
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
	for _, u := range api.LocalURLs(cfg) {
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

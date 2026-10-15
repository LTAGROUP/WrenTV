// Package config loads Wren TV server configuration from a YAML file in the
// data directory, with environment-variable overrides (WRENTV_*, or the
// legacy HAVEN_*).
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	// DataDir holds the database, caches and transcode scratch space.
	DataDir string `yaml:"-"`

	ServerName string `yaml:"server_name"`
	// MachineID uniquely identifies this server install. Generated on first run.
	MachineID string `yaml:"machine_id"`
	Listen    string `yaml:"listen"`
	// PublicURL is how external services (webhook receivers) reach this server.
	PublicURL string `yaml:"public_url"`
	// LocalURLs overrides the LAN addresses advertised to apps (detected from
	// network interfaces by default, which inside Docker are container IPs).
	LocalURLs []string `yaml:"local_urls,omitempty"`
	// IntegrationKey authenticates backend services (e.g. a Riven-like source
	// manager) calling the integration API. Generated on first run.
	IntegrationKey string `yaml:"integration_key"`

	Auth      AuthConfig      `yaml:"auth"`
	Metadata  MetadataConfig  `yaml:"metadata"`
	Transcode TranscodeConfig `yaml:"transcode"`
	Torrents  TorrentConfig   `yaml:"torrents"`
	Webhooks  []WebhookConfig `yaml:"webhooks"`
	Scanner   ScannerConfig   `yaml:"scanner"`
	Markers   MarkersConfig   `yaml:"markers"`
}

// MarkersConfig controls intro/credits detection. Chapter names are always
// used; Fingerprint additionally compares the audio of episodes in a season
// (a background job) to find intros and credits in files without chapters.
type MarkersConfig struct {
	Fingerprint bool `yaml:"fingerprint"`
}

type AuthConfig struct {
	// Mode is "local" (accounts stored on this server) or "supabase"
	// (Wren TV accounts: sign in once, link devices, share servers).
	Mode string `yaml:"mode"`
	// SigningKey signs locally issued tokens. Generated on first run.
	SigningKey string         `yaml:"signing_key"`
	Supabase   SupabaseConfig `yaml:"supabase"`
}

type SupabaseConfig struct {
	URL            string `yaml:"url"`
	PublishableKey string `yaml:"publishable_key"`
	// ServerID and ServerSecret are filled in when the server is claimed.
	ServerID     string `yaml:"server_id"`
	ServerSecret string `yaml:"server_secret"`
	// ClaimCode must be entered to claim an unclaimed server (printed in the
	// log at startup), so whoever reaches it first over the internet can't.
	ClaimCode string `yaml:"claim_code,omitempty"`
}

type MetadataConfig struct {
	TMDBAPIKey string `yaml:"tmdb_api_key"`
	// OMDbAPIKey enables IMDb, Rotten Tomatoes and Metacritic ratings
	// (free key: https://www.omdbapi.com/apikey.aspx).
	OMDbAPIKey string `yaml:"omdb_api_key"`
	Language   string `yaml:"language"`
	// Workers is the number of concurrent metadata lookups.
	Workers int `yaml:"workers"`
}

type TranscodeConfig struct {
	FFmpegPath  string `yaml:"ffmpeg_path"`
	FFprobePath string `yaml:"ffprobe_path"`
	// HWAccel: "auto", "none", "videotoolbox", "nvenc", "qsv", "vaapi", "amf".
	HWAccel     string `yaml:"hwaccel"`
	VAAPIDevice string `yaml:"vaapi_device"`
	// ScratchDir defaults to <data>/transcode.
	ScratchDir string `yaml:"scratch_dir"`
}

type TorrentConfig struct {
	Enabled bool `yaml:"enabled"`
	// Providers are tried in order. Supported: "debrid", "builtin".
	Providers   []string          `yaml:"providers"`
	DownloadDir string            `yaml:"download_dir"`
	ListenPort  int               `yaml:"listen_port"`
	Debrid      DebridConfig      `yaml:"debrid"`
	Health      TorrentHealthConf `yaml:"health"`
}

type DebridConfig struct {
	// Service: "realdebrid" (more can be added behind the same interface).
	Service string `yaml:"service"`
	APIKey  string `yaml:"api_key"`
}

type TorrentHealthConf struct {
	// MetadataTimeoutSec: give up if torrent metadata is not received.
	MetadataTimeoutSec int `yaml:"metadata_timeout_sec"`
	// StallTimeoutSec: a stream that makes no progress for this long is unplayable.
	StallTimeoutSec int `yaml:"stall_timeout_sec"`
	// MinPeers below which (after the metadata timeout) a torrent is unhealthy.
	MinPeers int `yaml:"min_peers"`
	// ReplaceTimeoutMin: how long an unplayable torrent waits for a backend
	// to supply a replacement before it is marked failed.
	ReplaceTimeoutMin int `yaml:"replace_timeout_min"`
}

type WebhookConfig struct {
	URL string `yaml:"url" json:"url"`
	// Secret signs the payload: X-WrenTV-Signature: sha256=<hex hmac>.
	Secret string `yaml:"secret" json:"secret,omitempty"`
	// Events to send; empty means all.
	Events []string `yaml:"events" json:"events"`
}

type ScannerConfig struct {
	// Watch enables filesystem notifications for instant pickup of new files.
	Watch bool `yaml:"watch"`
	// PollSec is how often the watcher checks folders for changes (0 = 5s on
	// macOS, 30s elsewhere). Network/FUSE mounts rely on this.
	PollSec int `yaml:"poll_sec"`
	// IntervalMin runs a full incremental rescan this often as a safety net
	// (catches anything the watcher missed). 0 disables.
	IntervalMin int `yaml:"interval_min"`
	// ProbeWorkers is the number of concurrent background ffprobe runs.
	// -1 disables background probing (files are probed on first play), which
	// suits debrid/cloud mounts where every probe downloads data.
	ProbeWorkers int `yaml:"probe_workers"`
}

func defaults() Config {
	host, _ := os.Hostname()
	return Config{
		ServerName: host,
		Listen:     ":32500",
		Auth:       AuthConfig{Mode: "local"},
		Metadata:   MetadataConfig{Language: "en-US", Workers: 8},
		Transcode:  TranscodeConfig{FFmpegPath: "ffmpeg", FFprobePath: "ffprobe", HWAccel: "auto", VAAPIDevice: "/dev/dri/renderD128"},
		Torrents: TorrentConfig{
			Enabled:    true,
			Providers:  []string{"builtin"},
			ListenPort: 42069,
			Debrid:     DebridConfig{Service: "realdebrid"},
			Health:     TorrentHealthConf{MetadataTimeoutSec: 60, StallTimeoutSec: 45, MinPeers: 1, ReplaceTimeoutMin: 30},
		},
		Scanner: ScannerConfig{Watch: true, IntervalMin: 30, ProbeWorkers: 4},
		Markers: MarkersConfig{Fingerprint: true},
	}
}

// DefaultDataDir returns the platform's conventional location.
func DefaultDataDir() string {
	if d := Getenv("DATA_DIR"); d != "" {
		return d
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "/config"
	}
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "Wren TV")
	case "windows":
		if pd := os.Getenv("ProgramData"); pd != "" {
			return filepath.Join(pd, "WrenTV")
		}
		return `C:\ProgramData\WrenTV`
	default:
		if os.Geteuid() == 0 {
			return "/var/lib/wrentv"
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "share", "wrentv")
	}
}

func (c *Config) Path() string { return filepath.Join(c.DataDir, configFile) }

// Load reads <dataDir>/wrentv.yaml, creating it with defaults if missing, and
// applies environment overrides. Generated secrets are persisted.
func Load(dataDir string) (*Config, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	c := defaults()
	c.DataDir = dataDir
	b, err := os.ReadFile(c.Path())
	switch {
	case err == nil:
		if err := yaml.Unmarshal(b, &c); err != nil {
			return nil, fmt.Errorf("parse %s: %w", c.Path(), err)
		}
	case !os.IsNotExist(err):
		return nil, err
	}

	dirty := false
	if c.MachineID == "" {
		c.MachineID = randHex(16)
		dirty = true
	}
	if c.IntegrationKey == "" {
		c.IntegrationKey = "wtv_" + randHex(24)
		dirty = true
	}
	if c.Auth.SigningKey == "" {
		c.Auth.SigningKey = randHex(32)
		dirty = true
	}
	if dirty || os.IsNotExist(err) {
		if err := c.Save(); err != nil {
			return nil, err
		}
	}
	c.applyEnv()
	if c.Transcode.ScratchDir == "" {
		c.Transcode.ScratchDir = filepath.Join(dataDir, "transcode")
	}
	if c.Torrents.DownloadDir == "" {
		c.Torrents.DownloadDir = filepath.Join(dataDir, "torrents")
	}
	return &c, nil
}

func (c *Config) applyEnv() {
	set := func(key string, dst *string) {
		if v, ok := LookupEnv(key); ok {
			*dst = v
		}
	}
	set("LISTEN", &c.Listen)
	set("SERVER_NAME", &c.ServerName)
	set("AUTH_MODE", &c.Auth.Mode)
	set("SUPABASE_URL", &c.Auth.Supabase.URL)
	set("SUPABASE_PUBLISHABLE_KEY", &c.Auth.Supabase.PublishableKey)
	set("TMDB_API_KEY", &c.Metadata.TMDBAPIKey)
	set("OMDB_API_KEY", &c.Metadata.OMDbAPIKey)
	set("FFMPEG", &c.Transcode.FFmpegPath)
	set("FFPROBE", &c.Transcode.FFprobePath)
	set("HWACCEL", &c.Transcode.HWAccel)
	set("DEBRID_API_KEY", &c.Torrents.Debrid.APIKey)
	set("PUBLIC_URL", &c.PublicURL)
	if v, err := strconv.Atoi(Getenv("SCAN_POLL_SEC")); err == nil {
		c.Scanner.PollSec = v
	}
	if v := Getenv("LOCAL_URLS"); v != "" {
		c.LocalURLs = nil
		for _, u := range strings.Split(v, ",") {
			if u = strings.TrimSpace(u); u != "" {
				c.LocalURLs = append(c.LocalURLs, u)
			}
		}
	}
	if v, ok := LookupEnv("TORRENT_PROVIDERS"); ok {
		c.Torrents.Providers = strings.Split(v, ",")
	}
}

// Save writes the config back to disk (used for generated secrets and claim state).
func (c *Config) Save() error {
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp := c.Path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.Path())
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

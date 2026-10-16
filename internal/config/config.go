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

	Auth         AuthConfig         `yaml:"auth"`
	RemoteAccess RemoteAccessConfig `yaml:"remote_access"`
	Metadata     MetadataConfig     `yaml:"metadata"`
	Transcode    TranscodeConfig    `yaml:"transcode"`
	Streaming    StreamingConfig    `yaml:"streaming"`
	Users        UserLimitsConfig   `yaml:"users"`
	Webhooks     []WebhookConfig    `yaml:"webhooks"`
	Scanner      ScannerConfig      `yaml:"scanner"`
	Markers      MarkersConfig      `yaml:"markers"`

	// LegacyTorrents is read from configs written before streaming replaced
	// torrents, so a Real-Debrid key carries over. Never written back.
	LegacyTorrents *legacyTorrents `yaml:"torrents,omitempty"`
}

type legacyTorrents struct {
	Debrid struct {
		Service string `yaml:"service"`
		APIKey  string `yaml:"api_key"`
	} `yaml:"debrid"`
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

// RemoteAccessConfig controls the automatic HTTPS addresses under wrentv.net
// (servers using Wren TV accounts only): https names for each LAN address,
// plus a public one when the server is reachable from the internet.
type RemoteAccessConfig struct {
	Enabled bool `yaml:"enabled"`
	// UPnP asks the router to forward a port (UPnP IGD or NAT-PMP).
	UPnP bool `yaml:"upnp"`
	// PublicPort is a port the owner forwarded by hand to this server's
	// listen port. Set, it replaces automatic port mapping.
	PublicPort int `yaml:"public_port,omitempty"`
	// API and ACMEDirectory override the address service and the certificate
	// authority (for testing, e.g. Let's Encrypt staging).
	API           string `yaml:"api,omitempty"`
	ACMEDirectory string `yaml:"acme_directory,omitempty"`
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

// StreamingConfig turns the server into a shared Stremio-style client:
// any movie or show can be searched and played, resolved through Stremio
// addons to releases that are already cached on the owner's debrid accounts.
// Only the stream (info hash and file) is stored; bytes are proxied on demand.
// In download mode the file is also saved into a real library.
type StreamingConfig struct {
	Enabled bool `yaml:"enabled"`
	// Debrid accounts (owner's keys, shared by every user). Providers are
	// tried in ProviderOrder; a stream plays through the first that has it cached.
	TorBoxKey     string   `yaml:"torbox_key"`
	AllDebridKey  string   `yaml:"alldebrid_key"`
	RealDebridKey string   `yaml:"realdebrid_key"`
	ProviderOrder []string `yaml:"provider_order"`
	// Addons are Stremio addon manifests queried for releases.
	Addons []AddonConfig `yaml:"addons"`
	// Qualities resolved and stored for every title, best first.
	Qualities []string `yaml:"qualities"`
	// MaxSizeGB skips releases larger than this per quality (0 = no limit).
	MaxSizeGB map[string]float64 `yaml:"max_size_gb"`
	// Prefetch resolves the rest of the season after an episode is played.
	Prefetch bool `yaml:"prefetch"`
	// Mode: "stream" stores only the stream; "download" also saves the file
	// into the download library, which then replaces the stream.
	Mode string `yaml:"mode"`
	// MovieLibrary and ShowLibrary are the real libraries downloads go into.
	MovieLibrary    string          `yaml:"movie_library"`
	ShowLibrary     string          `yaml:"show_library"`
	DownloadQuality string          `yaml:"download_quality"`
	Limits          StreamingLimits `yaml:"limits"`
}

type AddonConfig struct {
	Name    string `yaml:"name" json:"name"`
	URL     string `yaml:"url" json:"url"`
	Enabled bool   `yaml:"enabled" json:"enabled"`
}

// StreamingLimits bound what streaming may cost the server. 0 means no limit.
type StreamingLimits struct {
	// MaxStreams is the most debrid-backed playbacks at once (all users).
	MaxStreams int `yaml:"max_streams" json:"maxStreams"`
	// BandwidthMbps caps all debrid traffic through the server; PerStreamMbps
	// caps one playback; DownloadMbps caps server downloads.
	BandwidthMbps float64 `yaml:"bandwidth_mbps" json:"bandwidthMbps"`
	PerStreamMbps float64 `yaml:"per_stream_mbps" json:"perStreamMbps"`
	DownloadMbps  float64 `yaml:"download_mbps" json:"downloadMbps"`
	// MaxDownloads is how many server downloads run at once.
	MaxDownloads int `yaml:"max_downloads" json:"maxDownloads"`
	// PrefetchWorkers is how many episodes are resolved at once in the background.
	PrefetchWorkers int `yaml:"prefetch_workers" json:"prefetchWorkers"`
	// DownloadQuotaGB stops downloading once downloaded files total this
	// much; MinFreeGB keeps this much disk free.
	DownloadQuotaGB float64 `yaml:"download_quota_gb" json:"downloadQuotaGB"`
	MinFreeGB       float64 `yaml:"min_free_gb" json:"minFreeGB"`
}

// UserLimitsConfig holds defaults for users without their own limits.
type UserLimitsConfig struct {
	// MaxStreams is how many playbacks one user may run at once (0 = no limit).
	MaxStreams int `yaml:"max_streams" json:"maxStreams"`
	// DeviceDownloads lets users save titles to their devices.
	DeviceDownloads bool `yaml:"device_downloads" json:"deviceDownloads"`
	// MaxDownloads is how many device downloads one user may run at once.
	MaxDownloads int `yaml:"max_downloads" json:"maxDownloads"`
}

// DefaultAddons are offered on new servers.
func DefaultAddons() []AddonConfig {
	return []AddonConfig{
		{Name: "torrentio", URL: "https://torrentio.strem.fun/manifest.json", Enabled: true},
		{Name: "comet", URL: "https://comet.feels.legal/manifest.json", Enabled: true},
	}
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
		ServerName:   host,
		Listen:       ":32500",
		Auth:         AuthConfig{Mode: "local"},
		RemoteAccess: RemoteAccessConfig{Enabled: true, UPnP: true},
		Metadata:     MetadataConfig{Language: "en-US", Workers: 8},
		Transcode:    TranscodeConfig{FFmpegPath: "ffmpeg", FFprobePath: "ffprobe", HWAccel: "auto", VAAPIDevice: "/dev/dri/renderD128"},
		Streaming: StreamingConfig{
			ProviderOrder:   []string{"torbox", "alldebrid", "realdebrid"},
			Addons:          DefaultAddons(),
			Qualities:       []string{"2160p", "1080p"},
			MaxSizeGB:       map[string]float64{"2160p": 0, "1080p": 0},
			Prefetch:        true,
			Mode:            "stream",
			DownloadQuality: "2160p",
			Limits:          StreamingLimits{MaxDownloads: 2, PrefetchWorkers: 2, MinFreeGB: 20},
		},
		Users:   UserLimitsConfig{DeviceDownloads: true, MaxDownloads: 2},
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
	if lt := c.LegacyTorrents; lt != nil {
		if lt.Debrid.APIKey != "" && c.Streaming.RealDebridKey == "" && (lt.Debrid.Service == "" || lt.Debrid.Service == "realdebrid") {
			c.Streaming.RealDebridKey = lt.Debrid.APIKey
		}
		c.LegacyTorrents = nil
		dirty = true
	}
	if len(c.Streaming.ProviderOrder) == 0 {
		c.Streaming.ProviderOrder = []string{"torbox", "alldebrid", "realdebrid"}
	}
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
	set("TORBOX_API_KEY", &c.Streaming.TorBoxKey)
	set("ALLDEBRID_API_KEY", &c.Streaming.AllDebridKey)
	set("REALDEBRID_API_KEY", &c.Streaming.RealDebridKey)
	set("PUBLIC_URL", &c.PublicURL)
	set("DIRECT_API", &c.RemoteAccess.API)
	set("ACME_DIRECTORY", &c.RemoteAccess.ACMEDirectory)
	if v, err := strconv.ParseBool(Getenv("REMOTE_ACCESS")); err == nil {
		c.RemoteAccess.Enabled = v
	}
	if v, err := strconv.Atoi(Getenv("PUBLIC_PORT")); err == nil {
		c.RemoteAccess.PublicPort = v
	}
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
	if v, err := strconv.ParseBool(Getenv("STREAMING")); err == nil {
		c.Streaming.Enabled = v
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

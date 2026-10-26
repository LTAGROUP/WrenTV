// Package config loads Wren TV server configuration from a YAML file in the
// data directory, with environment-variable overrides (WRENTV_*).
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
	"time"

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
	LiveTV       LiveTVConfig       `yaml:"live_tv"`
	Sports       SportsConfig       `yaml:"sports"`
	Users        UserLimitsConfig   `yaml:"users"`
	Webhooks     []WebhookConfig    `yaml:"webhooks"`
	Simkl        SimklConfig        `yaml:"simkl"`
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

// The Wren TV account service. Every server uses this one; the publishable
// key is public (every app ships it) and row-level security guards the data.
const (
	SupabaseURL            = "https://buycndonhmcafzzcvmgu.supabase.co"
	SupabasePublishableKey = "sb_publishable_vNiXbucy_8L_RQBFPoBPvA_HNZv1i15"
)

type AuthConfig struct {
	// SigningKey signs locally issued tokens. Generated on first run.
	SigningKey string         `yaml:"signing_key"`
	Supabase   SupabaseConfig `yaml:"supabase"`
}

type SupabaseConfig struct {
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
// LiveTVConfig sets up live TV from IPTV playlists (M3U, or an Xtream Codes
// login). Channels, numbers and the guide are worked out from the playlist;
// the owner only picks which groups to show.
type LiveTVConfig struct {
	Enabled bool           `yaml:"enabled"`
	Sources []LiveTVSource `yaml:"sources"`
	// HiddenGroups are playlist groups left out of the channel list.
	HiddenGroups []string `yaml:"hidden_groups,omitempty"`
	// MaxStreams caps channels playing at once, as providers limit
	// connections. 0 is no limit.
	MaxStreams int `yaml:"max_streams,omitempty"`
}

// NormalizeSourceIDs makes every provider independently addressable. An empty
// or duplicated id would make two providers share playlist caches, channel
// identities and guide rows, so it gets a fresh id instead.
func (l *LiveTVConfig) NormalizeSourceIDs() bool {
	seen := map[string]bool{}
	changed := false
	for i := range l.Sources {
		id := strings.TrimSpace(l.Sources[i].ID)
		if id != l.Sources[i].ID {
			l.Sources[i].ID = id
			changed = true
		}
		if id == "" || seen[id] {
			for {
				id = randHex(4)
				if !seen[id] {
					break
				}
			}
			l.Sources[i].ID = id
			changed = true
		}
		seen[id] = true
	}
	return changed
}

// SportsConfig turns the Sports Hub on and selects the competitions polled
// server-wide. An empty league list with Enabled true means every catalog
// league, matching MediaStorm's default.
type SportsConfig struct {
	// Enabled is nil in configs written before the hub existed, which preserves
	// the enabled default.
	Enabled       *bool                        `yaml:"enabled" json:"enabled"`
	Leagues       []string                     `yaml:"leagues,omitempty" json:"enabledLeagues"`
	FavoriteTeams []string                     `yaml:"favorite_teams,omitempty" json:"favoriteTeamIds"`
	Sources       []string                     `yaml:"sources,omitempty" json:"defaultSourceIds"`
	Groups        []string                     `yaml:"groups,omitempty" json:"defaultCategoryIds"`
	Overrides     map[string]SportsSearchScope `yaml:"overrides,omitempty" json:"leagueSearchOverrides"`
}

// SportsSearchScope limits automatic sports stream discovery for one league.
// Empty lists mean every enabled Live TV source/category.
type SportsSearchScope struct {
	SourceIDs []string `yaml:"sources,omitempty" json:"sourceIds,omitempty"`
	Groups    []string `yaml:"groups,omitempty" json:"categoryIds,omitempty"`
}

// Normalize backfills nil fields so every consumer sees a consistent, non-nil
// shape. It never replaces an explicitly empty league selection.
func (s *SportsConfig) Normalize() {
	if s.Enabled == nil {
		enabled := true
		s.Enabled = &enabled
	}
	if s.Leagues == nil {
		s.Leagues = []string{}
	}
	if s.FavoriteTeams == nil {
		s.FavoriteTeams = []string{}
	}
	if s.Sources == nil {
		s.Sources = []string{}
	}
	if s.Groups == nil {
		s.Groups = []string{}
	}
	if s.Overrides == nil {
		s.Overrides = map[string]SportsSearchScope{}
	}
}

// IsEnabled reports the effective switch.
func (s SportsConfig) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// PollingLeagueIDs is the effective server-wide polling selection. An empty
// list selects the whole catalog; a disabled hub polls nothing.
func (s SportsConfig) PollingLeagueIDs() []string {
	if !s.IsEnabled() {
		return []string{}
	}
	if len(s.Leagues) == 0 {
		return []string{"*"}
	}
	return append([]string(nil), s.Leagues...)
}

// AddonCatalog is one catalog a Stremio addon declares, with the parts of its
// declaration that decide whether Wren TV can browse it without guessing.
type AddonCatalog struct {
	Type string `yaml:"type" json:"type"`
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// Searchable: the catalog accepts a search extra. Browsable: it also lists
	// items without a query. Unsupported names any required extra Wren TV
	// cannot supply, which makes the catalog unusable rather than absent.
	Searchable  bool     `yaml:"searchable,omitempty" json:"searchable"`
	Browsable   bool     `yaml:"browsable,omitempty" json:"browsable"`
	Skip        bool     `yaml:"skip,omitempty" json:"skip"`
	Unsupported []string `yaml:"unsupported,omitempty" json:"unsupported,omitempty"`
}

// AddonTypeModel is what one declared content type can do on this addon.
// Model is the browsing model the viewer UI uses: "channels" for television
// channels imported into Live TV, "events" for sports events and "catalog"
// for any other content type that lists items and plays streams.
type AddonTypeModel struct {
	Type     string `yaml:"type" json:"type"`
	Model    string `yaml:"model" json:"model"`
	Catalogs int    `yaml:"catalogs,omitempty" json:"catalogs"`
	Catalog  bool   `yaml:"catalog,omitempty" json:"catalog"`
	Meta     bool   `yaml:"meta,omitempty" json:"meta"`
	Stream   bool   `yaml:"stream,omitempty" json:"stream"`
}

// AddonCapabilities is what an addon's manifest declares, discovered once and
// stored with the source. It separates a valid manifest from a usable one: an
// addon may be accepted with no TV channels, no catalogs, or no streams, and
// the limitations say so without pretending it is an invalid manifest.
type AddonCapabilities struct {
	ID          string   `yaml:"id,omitempty" json:"id,omitempty"`
	Name        string   `yaml:"name,omitempty" json:"name,omitempty"`
	Version     string   `yaml:"version,omitempty" json:"version,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Logo        string   `yaml:"logo,omitempty" json:"logo,omitempty"`
	Types       []string `yaml:"types,omitempty" json:"types"`
	// Resources are the resource names the addon declares: catalog, meta and
	// stream are the ones Wren TV uses; others are recorded but not requested.
	Resources []string         `yaml:"resources,omitempty" json:"resources"`
	Catalogs  []AddonCatalog   `yaml:"catalogs,omitempty" json:"catalogs"`
	Models    []AddonTypeModel `yaml:"models,omitempty" json:"models"`
	// Browsable is true when at least one catalog can be listed, with or
	// without a search query. Playable is true when a declared content type
	// offers the stream resource.
	Browsable bool `yaml:"browsable,omitempty" json:"browsable"`
	Playable  bool `yaml:"playable,omitempty" json:"playable"`
	// ConfigurationRequired is the manifest's own behaviorHints flag: the addon
	// is valid but its manifest link is not the configured one yet.
	ConfigurationRequired bool      `yaml:"configuration_required,omitempty" json:"configurationRequired"`
	Limitations           []string  `yaml:"limitations,omitempty" json:"limitations"`
	Checked               time.Time `yaml:"checked,omitempty" json:"checked,omitzero"`
}

// HasTVChannels reports whether any declared catalog is a TV channel list.
func (c *AddonCapabilities) HasTVChannels() bool {
	if c == nil {
		return false
	}
	for _, cat := range c.Catalogs {
		if strings.EqualFold(strings.TrimSpace(cat.Type), "tv") {
			return true
		}
	}
	return false
}

// SportCatalogs counts the declarative sport shelves.
func (c *AddonCapabilities) SportCatalogs() int {
	if c == nil {
		return 0
	}
	n := 0
	for _, cat := range c.Catalogs {
		if strings.EqualFold(strings.TrimSpace(cat.Type), "sport") {
			n++
		}
	}
	return n
}

// HasBrowsableModel reports whether the addon has content that is not imported
// as television channels: sports events or any other browsable catalog type.
func (c *AddonCapabilities) HasBrowsableModel() bool {
	if c == nil {
		return false
	}
	for _, model := range c.Models {
		if model.Model != "channels" && model.Catalogs > 0 {
			return true
		}
	}
	return false
}

type LiveTVSource struct {
	ID      string `yaml:"id" json:"id"`
	Name    string `yaml:"name" json:"name"`
	Enabled bool   `yaml:"enabled" json:"enabled"`
	// Kind is "m3u" (a playlist URL), "xtream" (a server with a username
	// and password) or "stremio" / "sport" (a Stremio addon manifest). The
	// addon kinds are legacy hints: what an addon can do is read from its
	// manifest into Capabilities, not from the kind the admin picked.
	Kind string `yaml:"kind" json:"kind"`
	// URL is the playlist, the Xtream server's address, or the addon's manifest.
	URL      string `yaml:"url" json:"url"`
	Username string `yaml:"username,omitempty" json:"username"`
	Password string `yaml:"password,omitempty" json:"password"`
	// EPGURL is the XMLTV guide. Blank finds it on its own: the playlist's
	// url-tvg header, or the Xtream server's guide.
	EPGURL    string `yaml:"epg_url,omitempty" json:"epgUrl"`
	UserAgent string `yaml:"user_agent,omitempty" json:"userAgent"`
	// Capabilities is the discovered manifest for an addon source. It is kept
	// so the settings page and the Live TV picker describe the addon without
	// asking it again; a missing value means "not discovered yet", never
	// "invalid". Playlists and Xtream sources leave it empty.
	Capabilities *AddonCapabilities `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
}

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
	// PerQuality is how many streams each quality keeps for users to choose
	// from; the first plays by default.
	PerQuality int `yaml:"per_quality"`
	// Usenet plays Usenet posts before torrents.
	Usenet bool `yaml:"usenet"`
	// Indexers are searched for Usenet posts, which play straight from
	// NewsServers: articles are fetched as the player reads them.
	Indexers    []IndexerConfig    `yaml:"indexers"`
	NewsServers []NewsServerConfig `yaml:"news_servers"`
	// UsenetCacheMB keeps recently read articles in memory for every stream
	// (0 = 256 MB); UsenetDiskCacheGB keeps more on disk (0 = none).
	UsenetCacheMB     int     `yaml:"usenet_cache_mb"`
	UsenetDiskCacheGB float64 `yaml:"usenet_disk_cache_gb"`
	// ExcludeWords skips releases whose name has any of these: whole words
	// ("TS" doesn't match "DTS"), phrases ("korean dub" matches
	// "Korean.Dub"), or /regular expressions/.
	ExcludeWords []string `yaml:"exclude_words"`
	// MaxSizeGB skips releases larger than this per quality (0 = no limit).
	MaxSizeGB map[string]float64 `yaml:"max_size_gb"`
	// Prefetch looks streams up ahead of play: the rest of the season after
	// an episode is played, and what's on watchlists.
	Prefetch bool `yaml:"prefetch"`
	// LookupOnOpen starts finding a movie's or episode's streams when its
	// details page opens (a show's next episode too), so play starts sooner.
	LookupOnOpen bool `yaml:"lookup_on_open"`
	// CheckAddonLinks opens every addon play link while looking up streams,
	// so dead ones are dropped before play. Off, links from addons that give
	// a size are only opened when played (debrid links are always checked).
	CheckAddonLinks bool `yaml:"check_addon_links"`
	// Mode: "stream" stores only the stream; "download" also saves the file
	// into the download library, which then replaces the stream.
	Mode string `yaml:"mode"`
	// MovieLibrary and ShowLibrary are the real libraries downloads go into.
	MovieLibrary    string          `yaml:"movie_library"`
	ShowLibrary     string          `yaml:"show_library"`
	DownloadQuality string          `yaml:"download_quality"`
	Limits          StreamingLimits `yaml:"limits"`
}

type IndexerConfig struct {
	Name    string `yaml:"name" json:"name"`
	URL     string `yaml:"url" json:"url"`
	APIKey  string `yaml:"api_key" json:"-"`
	Enabled bool   `yaml:"enabled" json:"enabled"`
}

// NewsServerConfig is a Usenet provider account. Servers are used in
// order; later ones fill in articles the first lacks.
type NewsServerConfig struct {
	Host     string `yaml:"host" json:"host"`
	Port     int    `yaml:"port" json:"port"`
	TLS      bool   `yaml:"tls" json:"tls"`
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"-"`
	// Connections is the most this server opens at once (the plan's limit).
	Connections int  `yaml:"connections" json:"connections"`
	Enabled     bool `yaml:"enabled" json:"enabled"`
	// Backup servers are only asked for articles the others don't have.
	Backup bool `yaml:"backup" json:"backup"`
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

// SimklConfig lets users link Simkl accounts for scrobbling and importing
// watch history. ClientID is an AUTH V2 app registered at
// simkl.com/settings/developer.
type SimklConfig struct {
	ClientID string `yaml:"client_id"`
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
		RemoteAccess: RemoteAccessConfig{Enabled: true, UPnP: true},
		Metadata:     MetadataConfig{Language: "en-US", Workers: 8},
		Transcode:    TranscodeConfig{FFmpegPath: "ffmpeg", FFprobePath: "ffprobe", HWAccel: "auto", VAAPIDevice: "/dev/dri/renderD128"},
		Streaming: StreamingConfig{
			ProviderOrder:   []string{"torbox", "alldebrid", "realdebrid"},
			Addons:          DefaultAddons(),
			Qualities:       []string{"2160p", "1080p"},
			PerQuality:      3,
			Usenet:          true,
			MaxSizeGB:       map[string]float64{"2160p": 0, "1080p": 0},
			Prefetch:        true,
			LookupOnOpen:    true,
			Mode:            "stream",
			DownloadQuality: "2160p",
			Limits:          StreamingLimits{MaxDownloads: 2, PrefetchWorkers: 2, MinFreeGB: 20},
		},
		Sports:  SportsConfig{},
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
	c.Sports.Normalize()
	if c.LiveTV.NormalizeSourceIDs() {
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
	if v, err := strconv.ParseBool(Getenv("SPORTS")); err == nil {
		c.Sports.Enabled = &v
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

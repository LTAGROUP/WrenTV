// Package direct gives every claimed server automatic HTTPS addresses under
// wrentv.net (like Plex's plex.direct):
//
//	https://192-168-1-20.<id>.wrentv.net:32500   (each LAN address)
//	https://203-0-113-7.<id>.wrentv.net:<port>   (public address, when reachable)
//
// The wrentv-direct API (a Cloudflare Worker) publishes the DNS records after
// checking the server secret; this package forwards the port on the router,
// gets a wildcard Let's Encrypt cert for *.<id>.wrentv.net with a DNS-01
// challenge, and serves it. Media always goes straight to the server.
package direct

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

const (
	defaultAPI = "https://api.wrentv.net"
	syncEvery  = 15 * time.Minute
)

// Status is what the admin page shows.
type Status struct {
	Enabled     bool      `json:"enabled"`
	Domain      string    `json:"domain,omitempty"`
	LocalURLs   []string  `json:"localUrls"`
	RemoteURL   string    `json:"remoteUrl,omitempty"`
	PublicIP    string    `json:"publicIp,omitempty"`
	PortMapping string    `json:"portMapping,omitempty"` // "upnp", "nat-pmp", "manual" or ""
	Reachable   bool      `json:"reachable"`
	CertExpires time.Time `json:"certExpires,omitzero"`
	Problem     string    `json:"problem,omitempty"`
	CheckedAt   time.Time `json:"checkedAt,omitzero"`
}

type Manager struct {
	cfg      *config.Config
	store    certStore
	api      *apiClient
	lanURLs  func() []string
	onChange func()
	wake     chan struct{}

	mu       sync.Mutex
	cert     *tls.Certificate
	status   Status
	ports    *portMapper
	retryAt  time.Time // after a failed issuance
	failures int
}

// New prepares the manager. lanURLs returns the http LAN addresses the server
// already advertises (their hosts become wrentv.net names). onChange runs
// when the published addresses change, so the heartbeat can go out promptly.
func New(cfg *config.Config, lanURLs func() []string, onChange func()) *Manager {
	m := &Manager{
		cfg: cfg, store: certStore{dir: filepath.Join(cfg.DataDir, "certs", "direct")},
		lanURLs: lanURLs, onChange: onChange, wake: make(chan struct{}, 1),
		api: &apiClient{cfg: cfg, http: &http.Client{Timeout: 30 * time.Second, Transport: ipv4Transport()}},
	}
	return m
}

// TLSConfig serves the current wildcard cert. Handshakes fail until one exists.
func (m *Manager) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.cert == nil {
				return nil, errors.New("no certificate yet")
			}
			return m.cert, nil
		},
	}
}

// Status returns a copy of the current state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.status
	s.LocalURLs = append([]string{}, s.LocalURLs...)
	return s
}

// Refresh asks the background loop to run now (after a settings change).
func (m *Manager) Refresh() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Run keeps addresses, port mapping and certificate current until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	defer func() {
		m.mu.Lock()
		p := m.ports
		m.mu.Unlock()
		if p != nil {
			p.remove()
		}
	}()
	t := time.NewTicker(syncEvery)
	defer t.Stop()
	for {
		m.sync(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.wake:
		}
	}
}

func (m *Manager) sync(ctx context.Context) {
	before := m.Status()
	st := m.check(ctx)
	st.CheckedAt = time.Now()
	m.mu.Lock()
	m.status = st
	m.mu.Unlock()
	if st.Problem != "" && st.Problem != before.Problem {
		slog.Warn("remote access", "problem", st.Problem)
	}
	if st.RemoteURL != before.RemoteURL || strings.Join(st.LocalURLs, ",") != strings.Join(before.LocalURLs, ",") {
		if st.RemoteURL != "" {
			slog.Info("remote access ready", "url", st.RemoteURL)
		}
		if m.onChange != nil {
			m.onChange()
		}
	}
}

func (m *Manager) check(ctx context.Context) Status {
	rc := m.cfg.RemoteAccess
	sb := m.cfg.Auth.Supabase
	if !rc.Enabled || m.cfg.Auth.Mode != "supabase" || sb.ServerID == "" {
		return Status{Enabled: rc.Enabled, LocalURLs: []string{}}
	}
	st := Status{Enabled: true, LocalURLs: []string{}}

	// LAN addresses come from the http URLs the server advertises.
	type lan struct{ ip, port string }
	var lans []lan
	listenPort := ""
	for _, u := range m.lanURLs() {
		pu, err := url.Parse(u)
		if err != nil {
			continue
		}
		if ip := net.ParseIP(pu.Hostname()); ip != nil && ip.To4() != nil {
			lans = append(lans, lan{ip.String(), pu.Port()})
			if listenPort == "" {
				listenPort = pu.Port()
			}
		}
	}
	if _, p, err := net.SplitHostPort(m.cfg.Listen); err == nil && p != "" {
		listenPort = p
	}
	internal, _ := strconv.Atoi(listenPort)

	// Forward a port: the owner's manual setting wins over UPnP / NAT-PMP.
	extPort := 0
	var routerIP net.IP
	switch {
	case rc.PublicPort > 0:
		extPort, st.PortMapping = rc.PublicPort, "manual"
	case rc.UPnP && internal > 0:
		m.mu.Lock()
		if m.ports == nil || m.ports.internal != internal {
			m.ports = &portMapper{internal: internal}
		}
		pm := m.ports
		m.mu.Unlock()
		port, ip, err := pm.mapping()
		if err == nil {
			extPort, routerIP, st.PortMapping = port, ip, pm.via
		}
	}

	ips := make([]string, len(lans))
	for i, l := range lans {
		ips[i] = l.ip
	}
	res, err := m.api.addresses(ctx, ips, extPort)
	if err != nil {
		// The DNS records outlive a short outage, so keep publishing what worked.
		prev := m.Status()
		prev.Problem = "Couldn't reach the Wren TV address service: " + err.Error()
		return prev
	}
	st.Domain, st.PublicIP, st.Reachable = res.Domain, res.PublicIP, res.Reachable

	cert, problem := m.ensureCert(ctx, res.Domain)
	if cert == nil {
		st.Problem = problem
		return st
	}
	st.CertExpires = cert.Leaf.NotAfter
	for _, l := range lans {
		st.LocalURLs = append(st.LocalURLs, "https://"+net.JoinHostPort(dashed(l.ip)+"."+res.Domain, l.port))
	}
	switch {
	case res.Reachable:
		st.RemoteURL = "https://" + net.JoinHostPort(dashed(res.PublicIP)+"."+res.Domain, strconv.Itoa(extPort))
	case routerIP != nil && !isPublic(routerIP):
		st.Problem = "Remote access isn't available on this network: your internet provider shares one public IP between customers (CGNAT). Wren TV still works at home."
	case extPort == 0:
		st.Problem = "Your router didn't accept an automatic port forward (UPnP / NAT-PMP). Forward a TCP port to port " + listenPort + " on this computer and enter it under Remote access."
	default:
		st.Problem = fmt.Sprintf("Port %d isn't reachable from the internet. Check the port forward on your router.", extPort)
	}
	if problem != "" && st.Problem == "" {
		st.Problem = problem
	}
	return st
}

// ensureCert loads or issues the wildcard cert for domain. It keeps serving a
// still-valid cert when renewal fails, and backs off after failures.
func (m *Manager) ensureCert(ctx context.Context, domain string) (*tls.Certificate, string) {
	cur := m.useStoredCert(domain)
	now := time.Now()
	if !needsRenewal(cur, now) {
		return cur, ""
	}
	m.mu.Lock()
	wait := now.Before(m.retryAt)
	m.mu.Unlock()
	if wait {
		if cur != nil && now.Before(cur.Leaf.NotAfter) {
			return cur, ""
		}
		return nil, "Waiting to retry getting an HTTPS certificate."
	}
	c, err := m.issue(ctx, domain)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.failures++
		backoff := time.Hour << min(m.failures-1, 4) // 1h, 2h … 16h
		m.retryAt = now.Add(min(backoff, 24*time.Hour))
		slog.Warn("couldn't get an HTTPS certificate", "err", err, "retry", m.retryAt.Format(time.Kitchen))
		if cur != nil && now.Before(cur.Leaf.NotAfter) {
			return cur, ""
		}
		return nil, "Couldn't get an HTTPS certificate: " + err.Error()
	}
	m.failures, m.retryAt, m.cert = 0, time.Time{}, c
	return c, ""
}

// useStoredCert returns the cert for domain, loading it from disk if needed.
func (m *Manager) useStoredCert(domain string) *tls.Certificate {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cert != nil && m.cert.Leaf.DNSNames[0] == "*."+domain {
		return m.cert
	}
	if c, err := m.store.load(domain); err == nil {
		m.cert = c
		return c
	}
	return nil
}

func (m *Manager) acmeDirectory() string {
	if d := m.cfg.RemoteAccess.ACMEDirectory; d != "" {
		return d
	}
	return "https://acme-v02.api.letsencrypt.org/directory"
}

func dashed(ip string) string { return strings.ReplaceAll(ip, ".", "-") }

func isPublic(ip net.IP) bool {
	cgnat := &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !cgnat.Contains(ip)
}

// ---------- wrentv-direct API ----------

type apiClient struct {
	cfg  *config.Config
	http *http.Client
}

type addressResult struct {
	DirectID  string `json:"directId"`
	Domain    string `json:"domain"`
	PublicIP  string `json:"publicIp"`
	Reachable bool   `json:"reachable"`
}

func (a *apiClient) addresses(ctx context.Context, lan []string, port int) (*addressResult, error) {
	var out addressResult
	err := a.post(ctx, "/v1/addresses", map[string]any{"lan": lan, "port": port}, &out)
	if err == nil && out.Domain == "" {
		err = errors.New("no domain in response")
	}
	return &out, err
}

// setACME publishes (or with nil, clears) the DNS-01 values and returns the
// zone's nameservers.
func (a *apiClient) setACME(ctx context.Context, values []string) ([]string, error) {
	var out struct {
		Nameservers []string `json:"nameservers"`
	}
	if values == nil {
		values = []string{}
	}
	err := a.post(ctx, "/v1/acme", map[string]any{"values": values}, &out)
	return out.Nameservers, err
}

func (a *apiClient) post(ctx context.Context, path string, body map[string]any, out any) error {
	body["serverId"] = a.cfg.Auth.Supabase.ServerID
	body["secret"] = a.cfg.Auth.Supabase.ServerSecret
	b, _ := json.Marshal(body)
	base := strings.TrimSuffix(a.cfg.RemoteAccess.API, "/")
	if base == "" {
		base = defaultAPI
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rb, &e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return errors.New(e.Error)
	}
	return json.Unmarshal(rb, out)
}

// The API learns the public IPv4 address from the request, so it must go over IPv4.
func ipv4Transport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	d := &net.Dialer{Timeout: 10 * time.Second}
	t.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
		return d.DialContext(ctx, "tcp4", addr)
	}
	return t
}

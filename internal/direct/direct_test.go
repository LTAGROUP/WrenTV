package direct

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/config"
)

func TestSplitServesHTTPAndHTTPSOnOnePort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cert := selfSigned(t, "*.abc.wrentv.net", time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS == nil {
				io.WriteString(w, "plain")
			} else {
				io.WriteString(w, "tls "+r.Proto)
			}
		}),
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{*cert}},
	}
	plain, secure := Split(ln)
	tlsSrv := &http.Server{Handler: srv.Handler, TLSConfig: srv.TLSConfig}
	go srv.Serve(plain)
	go tlsSrv.ServeTLS(secure, "", "")
	defer srv.Close()
	defer tlsSrv.Close()

	addr := ln.Addr().String()
	get := func(c *http.Client, u string) string {
		t.Helper()
		resp, err := c.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if got := get(http.DefaultClient, "http://"+addr+"/"); got != "plain" {
		t.Fatalf("http: got %q", got)
	}
	insecure := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, ForceAttemptHTTP2: true}}
	if got := get(insecure, "https://"+addr+"/"); got != "tls HTTP/2.0" {
		t.Fatalf("https: got %q", got)
	}
}

func TestCheckPublishesHTTPSAddresses(t *testing.T) {
	var gotBody map[string]any
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/addresses" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(addressResult{DirectID: "abc", Domain: "abc.wrentv.net", PublicIP: "203.0.113.7", Reachable: true})
	}))
	defer api.Close()

	cfg := &config.Config{DataDir: t.TempDir(), Listen: ":32500"}
	cfg.Auth.Supabase.ServerID, cfg.Auth.Supabase.ServerSecret = "srv", "sec"
	cfg.RemoteAccess = config.RemoteAccessConfig{Enabled: true, PublicPort: 40000, API: api.URL}
	m := New(cfg, func() []string { return []string{"http://192.168.1.20:32500", "http://10.0.0.5:32500"} }, nil)
	m.api.http = api.Client()
	storeCert(t, m.store, selfSigned(t, "*.abc.wrentv.net", time.Now().Add(-time.Hour), time.Now().Add(80*24*time.Hour)))

	st := m.check(context.Background())
	if st.Problem != "" {
		t.Fatalf("problem: %s", st.Problem)
	}
	wantLAN := []string{"https://192-168-1-20.abc.wrentv.net:32500", "https://10-0-0-5.abc.wrentv.net:32500"}
	if strings.Join(st.LocalURLs, " ") != strings.Join(wantLAN, " ") {
		t.Fatalf("local urls = %v", st.LocalURLs)
	}
	if st.RemoteURL != "https://203-0-113-7.abc.wrentv.net:40000" || st.PortMapping != "manual" {
		t.Fatalf("remote = %q via %q", st.RemoteURL, st.PortMapping)
	}
	if gotBody["serverId"] != "srv" || gotBody["secret"] != "sec" || gotBody["port"].(float64) != 40000 {
		t.Fatalf("api body = %v", gotBody)
	}
}

func TestNeedsRenewal(t *testing.T) {
	now := time.Now()
	fresh := selfSigned(t, "*.x", now.Add(-10*24*time.Hour), now.Add(80*24*time.Hour))
	old := selfSigned(t, "*.x", now.Add(-70*24*time.Hour), now.Add(20*24*time.Hour))
	if needsRenewal(fresh, now) || !needsRenewal(old, now) || !needsRenewal(nil, now) {
		t.Fatal("renewal thresholds wrong")
	}
}

func selfSigned(t *testing.T, name string, from, to time.Time) *tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{name}, NotBefore: from, NotAfter: to}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

func storeCert(t *testing.T, s certStore, c *tls.Certificate) {
	t.Helper()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.save(c.Certificate, c.PrivateKey.(*ecdsa.PrivateKey)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "cert.pem")); err != nil {
		t.Fatal(err)
	}
}

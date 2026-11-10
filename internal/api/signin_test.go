package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LTAGROUP/wrentv/internal/auth"
	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
)

func signinServer(t *testing.T) (*Server, func(method, path string, body any, token, ip string) (int, map[string]any), *db.User) {
	t.Helper()
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	hash, _ := auth.HashPassword("correct horse")
	u := &db.User{Username: "ann", DisplayName: "Ann", PasswordHash: hash, IsAdmin: true}
	if err := d.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	local := auth.NewLocal(d, "test-key")
	s := &Server{Cfg: &config.Config{}, DB: d, Local: local, Devices: auth.NewDevices(d, "test-key", local, nil)}
	s.Auth = auth.WithDevices(local, s.Devices)
	h := s.Router()
	call := func(method, path string, body any, token, ip string) (int, map[string]any) {
		var b bytes.Buffer
		_ = json.NewEncoder(&b).Encode(body)
		req := httptest.NewRequest(method, path, &b)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if ip != "" {
			req.RemoteAddr = ip + ":40000"
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	return s, call, u
}

// Wrong passwords earn a cooldown that even the right password waits out;
// other addresses aren't held up by it.
func TestLoginCooldown(t *testing.T) {
	_, call, _ := signinServer(t)
	bad := map[string]string{"username": "ann", "password": "wrong wrong"}
	good := map[string]string{"username": "ann", "password": "correct horse"}
	for i := 0; i < throttlePairFree; i++ {
		if st, _ := call("POST", "/api/auth/login", bad, "", "203.0.113.5"); st != 401 {
			t.Fatalf("wrong password %d: status %d", i+1, st)
		}
	}
	if st, out := call("POST", "/api/auth/login", good, "", "203.0.113.5"); st != 429 || out["error"] == "" {
		t.Fatalf("during the cooldown: %d %v", st, out)
	}
	if st, out := call("POST", "/api/auth/login", good, "", "203.0.113.6"); st != 200 || out["token"] == "" {
		t.Fatalf("from another address: %d %v", st, out)
	}
}

// A new password, or signing out everywhere, ends older sign-ins and linked
// devices; whoever asked gets a fresh token.
func TestRevokeSignIns(t *testing.T) {
	s, call, u := signinServer(t)
	ctx := context.Background()
	old, _ := s.Local.Issue(u, "Web")
	tv, _ := s.Devices.Issue(u, "TV")
	for _, tok := range []string{old, tv} {
		if st, _ := call("GET", "/api/me/prefs", nil, tok, ""); st != 200 {
			t.Fatalf("token should work before the change: %d", st)
		}
	}
	// Tokens carry the second they were issued in; step past it.
	time.Sleep(1100 * time.Millisecond)

	st, out := call("PATCH", "/api/users/"+u.ID, map[string]string{"password": "a new password"}, old, "")
	fresh, _ := out["token"].(string)
	if st != 200 || fresh == "" {
		t.Fatalf("password change: %d %v", st, out)
	}
	for name, tok := range map[string]string{"old sign-in": old, "linked TV": tv} {
		if st, _ := call("GET", "/api/me/prefs", nil, tok, ""); st != 401 {
			t.Fatalf("%s still works after the password change: %d", name, st)
		}
	}
	if st, _ := call("GET", "/api/me/prefs", nil, fresh, ""); st != 200 {
		t.Fatalf("the fresh token doesn't work: %d", st)
	}
	if _, err := s.Local.Login(ctx, "ann", "a new password"); err != nil {
		t.Fatalf("new password: %v", err)
	}

	tv2, _ := s.Devices.Issue(u, "TV")
	time.Sleep(1100 * time.Millisecond)
	st, out = call("POST", "/api/me/signout-everywhere", nil, fresh, "")
	again, _ := out["token"].(string)
	if st != 200 || again == "" {
		t.Fatalf("sign out everywhere: %d %v", st, out)
	}
	if st, _ := call("GET", "/api/me/prefs", nil, tv2, ""); st != 401 {
		t.Fatalf("linked TV still works after signing out everywhere: %d", st)
	}
	if st, _ := call("GET", "/api/me/prefs", nil, again, ""); st != 200 {
		t.Fatalf("the caller was signed out too: %d", st)
	}
}

// One address can't use up every waiting TV code.
func TestPairCodesPerAddress(t *testing.T) {
	_, call, _ := signinServer(t)
	for i := 0; i < pairMaxPerIP; i++ {
		if st, _ := call("POST", "/api/pair", map[string]string{"device": "TV"}, "", "203.0.113.7"); st != 200 {
			t.Fatalf("code %d: status %d", i+1, st)
		}
	}
	if st, _ := call("POST", "/api/pair", map[string]string{"device": "TV"}, "", "203.0.113.7"); st != 429 {
		t.Fatalf("over the per-address limit: status %d", st)
	}
	if st, _ := call("POST", "/api/pair", map[string]string{"device": "TV"}, "", "203.0.113.8"); st != 200 {
		t.Fatalf("another address: status %d", st)
	}
}

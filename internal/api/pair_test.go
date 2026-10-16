package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/LTAGROUP/wrentv/internal/auth"
	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
)

func TestPairing(t *testing.T) {
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	u := &db.User{Username: "ann", DisplayName: "Ann", PasswordHash: "x", IsAdmin: true}
	if err := d.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	local := auth.NewLocal(d, "test-key")
	s := &Server{Cfg: &config.Config{}, DB: d, Local: local, Devices: auth.NewDevices(d, "test-key", local, nil)}
	s.Auth = auth.WithDevices(local, s.Devices)
	h := s.Router()

	call := func(method, path string, body any, token string) (int, map[string]any) {
		var b bytes.Buffer
		_ = json.NewEncoder(&b).Encode(body)
		req := httptest.NewRequest(method, path, &b)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	_, pair := call("POST", "/api/pair", map[string]string{"device": "Living Room"}, "")
	code, secret := pair["code"].(string), pair["secret"].(string)
	if len(code) != pairCodeLen || secret == "" {
		t.Fatalf("bad pair response %v", pair)
	}
	if st, out := call("POST", "/api/pair/"+code+"/poll", map[string]string{"secret": "wrong"}, ""); st != 404 {
		t.Fatalf("wrong secret: %d %v", st, out)
	}
	if _, out := call("POST", "/api/pair/"+code+"/poll", map[string]string{"secret": secret}, ""); out["status"] != "pending" {
		t.Fatalf("want pending, got %v", out)
	}
	if _, out := call("GET", "/api/pair/"+code[:4]+"-"+code[4:], nil, ""); out["device"] != "Living Room" {
		t.Fatalf("info: %v", out)
	}
	if st, _ := call("POST", "/api/pair/"+code+"/approve", nil, ""); st != 401 {
		t.Fatalf("approve without sign-in: %d", st)
	}
	userTok, _ := local.Issue(u, "phone")
	if st, out := call("POST", "/api/pair/"+code+"/approve", nil, userTok); st != 200 {
		t.Fatalf("approve: %d %v", st, out)
	}
	_, out := call("POST", "/api/pair/"+code+"/poll", map[string]string{"secret": secret}, "")
	tvTok, _ := out["token"].(string)
	if out["status"] != "approved" || tvTok == "" {
		t.Fatalf("want approved, got %v", out)
	}
	if st, _ := call("POST", "/api/pair/"+code+"/poll", map[string]string{"secret": secret}, ""); st != 404 {
		t.Fatalf("code should be single use, got %d", st)
	}
	if p, err := s.Auth.Authenticate(ctx, tvTok); err != nil || p.User.Username != "ann" || p.Role != "admin" {
		t.Fatalf("device token: %v %v", p, err)
	}
	if _, err := local.Authenticate(ctx, tvTok); err == nil {
		t.Fatal("device tokens must not pass as local session tokens")
	}
}

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
)

// fakeAccounts stands in for the account service's RPCs.
type fakeAccounts struct {
	t       *testing.T
	tickets map[string]string // ticket -> account id, used up when redeemed
	roles   map[string]string // account id -> role
	sawAuth bool              // any request carried a user's Authorization
}

func (f *fakeAccounts) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("Authorization") != "" {
		f.sawAuth = true
	}
	var args map[string]any
	_ = json.NewDecoder(r.Body).Decode(&args)
	reply := func(v any) (*http.Response, error) {
		b, _ := json.Marshal(v)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b))), Header: http.Header{}, Request: r}, nil
	}
	if args["p_server_id"] != "srv-1" || args["p_secret"] != "s3cret" {
		return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"message":"invalid server credentials"}`)), Request: r}, nil
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/rpc/redeem_server_ticket"):
		t, _ := args["p_ticket"].(string)
		id, ok := f.tickets[t]
		if !ok {
			return reply([]any{})
		}
		delete(f.tickets, t)
		return reply([]map[string]string{{"user_id": id, "email": id + "@example.com", "display_name": "Person " + id}})
	case strings.HasSuffix(r.URL.Path, "/rpc/server_access"):
		id, _ := args["p_user_id"].(string)
		if role := f.roles[id]; role != "" {
			return reply([]map[string]any{{"role": role, "library_ids": nil}})
		}
		return reply([]any{})
	}
	f.t.Errorf("unexpected account-service call %s", r.URL.Path)
	return reply(nil)
}

// A ticket signs its account in once, with the access the account service
// gives it; the server never needs the account's own token.
func TestRedeemTicket(t *testing.T) {
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cfg := &config.Config{}
	cfg.Auth.Supabase.ServerID, cfg.Auth.Supabase.ServerSecret = "srv-1", "s3cret"
	fake := &fakeAccounts{t: t, roles: map[string]string{"owner-1": "owner", "friend-1": "shared"},
		tickets: map[string]string{
			strings.Repeat("a", 64): "owner-1", strings.Repeat("b", 64): "friend-1", strings.Repeat("c", 64): "stranger-1",
		}}
	s := NewSupabase(d, cfg)
	s.http = &http.Client{Transport: fake}
	ctx := context.Background()

	p, err := s.Redeem(ctx, strings.Repeat("a", 64))
	if err != nil || p.Role != "owner" || p.User.ExternalID != "owner-1" || p.User.Email != "owner-1@example.com" {
		t.Fatalf("owner ticket: %+v %v", p, err)
	}
	if _, err := s.Redeem(ctx, strings.Repeat("a", 64)); !errors.Is(err, ErrBadTicket) {
		t.Fatalf("a ticket worked twice: %v", err)
	}
	if p, err := s.Redeem(ctx, strings.Repeat("b", 64)); err != nil || p.Role != "user" {
		t.Fatalf("shared ticket: %+v %v", p, err)
	}
	if _, err := s.Redeem(ctx, strings.Repeat("c", 64)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an account without access: %v", err)
	}
	for _, bad := range []string{"", "short", strings.Repeat("z", 64), strings.Repeat("x", 300)} {
		if _, err := s.Redeem(ctx, bad); !errors.Is(err, ErrBadTicket) {
			t.Fatalf("ticket %q: %v", bad, err)
		}
	}
	if fake.sawAuth {
		t.Fatal("the server sent a user's token to the account service")
	}

	// The token the server issues for the ticket's user works, and stops
	// with the account's access.
	local := NewLocal(d, "k")
	dev := NewDevices(d, "k", local, s)
	tok, _ := dev.Issue(p.User, "Phone")
	auth := WithDevices(Either(local, s), dev)
	if got, err := auth.Authenticate(ctx, tok); err != nil || got.Role != "owner" {
		t.Fatalf("server token: %+v %v", got, err)
	}
	delete(fake.roles, "owner-1")
	s.mu.Lock()
	s.access = map[string]cachedAccess{}
	s.mu.Unlock()
	if _, err := auth.Authenticate(ctx, tok); !errors.Is(err, ErrForbidden) {
		t.Fatalf("server token after access ended: %v", err)
	}
}

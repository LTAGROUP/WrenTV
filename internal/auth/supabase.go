package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/time/rate"

	"github.com/LTAGROUP/wrentv/internal/config"
	"github.com/LTAGROUP/wrentv/internal/db"
)

// Supabase authenticates Wren TV account users.
type Supabase struct {
	db   *db.DB
	cfg  *config.Config
	http *http.Client

	jwksOnce sync.Once
	jwks     keyfunc.Keyfunc

	mu     sync.Mutex
	tokens map[string]cachedUser   // token -> verified user (for HS256 projects)
	access map[string]cachedAccess // supabase user id -> access
	// Tokens the account service turned down lately (by hash), and a cap on
	// how often it's asked: made-up tokens can't make this server flood it.
	rejected map[[32]byte]time.Time
	asks     *rate.Limiter
}

type cachedUser struct {
	id, email, name string
	exp             time.Time
}

type cachedAccess struct {
	role    string
	libs    []string
	fetched time.Time
}

func NewSupabase(d *db.DB, cfg *config.Config) *Supabase {
	return &Supabase{db: d, cfg: cfg, http: &http.Client{Timeout: 15 * time.Second},
		tokens: map[string]cachedUser{}, access: map[string]cachedAccess{},
		rejected: map[[32]byte]time.Time{}, asks: rate.NewLimiter(2, 20)}
}

func (s *Supabase) Mode() string { return "supabase" }

func (s *Supabase) base() string { return config.SupabaseURL }

func (s *Supabase) Claimed() bool { return s.cfg.Snapshot().Auth.Supabase.ServerID != "" }

// verify returns the Supabase user behind an access token.
func (s *Supabase) verify(ctx context.Context, token string) (id, email, name string, err error) {
	// Asymmetric signing keys (default for new projects): verify locally via JWKS.
	s.jwksOnce.Do(func() {
		kf, kerr := keyfunc.NewDefaultCtx(context.Background(), []string{s.base() + "/auth/v1/.well-known/jwks.json"})
		if kerr == nil {
			s.jwks = kf
		}
	})
	if s.jwks != nil {
		var c struct {
			jwt.RegisteredClaims
			Email        string         `json:"email"`
			UserMetadata map[string]any `json:"user_metadata"`
			Role         string         `json:"role"`
		}
		tok, perr := jwt.ParseWithClaims(token, &c, s.jwks.Keyfunc, jwt.WithValidMethods([]string{"ES256", "RS256", "EdDSA"}))
		if perr == nil && tok.Valid && c.Role == "authenticated" {
			n, _ := c.UserMetadata["display_name"].(string)
			return c.Subject, c.Email, n, nil
		}
		if perr != nil && !errors.Is(perr, jwt.ErrTokenUnverifiable) && !errors.Is(perr, jwt.ErrTokenSignatureInvalid) &&
			!strings.Contains(perr.Error(), "kid") {
			return "", "", "", ErrUnauthorized
		}
	}
	// Legacy HS256 projects: ask Supabase who this is (cached).
	if strings.Count(token, ".") != 2 || len(token) > 8192 {
		return "", "", "", ErrUnauthorized // not a JWT: nothing to ask about
	}
	sum := sha256.Sum256([]byte(token))
	s.mu.Lock()
	if cu, ok := s.tokens[token]; ok && time.Now().Before(cu.exp) {
		s.mu.Unlock()
		return cu.id, cu.email, cu.name, nil
	}
	if until, ok := s.rejected[sum]; ok && time.Now().Before(until) {
		s.mu.Unlock()
		return "", "", "", ErrUnauthorized
	}
	s.mu.Unlock()
	if !s.asks.Allow() {
		return "", "", "", ErrBusy
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.base()+"/auth/v1/user", nil)
	req.Header.Set("apikey", config.SupabasePublishableKey)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.http.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		s.mu.Lock()
		if len(s.rejected) > 5000 {
			s.rejected = map[[32]byte]time.Time{}
		}
		s.rejected[sum] = time.Now().Add(time.Minute)
		s.mu.Unlock()
		return "", "", "", ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", "", ErrUnauthorized
	}
	var u struct {
		ID           string         `json:"id"`
		Email        string         `json:"email"`
		UserMetadata map[string]any `json:"user_metadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return "", "", "", err
	}
	n, _ := u.UserMetadata["display_name"].(string)
	s.mu.Lock()
	if len(s.tokens) > 1000 {
		s.tokens = map[string]cachedUser{}
	}
	s.tokens[token] = cachedUser{id: u.ID, email: u.Email, name: n, exp: time.Now().Add(5 * time.Minute)}
	s.mu.Unlock()
	return u.ID, u.Email, n, nil
}

// Identify verifies the token and returns (creating if needed) the local user
// row, without checking server access. Used by the claim flow.
func (s *Supabase) Identify(ctx context.Context, token string) (*db.User, error) {
	id, email, name, err := s.verify(ctx, token)
	if err != nil {
		return nil, err
	}
	return s.ensureUser(ctx, id, email, name)
}

// ensureUser is the local row for an account, created or brought up to date.
func (s *Supabase) ensureUser(ctx context.Context, id, email, name string) (*db.User, error) {
	u, err := s.db.UserByExternalID(ctx, id)
	if errors.Is(err, db.ErrNotFound) {
		if name == "" {
			name, _, _ = strings.Cut(email, "@")
		}
		u = &db.User{ExternalID: id, Email: email, DisplayName: name}
		if err := s.db.CreateUser(ctx, u); err != nil {
			return nil, err
		}
		return u, nil
	}
	if err == nil && (u.Email != email || (name != "" && u.DisplayName != name)) {
		if name == "" {
			name = u.DisplayName
		}
		_ = s.db.UpdateUserProfile(ctx, u.ID, name, email, u.IsAdmin)
		u.Email, u.DisplayName = email, name
	}
	return u, err
}

// ErrBadTicket: the ticket is unknown, used, expired, or for another server.
var ErrBadTicket = errors.New("this sign-in ticket isn't valid")

// Redeem turns a server ticket (which an app got from the account service
// for this server only) into the user it was issued to and what they may
// do here. The app never shows this server its account token.
func (s *Supabase) Redeem(ctx context.Context, ticket string) (*Principal, error) {
	if !s.Claimed() {
		return nil, ErrUnclaimed
	}
	if len(ticket) < 32 || len(ticket) > 256 {
		return nil, ErrBadTicket
	}
	sb := s.cfg.Snapshot().Auth.Supabase
	var rows []struct {
		UserID      string `json:"user_id"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
	}
	if err := s.rpc(ctx, "", "redeem_server_ticket", map[string]any{
		"p_server_id": sb.ServerID, "p_secret": sb.ServerSecret, "p_ticket": ticket,
	}, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 || rows[0].UserID == "" {
		return nil, ErrBadTicket
	}
	u, err := s.ensureUser(ctx, rows[0].UserID, rows[0].Email, rows[0].DisplayName)
	if err != nil {
		return nil, err
	}
	return s.principal(ctx, u)
}

func (s *Supabase) Authenticate(ctx context.Context, token string) (*Principal, error) {
	u, err := s.Identify(ctx, token)
	if errors.Is(err, ErrBusy) {
		return nil, ErrBusy
	}
	if err != nil {
		return nil, ErrUnauthorized
	}
	return s.principal(ctx, u)
}

// principal asks the account service what an identified user may do here.
func (s *Supabase) principal(ctx context.Context, u *db.User) (*Principal, error) {
	if !s.Claimed() {
		return nil, ErrUnclaimed
	}
	role, libs, err := s.accessFor(ctx, u.ExternalID)
	if err != nil {
		return nil, err
	}
	if role == "" {
		return nil, ErrForbidden
	}
	p := &Principal{User: u, Role: role, LibraryIDs: libs}
	if role == "owner" {
		p.LibraryIDs = nil
		if !u.IsAdmin {
			_ = s.db.UpdateUserProfile(ctx, u.ID, u.DisplayName, u.Email, true)
		}
	}
	return p, nil
}

// accessFor asks Supabase what this user may do on this server (cached 60s,
// with stale results used if Supabase is unreachable).
func (s *Supabase) accessFor(ctx context.Context, userID string) (string, []string, error) {
	s.mu.Lock()
	ca, ok := s.access[userID]
	s.mu.Unlock()
	if ok && time.Since(ca.fetched) < 60*time.Second {
		return ca.role, ca.libs, nil
	}
	var rows []struct {
		Role       string   `json:"role"`
		LibraryIDs []string `json:"library_ids"`
	}
	sb := s.cfg.Snapshot().Auth.Supabase
	err := s.rpc(ctx, "", "server_access", map[string]any{
		"p_server_id": sb.ServerID, "p_secret": sb.ServerSecret, "p_user_id": userID,
	}, &rows)
	if err != nil {
		if ok {
			slog.Warn("supabase unreachable, using cached access", "err", err)
			return ca.role, ca.libs, nil
		}
		return "", nil, err
	}
	role, libs := "", []string(nil)
	if len(rows) > 0 {
		role, libs = rows[0].Role, rows[0].LibraryIDs
		if role == "shared" {
			role = "user"
			if libs == nil {
				libs = nil // null means every library
			}
		}
	}
	s.mu.Lock()
	s.access[userID] = cachedAccess{role: role, libs: libs, fetched: time.Now()}
	s.mu.Unlock()
	return role, libs, nil
}

// NewClaimCode returns a short human-friendly one-time code like "K7QX-3MPA".
func NewClaimCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	out := make([]byte, 0, 9)
	for i, c := range b {
		if i == 4 {
			out = append(out, '-')
		}
		out = append(out, alphabet[int(c)%len(alphabet)])
	}
	return string(out)
}

// ClaimCodeOK compares a user-entered code, ignoring case, spaces and dashes.
func (s *Supabase) ClaimCodeOK(code string) bool {
	norm := func(v string) string {
		return strings.NewReplacer("-", "", " ", "").Replace(strings.ToUpper(strings.TrimSpace(v)))
	}
	want := norm(s.cfg.Snapshot().Auth.Supabase.ClaimCode)
	return want != "" && subtle.ConstantTimeCompare([]byte(norm(code)), []byte(want)) == 1
}

// Claim registers this server to the signed-in user's account.
func (s *Supabase) Claim(ctx context.Context, userToken string) error {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	sec := hex.EncodeToString(secret)
	var serverID string
	if err := s.rpc(ctx, userToken, "claim_server", map[string]any{
		"p_machine_id": s.cfg.MachineID, "p_name": s.cfg.Snapshot().ServerName, "p_secret": sec,
	}, &serverID); err != nil {
		return err
	}
	err := s.cfg.Update(func(c *config.Config) error {
		c.Auth.Supabase.ServerID = serverID
		c.Auth.Supabase.ServerSecret = sec
		c.Auth.Supabase.ClaimCode = ""
		return nil
	})
	s.mu.Lock()
	s.access = map[string]cachedAccess{}
	s.mu.Unlock()
	return err
}

// Heartbeat publishes reachability and the library list (so owners can
// manage sharing from any device).
func (s *Supabase) Heartbeat(ctx context.Context, urls []string, remoteURL, version string, libs []*db.Library) error {
	if !s.Claimed() {
		return nil
	}
	type lib struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	}
	ls := []lib{}
	for _, l := range libs {
		ls = append(ls, lib{l.ID, l.Name, l.Type})
	}
	c := s.cfg.Snapshot()
	return s.rpc(ctx, "", "server_heartbeat", map[string]any{
		"p_server_id": c.Auth.Supabase.ServerID, "p_secret": c.Auth.Supabase.ServerSecret,
		"p_name": c.ServerName, "p_local_urls": urls, "p_public_url": c.PublicURL, "p_version": version, "p_libraries": ls,
		"p_remote_url": remoteURL,
	}, nil)
}

func (s *Supabase) rpc(ctx context.Context, userToken, fn string, args any, out any) error {
	body, _ := json.Marshal(args)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.base()+"/rest/v1/rpc/"+fn, bytes.NewReader(body))
	req.Header.Set("apikey", config.SupabasePublishableKey)
	req.Header.Set("Content-Type", "application/json")
	if userToken != "" {
		req.Header.Set("Authorization", "Bearer "+userToken)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(b, &e)
		if e.Message == "" {
			e.Message = string(b)
		}
		return fmt.Errorf("%s: %s", fn, e.Message)
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

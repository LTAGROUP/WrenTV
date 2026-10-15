// Package auth authenticates API requests.
//
// Two modes:
//   - local: accounts live in this server's database (username/password).
//   - supabase: Wren TV accounts. Users sign in once (any device), link other
//     devices with a short code, and server owners share libraries with other
//     accounts. The server verifies Supabase-issued JWTs and asks Supabase
//     (with its own server secret) what each user may access.
package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/LTAGROUP/wrentv/internal/db"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrUnclaimed    = errors.New("server has not been claimed")
)

// Principal is an authenticated user and what they can reach on this server.
type Principal struct {
	User *db.User
	// Role: "owner", "admin" or "user".
	Role string
	// LibraryIDs the user may access; nil means all libraries.
	LibraryIDs []string
}

func (p *Principal) IsAdmin() bool { return p.Role == "owner" || p.Role == "admin" }

// CanAccess reports whether the principal may see a library.
func (p *Principal) CanAccess(libraryID string) bool {
	if p.LibraryIDs == nil {
		return true
	}
	for _, l := range p.LibraryIDs {
		if l == libraryID {
			return true
		}
	}
	return false
}

// Visible filters a full library list to what the principal may see.
func (p *Principal) Visible(all []*db.Library) []string {
	out := []string{}
	for _, l := range all {
		if p.CanAccess(l.ID) {
			out = append(out, l.ID)
		}
	}
	return out
}

type Provider interface {
	Mode() string
	Authenticate(ctx context.Context, token string) (*Principal, error)
}

// BearerToken extracts a token from the Authorization header or ?token=.
func BearerToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if t := r.Header.Get("X-WrenTV-Token"); t != "" {
		return t
	}
	if t := r.Header.Get("X-Haven-Token"); t != "" { // legacy clients
		return t
	}
	return r.URL.Query().Get("token")
}

// ---------- local mode ----------

type Local struct {
	db  *db.DB
	key []byte
}

func NewLocal(d *db.DB, signingKey string) *Local {
	return &Local{db: d, key: []byte(signingKey)}
}

func (l *Local) Mode() string { return "local" }

// Tokens issued before the rename carry the Haven issuer and stay valid.
const (
	issuer       = "wrentv-local"
	legacyIssuer = "haven-local"
)

type claims struct {
	jwt.RegisteredClaims
	Device string `json:"dev,omitempty"`
}

// Issue creates a long-lived token for a user on a device.
func (l *Local) Issue(u *db.User, device string) (string, error) {
	c := claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   u.ID,
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(365 * 24 * time.Hour)),
		},
		Device: device,
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(l.key)
}

func (l *Local) Authenticate(ctx context.Context, token string) (*Principal, error) {
	var c claims
	_, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) { return l.key, nil },
		jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || (c.Issuer != issuer && c.Issuer != legacyIssuer) {
		return nil, ErrUnauthorized
	}
	u, err := l.db.UserByID(ctx, c.Subject)
	if err != nil {
		return nil, ErrUnauthorized
	}
	return l.principal(ctx, u)
}

func (l *Local) principal(ctx context.Context, u *db.User) (*Principal, error) {
	p := &Principal{User: u, Role: "user"}
	if u.IsAdmin {
		p.Role = "admin"
		return p, nil
	}
	libs, err := l.db.LibraryAccess(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	if libs == nil {
		libs = []string{}
	}
	p.LibraryIDs = libs
	return p, nil
}

// Login checks a username/password.
func (l *Local) Login(ctx context.Context, username, password string) (*db.User, error) {
	u, err := l.db.UserByUsername(ctx, username)
	if err != nil {
		// Spend similar time to avoid user enumeration by timing.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$7EqJtq98hPqEX7fNZaFWoO5f1H6Z1lYz6Hc5e1Xn8Q6pC6v1u0b7W"), []byte(password))
		return nil, ErrUnauthorized
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, ErrUnauthorized
	}
	return u, nil
}

func HashPassword(pw string) (string, error) {
	if len(pw) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

// IntegrationKeyOK compares a presented key in constant time.
func IntegrationKeyOK(presented, expected string) bool {
	if presented == "" || expected == "" || len(presented) != len(expected) {
		return false
	}
	var v byte
	for i := 0; i < len(presented); i++ {
		v |= presented[i] ^ expected[i]
	}
	return v == 0
}

package auth

import (
	"context"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/LTAGROUP/wrentv/internal/db"
)

const deviceIssuer = "wrentv-device"

// Devices signs in devices that were linked from another signed-in device:
// a TV shows a code (or QR code), and someone approves it from their phone or
// browser instead of typing a password with a remote. The tokens are signed
// by this server and work in both modes; with Wren TV accounts the user's
// access is still checked with the account service on every request, so
// unsharing the server locks the device out too.
type Devices struct {
	db    *db.DB
	key   []byte
	local *Local
	supa  *Supabase
}

func NewDevices(d *db.DB, signingKey string, local *Local, supa *Supabase) *Devices {
	return &Devices{db: d, key: []byte(signingKey), local: local, supa: supa}
}

// Issue creates a long-lived token for a user on a linked device.
func (d *Devices) Issue(u *db.User, device string) (string, error) {
	c := claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   u.ID,
			Issuer:    deviceIssuer,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(365 * 24 * time.Hour)),
		},
		Device: device,
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(d.key)
}

// parse returns the user id of a valid device token, or "".
func (d *Devices) parse(token string) string {
	var c claims
	_, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) { return d.key, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(deviceIssuer))
	if err != nil {
		return ""
	}
	return c.Subject
}

// WithDevices accepts device tokens in front of another provider.
func WithDevices(next Provider, d *Devices) Provider { return &withDevices{next, d} }

type withDevices struct {
	next Provider
	d    *Devices
}

func (w *withDevices) Mode() string { return w.next.Mode() }

func (w *withDevices) Authenticate(ctx context.Context, token string) (*Principal, error) {
	id := w.d.parse(token)
	if id == "" {
		return w.next.Authenticate(ctx, token)
	}
	u, err := w.d.db.UserByID(ctx, id)
	if err != nil {
		return nil, ErrUnauthorized
	}
	if w.d.supa != nil && u.ExternalID != "" {
		return w.d.supa.principal(ctx, u)
	}
	if w.d.local == nil || u.PasswordHash == "" {
		return nil, ErrUnauthorized
	}
	return w.d.local.principal(ctx, u)
}

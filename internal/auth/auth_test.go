package auth

import (
	"context"
	"testing"

	"github.com/LTAGROUP/wrentv/internal/db"
)

type fakeProvider struct{ called bool }

func (f *fakeProvider) Mode() string { return "supabase" }

func (f *fakeProvider) Authenticate(ctx context.Context, token string) (*Principal, error) {
	f.called = true
	return &Principal{Role: "owner"}, nil
}

// Local accounts keep working on servers that use Wren TV accounts.
func TestEitherAcceptsLocalAndAccountTokens(t *testing.T) {
	d, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hash, _ := HashPassword("correct horse")
	u := &db.User{Username: "guest", DisplayName: "Guest", PasswordHash: hash}
	if err := d.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	local := NewLocal(d, "test-key")
	next := &fakeProvider{}
	p := Either(local, next)
	if p.Mode() != "supabase" {
		t.Fatalf("mode = %q", p.Mode())
	}

	if _, err := local.Login(ctx, "guest", "correct horse"); err != nil {
		t.Fatal(err)
	}
	tok, _ := local.Issue(u, "test")
	got, err := p.Authenticate(ctx, tok)
	if err != nil || got.User.ID != u.ID || got.Role != "user" || next.called {
		t.Fatalf("local token: %+v %v (next called %v)", got, err, next.called)
	}

	got, err = p.Authenticate(ctx, "an-account-token")
	if err != nil || got.Role != "owner" || !next.called {
		t.Fatalf("account token: %+v %v", got, err)
	}
}

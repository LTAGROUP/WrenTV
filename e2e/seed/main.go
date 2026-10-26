// Command seed creates or resets one local account in a Wren TV data directory.
// It exists for the development deployment gate, which needs an account that can
// sign in through the real UI; normal installs create their first user through
// the app. Development only: it is never built into the server image.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/LTAGROUP/wrentv/internal/auth"
	"github.com/LTAGROUP/wrentv/internal/db"
)

func main() {
	dataDir := flag.String("data", "", "data directory of the deployment")
	username := flag.String("user", "gate", "username to create or reset")
	password := flag.String("password", "", "password (8+ characters)")
	admin := flag.Bool("admin", true, "grant administrator rights")
	flag.Parse()

	if strings.TrimSpace(*dataDir) == "" || len(*password) < 8 {
		fmt.Fprintln(os.Stderr, "usage: seed -data <dir> -user <name> -password <8+ chars> [-admin]")
		os.Exit(2)
	}
	d, err := db.Open(*dataDir)
	if err != nil {
		fail("open database", err)
	}
	defer d.Close()

	ctx := context.Background()
	if existing, err := d.UserByUsername(ctx, *username); err == nil && existing != nil {
		// A re-run must not keep the old password or role.
		if err := d.DeleteUser(ctx, existing.ID); err != nil {
			fail("remove previous account", err)
		}
	} else if err != nil && !errors.Is(err, db.ErrNotFound) {
		fail("look up account", err)
	}

	hash, err := auth.HashPassword(*password)
	if err != nil {
		fail("hash password", err)
	}
	user := &db.User{Username: *username, DisplayName: *username, PasswordHash: hash, IsAdmin: *admin}
	if err := d.CreateUser(ctx, user); err != nil {
		fail("create account", err)
	}
	fmt.Printf("seeded %s (admin=%v) in %s\n", *username, *admin, *dataDir)
}

func fail(what string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
	os.Exit(1)
}

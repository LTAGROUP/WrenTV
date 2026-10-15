package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGetenvFallsBackToHaven(t *testing.T) {
	t.Setenv("HAVEN_LISTEN", ":1")
	if got := Getenv("LISTEN"); got != ":1" {
		t.Fatalf("legacy only: got %q", got)
	}
	t.Setenv("WRENTV_LISTEN", ":2")
	if got := Getenv("LISTEN"); got != ":2" {
		t.Fatalf("both set: got %q, want WRENTV_ to win", got)
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMigrateRenamesFilesInPlace(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "haven.yaml"), "server_name: x\n")
	write(t, filepath.Join(dir, "haven.db"), "db")
	write(t, filepath.Join(dir, "haven.db-wal"), "wal")
	got, notes, err := MigrateDataDir(dir)
	if err != nil || got != dir {
		t.Fatalf("dir=%q err=%v", got, err)
	}
	if len(notes) != 3 {
		t.Fatalf("notes = %v", notes)
	}
	if read(t, filepath.Join(dir, "wrentv.yaml")) != "server_name: x\n" || read(t, filepath.Join(dir, "wrentv.db")) != "db" ||
		read(t, filepath.Join(dir, "wrentv.db-wal")) != "wal" {
		t.Fatal("files not renamed")
	}
	// Second run is a no-op.
	if _, notes, _ := MigrateDataDir(dir); len(notes) != 0 {
		t.Fatalf("rerun notes = %v", notes)
	}
}

func TestMigrateKeepsExistingNewDB(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "wrentv.db"), "new")
	write(t, filepath.Join(dir, "haven.db"), "old")
	write(t, filepath.Join(dir, "haven.db-wal"), "oldwal")
	if _, _, err := MigrateDataDir(dir); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dir, "wrentv.db")) != "new" {
		t.Fatal("new db overwritten")
	}
	if _, err := os.Stat(filepath.Join(dir, "wrentv.db-wal")); err == nil {
		t.Fatal("old WAL moved next to the new db")
	}
}

func TestMigrateMovesLegacyDefaultDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("uses the per-user default dirs")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Unsetenv("WRENTV_DATA_DIR")
	os.Unsetenv("HAVEN_DATA_DIR")
	old := legacyDataDir()
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(old, "haven.yaml"), "server_name: x\n")
	write(t, filepath.Join(old, "haven.db"), "db")

	newDir := DefaultDataDir()
	got, _, err := MigrateDataDir(newDir)
	if err != nil || got != newDir {
		t.Fatalf("dir=%q err=%v", got, err)
	}
	if read(t, filepath.Join(newDir, "wrentv.db")) != "db" {
		t.Fatal("db not moved")
	}
	if fi, err := os.Lstat(old); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("no symlink left at the old path")
	}
	// Loading through the symlinked old path still finds the config.
	c, err := Load(newDir)
	if err != nil || c.ServerName != "x" {
		t.Fatalf("load: %v %+v", err, c)
	}
}

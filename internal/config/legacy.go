package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// Wren TV was called Haven. Installs from that era keep working: HAVEN_*
// environment variables are still read, and the old data directory and file
// names are adopted on first start.

const (
	configFile       = "wrentv.yaml"
	legacyConfigFile = "haven.yaml"
	dbFile           = "wrentv.db"
	legacyDBFile     = "haven.db"
)

// Getenv returns WRENTV_<name>, falling back to the legacy HAVEN_<name>.
func Getenv(name string) string {
	v, _ := LookupEnv(name)
	return v
}

// LookupEnv is Getenv that also reports whether either variable is set.
func LookupEnv(name string) (string, bool) {
	if v, ok := os.LookupEnv("WRENTV_" + name); ok {
		return v, true
	}
	return os.LookupEnv("HAVEN_" + name)
}

// legacyDataDir is where Haven kept its data by default.
func legacyDataDir() string {
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "Haven")
	case "windows":
		if pd := os.Getenv("ProgramData"); pd != "" {
			return filepath.Join(pd, "Haven")
		}
		return `C:\ProgramData\Haven`
	default:
		if os.Geteuid() == 0 {
			return "/var/lib/haven"
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "share", "haven")
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func hasData(dir string) bool {
	for _, f := range []string{configFile, legacyConfigFile, dbFile, legacyDBFile} {
		if exists(filepath.Join(dir, f)) {
			return true
		}
	}
	return false
}

func isEmptyDir(dir string) bool {
	ents, err := os.ReadDir(dir)
	return err == nil && len(ents) == 0
}

// MigrateDataDir adopts a Haven-era install. If dir is the default data
// directory and holds no data while the old default does, the old directory
// is moved into place (and a symlink left behind); if the move fails, the old
// directory is used as is. Inside the chosen directory, haven.yaml and
// haven.db (with its WAL files) are renamed. It returns the directory to use
// and a description of each change made. Call it before the server opens the
// database.
func MigrateDataDir(dir string) (string, []string, error) {
	var notes []string
	if filepath.Clean(dir) == filepath.Clean(DefaultDataDir()) && !hasData(dir) {
		old := legacyDataDir()
		if old != dir && hasData(old) {
			if isEmptyDir(dir) {
				_ = os.Remove(dir)
			}
			if exists(dir) {
				notes = append(notes, "using legacy data dir "+old+" ("+dir+" is not empty)")
				dir = old
			} else if err := os.MkdirAll(filepath.Dir(dir), 0o755); err == nil && os.Rename(old, dir) == nil {
				notes = append(notes, "moved data dir "+old+" -> "+dir)
				if runtime.GOOS != "windows" && os.Symlink(dir, old) == nil {
					notes = append(notes, "left symlink "+old+" -> "+dir)
				}
			} else {
				notes = append(notes, "using legacy data dir "+old+" (could not move it)")
				dir = old
			}
		}
	}
	renames := [][2]string{
		{legacyConfigFile, configFile},
		{legacyDBFile, dbFile},
		{legacyDBFile + "-wal", dbFile + "-wal"},
		{legacyDBFile + "-shm", dbFile + "-shm"},
	}
	// Only move the database when the new one doesn't exist yet, and move its
	// WAL files with it so no committed data is left behind.
	moveDB := exists(filepath.Join(dir, legacyDBFile)) && !exists(filepath.Join(dir, dbFile))
	for i, r := range renames {
		from, to := filepath.Join(dir, r[0]), filepath.Join(dir, r[1])
		if i > 0 && !moveDB {
			break
		}
		if !exists(from) || exists(to) {
			continue
		}
		if err := os.Rename(from, to); err != nil {
			return dir, notes, errors.Join(errors.New("rename "+from), err)
		}
		notes = append(notes, "renamed "+from+" -> "+to)
	}
	return dir, notes, nil
}

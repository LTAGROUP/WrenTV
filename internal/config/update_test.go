package config

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

// A failed update changes nothing; a good one is applied whole and saved.
func TestUpdateIsAllOrNothing(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = c.Update(func(n *Config) error {
		n.ServerName = "half"
		n.Streaming.MaxSizeGB["1080p"] = 9
		return errors.New("invalid")
	})
	if err == nil {
		t.Fatal("want the update's error")
	}
	if s := c.Snapshot(); s.ServerName == "half" || s.Streaming.MaxSizeGB["1080p"] == 9 {
		t.Fatalf("a failed update leaked: %q %v", s.ServerName, s.Streaming.MaxSizeGB)
	}
	if err := c.Update(func(n *Config) error {
		n.ServerName = "Den"
		n.Streaming.MaxSizeGB["1080p"] = 9
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	again, err := Load(c.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if again.ServerName != "Den" || again.Streaming.MaxSizeGB["1080p"] != 9 || again.MachineID != c.MachineID {
		t.Fatalf("not saved: %q %v", again.ServerName, again.Streaming.MaxSizeGB)
	}
}

// Readers and writers at once (run with -race).
func TestUpdateWhileReading(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				s := c.Snapshot()
				_ = s.Streaming.MaxSizeGB["1080p"]
				_ = len(s.Streaming.Addons) + len(s.ServerName)
			}
		}()
	}
	for i := 0; i < 50; i++ {
		if err := c.Update(func(n *Config) error {
			n.Streaming.MaxSizeGB["1080p"] = float64(i)
			n.ServerName = strings.Repeat("x", i%7+1)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}

// A setting an environment variable overrides is used, but never written
// into the file; one changed on the settings page since is.
func TestEnvOverridesStayOutOfTheFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WRENTV_TMDB_API_KEY", "from-env")
	t.Setenv("WRENTV_SERVER_NAME", "Env Name")
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Metadata.TMDBAPIKey != "from-env" {
		t.Fatalf("env not applied: %q", c.Metadata.TMDBAPIKey)
	}
	if err := c.Update(func(n *Config) error { n.ServerName = "Chosen"; return nil }); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.Path())
	if strings.Contains(string(b), "from-env") {
		t.Fatal("the environment's TMDB key was written to the file")
	}
	if !strings.Contains(string(b), "Chosen") {
		t.Fatal("a setting changed on purpose wasn't saved")
	}
	if c.Snapshot().Metadata.TMDBAPIKey != "from-env" {
		t.Fatal("saving dropped the environment's value from the running config")
	}
}

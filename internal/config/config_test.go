package config

import "testing"

func TestTorrentPortForwardingOptIn(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.Torrents.PortForwarding {
		t.Fatal("torrent port forwarding should be off by default")
	}

	t.Setenv("WRENTV_TORRENT_PORT_FORWARDING", "true")
	if c, err = Load(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if !c.Torrents.PortForwarding {
		t.Fatal("WRENTV_TORRENT_PORT_FORWARDING=true should enable it")
	}
}

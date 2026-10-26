package db

import (
	"context"
	"errors"
	"testing"
)

func TestSyncSportsTeamsChangeDetection(t *testing.T) {
	ctx := context.Background()
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	teams := []SportsTeamRow{
		{ID: "nba:2", League: "nba", ProviderID: "2", Name: "Boston Celtics", Location: "Boston", Nickname: "Celtics", Abbreviation: "BOS", LogoURL: "https://a.espncdn.com/bos.png"},
		{ID: "nba:14", League: "nba", ProviderID: "14", Name: "Miami Heat", Location: "Miami", Nickname: "Heat", Abbreviation: "MIA"},
	}
	changed, err := d.SyncSportsTeams(ctx, teams)
	if err != nil || changed != 2 {
		t.Fatalf("first sync: changed=%d err=%v", changed, err)
	}
	changed, err = d.SyncSportsTeams(ctx, teams)
	if err != nil || changed != 0 {
		t.Fatalf("unchanged sync rewrote rows: changed=%d err=%v", changed, err)
	}
	teams[0].Name = "Boston Celtics FC"
	changed, err = d.SyncSportsTeams(ctx, teams)
	if err != nil || changed != 1 {
		t.Fatalf("rename not detected: changed=%d err=%v", changed, err)
	}
	rows, err := d.ListSportsTeams(ctx, "nba")
	if err != nil || len(rows) != 2 {
		t.Fatalf("list: %d %v", len(rows), err)
	}
	if rows[0].ID != "nba:2" || rows[0].Name != "Boston Celtics FC" || rows[1].ID != "nba:14" {
		t.Fatalf("identity or order wrong: %+v", rows)
	}
	if got, err := d.ListSportsTeams(ctx, "nfl"); err != nil || len(got) != 0 {
		t.Fatalf("league filter: %d %v", len(got), err)
	}
}

func TestSportsPreferenceRevisioningAndIsolation(t *testing.T) {
	ctx := context.Background()
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, user := range []*User{{ID: "u-a", DisplayName: "A"}, {ID: "u-b", DisplayName: "B"}} {
		if err := d.CreateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	revision, raw, err := d.GetSportsPreferences(ctx, "u-a")
	if err != nil || revision != 0 || len(raw) != 0 {
		t.Fatalf("empty: rev=%d raw=%q err=%v", revision, raw, err)
	}
	first := []byte(`{"version":1,"pinnedSportIds":["basketball"],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":[]}`)
	revision, err = d.SetSportsPreferences(ctx, "u-a", 0, first)
	if err != nil || revision != 1 {
		t.Fatalf("first save: rev=%d err=%v", revision, err)
	}
	if _, err := d.SetSportsPreferences(ctx, "u-a", 0, first); !errors.Is(err, ErrSportsPreferenceConflict) {
		t.Fatalf("stale revision accepted: %v", err)
	}
	// Another user starts at revision zero and cannot see the first document.
	revisionB, rawB, err := d.GetSportsPreferences(ctx, "u-b")
	if err != nil || revisionB != 0 || len(rawB) != 0 {
		t.Fatalf("isolation: rev=%d raw=%q err=%v", revisionB, rawB, err)
	}
	if _, err := d.SetSportsPreferences(ctx, "u-b", 0, first); err != nil {
		t.Fatalf("second user save: %v", err)
	}
	revision, stored, err := d.GetSportsPreferences(ctx, "u-a")
	if err != nil || revision != 1 || string(stored) != string(first) {
		t.Fatalf("stored document changed: rev=%d raw=%q err=%v", revision, stored, err)
	}
	second := []byte(`{"version":1,"pinnedSportIds":["soccer"],"competitionIds":[],"teamIds":[],"athleteIds":[],"schools":[]}`)
	revision, err = d.SetSportsPreferences(ctx, "u-a", 1, second)
	if err != nil || revision != 2 {
		t.Fatalf("second save: rev=%d err=%v", revision, err)
	}
	_, raw, _ = d.GetSportsPreferences(ctx, "u-a")
	if string(raw) != string(second) {
		t.Fatalf("second document not stored: %q", raw)
	}
}

func seedLinkTeams(t *testing.T, d *DB) {
	t.Helper()
	if _, err := d.SyncSportsTeams(context.Background(), []SportsTeamRow{
		{ID: "nba:2", League: "nba", ProviderID: "2", Name: "Boston Celtics"},
		{ID: "nba:14", League: "nba", ProviderID: "14", Name: "Miami Heat"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSportsLinkCRUDAndOrdering(t *testing.T) {
	ctx := context.Background()
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	seedLinkTeams(t, d)
	primary, err := d.SetSportsPrimaryLink(ctx, "nba:2", SportsLinkRow{ChannelRef: "c1", ChannelTvgID: "celtics.us", ChannelName: "Celtics"})
	if err != nil || primary.Slot != "primary" || primary.Position != 0 || primary.AutoLinked {
		t.Fatalf("primary: %+v %v", primary, err)
	}
	// Replacing the primary keeps a single primary row.
	primary, err = d.SetSportsPrimaryLink(ctx, "nba:2", SportsLinkRow{ChannelRef: "c2", ChannelTvgID: "boston.us", ChannelName: "Boston Celtics"})
	if err != nil || primary.ChannelRef != "c2" {
		t.Fatalf("primary replacement: %+v %v", primary, err)
	}
	b1, err := d.AddSportsBackupLink(ctx, "nba:2", SportsLinkRow{ChannelRef: "b1", ChannelName: "Backup One"})
	if err != nil || b1.Position != 1 {
		t.Fatalf("backup one: %+v %v", b1, err)
	}
	b2, err := d.AddSportsBackupLink(ctx, "nba:2", SportsLinkRow{ChannelRef: "b2", ChannelName: "Backup Two"})
	if err != nil || b2.Position != 2 {
		t.Fatalf("backup two: %+v %v", b2, err)
	}
	links, err := d.SportsTeamLinks(ctx, "nba:2")
	if err != nil || len(links) != 3 || links[0].Slot != "primary" || links[1].ID != b1.ID || links[2].ID != b2.ID {
		t.Fatalf("links: %+v %v", links, err)
	}
	if err := d.ReorderSportsBackups(ctx, "nba:2", []string{b2.ID, b1.ID}); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	links, _ = d.SportsTeamLinks(ctx, "nba:2")
	if links[1].ID != b2.ID || links[1].Position != 1 || links[2].ID != b1.ID || links[2].Position != 2 {
		t.Fatalf("reordered links: %+v", links)
	}
	if err := d.ReorderSportsBackups(ctx, "nba:2", []string{b1.ID}); !errors.Is(err, ErrSportsLinkOrder) {
		t.Fatalf("partial order accepted: %v", err)
	}
	if err := d.ReorderSportsBackups(ctx, "nba:2", []string{b1.ID, b1.ID}); !errors.Is(err, ErrSportsLinkOrder) {
		t.Fatalf("duplicate order accepted: %v", err)
	}
	if err := d.ReorderSportsBackups(ctx, "nba:2", []string{b1.ID, "foreign"}); !errors.Is(err, ErrSportsLinkOrder) {
		t.Fatalf("foreign order accepted: %v", err)
	}
	// Deletion is scoped to the owning team.
	if err := d.DeleteSportsLink(ctx, "nba:14", b1.ID); err != nil {
		t.Fatal(err)
	}
	if links, _ := d.SportsTeamLinks(ctx, "nba:2"); len(links) != 3 {
		t.Fatalf("link deleted from wrong team: %+v", links)
	}
	if err := d.DeleteSportsLink(ctx, "nba:2", b1.ID); err != nil {
		t.Fatal(err)
	}
	if links, _ := d.SportsTeamLinks(ctx, "nba:2"); len(links) != 2 {
		t.Fatalf("delete: %+v", links)
	}
	// Batch lookup keeps teams separate.
	byTeam, err := d.SportsLinksForTeams(ctx, []string{"nba:2", "nba:14"})
	if err != nil || len(byTeam["nba:2"]) != 2 || len(byTeam["nba:14"]) != 0 {
		t.Fatalf("batch: %+v %v", byTeam, err)
	}
}

func TestSportsAutoLinksNeverReplacePrimary(t *testing.T) {
	ctx := context.Background()
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	seedLinkTeams(t, d)
	if _, err := d.SetSportsPrimaryLink(ctx, "nba:2", SportsLinkRow{ChannelRef: "manual", ChannelName: "User choice"}); err != nil {
		t.Fatal(err)
	}
	saved, err := d.InsertSportsAutoPrimaryLinks(ctx, []SportsLinkRow{
		{TeamID: "nba:2", ChannelRef: "auto", ChannelName: "Auto Celtics", LinkConfidence: 0.99, MatchReason: "Exact team name"},
		{TeamID: "nba:14", ChannelRef: "auto2", ChannelName: "Auto Heat", LinkConfidence: 0.97, MatchReason: "Full team name"},
	})
	if err != nil || len(saved) != 1 || saved[0].TeamID != "nba:14" || !saved[0].AutoLinked || saved[0].LinkConfidence != 0.97 {
		t.Fatalf("auto insert: %+v %v", saved, err)
	}
	links, _ := d.SportsTeamLinks(ctx, "nba:2")
	if links[0].ChannelName != "User choice" {
		t.Fatalf("manual primary replaced: %+v", links)
	}
	auto, _ := d.SportsTeamLinks(ctx, "nba:14")
	if len(auto) != 1 || auto[0].MatchReason != "Full team name" {
		t.Fatalf("provenance lost: %+v", auto)
	}
}

package db

import "context"

// OnlineSubtitle is a subtitle fetched from a subtitle site for an item.
type OnlineSubtitle struct {
	ItemID   string
	Key      string
	Provider string
	Language string
	Name     string
}

func (d *DB) AddOnlineSubtitle(ctx context.Context, s OnlineSubtitle) error {
	_, err := d.ExecContext(ctx, `INSERT INTO online_subtitles (item_id, key, provider, language, name, created_at) VALUES (?,?,?,?,?,?)
		ON CONFLICT DO NOTHING`, s.ItemID, s.Key, s.Provider, s.Language, s.Name, Now())
	return err
}

// OnlineSubtitles lists an item's fetched subtitles, oldest first, so their
// track numbers stay put as more are added.
func (d *DB) OnlineSubtitles(ctx context.Context, itemID string) ([]OnlineSubtitle, error) {
	rows, err := d.QueryContext(ctx, `SELECT item_id, key, provider, language, name FROM online_subtitles WHERE item_id=? ORDER BY created_at, rowid`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OnlineSubtitle
	for rows.Next() {
		var s OnlineSubtitle
		if err := rows.Scan(&s.ItemID, &s.Key, &s.Provider, &s.Language, &s.Name); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

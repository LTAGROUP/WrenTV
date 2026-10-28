package db

import (
	"context"
	"time"
)

// recentChannels is how many channels a user's recents keep.
const recentChannels = 20

// LiveFavorites lists the channels a user starred, oldest first.
func (d *DB) LiveFavorites(ctx context.Context, userID string) ([]string, error) {
	return d.channelIDs(ctx, `SELECT channel_id FROM live_favorites WHERE user_id=? ORDER BY added_at`, userID)
}

// SetLiveFavorite stars or unstars a channel.
func (d *DB) SetLiveFavorite(ctx context.Context, userID, channelID string, on bool) error {
	var err error
	if on {
		_, err = d.ExecContext(ctx, `INSERT INTO live_favorites (user_id, channel_id, added_at) VALUES (?, ?, ?)
			ON CONFLICT DO NOTHING`, userID, channelID, time.Now().Unix())
	} else {
		_, err = d.ExecContext(ctx, `DELETE FROM live_favorites WHERE user_id=? AND channel_id=?`, userID, channelID)
	}
	return err
}

// LiveRecents lists the channels a user watched lately, latest first.
func (d *DB) LiveRecents(ctx context.Context, userID string) ([]string, error) {
	return d.channelIDs(ctx, `SELECT channel_id FROM live_recents WHERE user_id=? ORDER BY watched_at DESC LIMIT ?`, userID, recentChannels)
}

// WatchedChannel puts a channel first in a user's recents.
func (d *DB) WatchedChannel(ctx context.Context, userID, channelID string) error {
	if _, err := d.ExecContext(ctx, `INSERT INTO live_recents (user_id, channel_id, watched_at) VALUES (?, ?, ?)
		ON CONFLICT (user_id, channel_id) DO UPDATE SET watched_at=excluded.watched_at`, userID, channelID, time.Now().Unix()); err != nil {
		return err
	}
	_, err := d.ExecContext(ctx, `DELETE FROM live_recents WHERE user_id=? AND channel_id NOT IN
		(SELECT channel_id FROM live_recents WHERE user_id=? ORDER BY watched_at DESC LIMIT ?)`, userID, userID, recentChannels)
	return err
}

func (d *DB) channelIDs(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

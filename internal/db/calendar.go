package db

import "context"

// CalendarShows are the shows a calendar looks at: ones in these libraries
// that have aired since `since` (by their own or an episode's air date),
// and ones the user has watched any of. watching marks the latter.
func (d *DB) CalendarShows(ctx context.Context, userID string, libraryIDs []string, since string) (shows []*Item, watching map[string]bool, err error) {
	watching = map[string]bool{}
	if len(libraryIDs) == 0 {
		return nil, watching, nil
	}
	rows, err := d.QueryContext(ctx, `SELECT DISTINCT e.show_id FROM watch_state w JOIN items e ON e.id=w.item_id
		WHERE w.user_id=? AND e.kind='episode' AND e.show_id IS NOT NULL`, userID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			watching[id] = true
		}
	}
	rows.Close()
	args := []any{}
	for _, id := range libraryIDs {
		args = append(args, id)
	}
	args = append(args, since, since, userID)
	rows, err = d.QueryContext(ctx, `SELECT `+itemCols+` FROM items i WHERE i.kind='show' AND i.tmdb_id > 0
		AND i.library_id IN (`+placeholders(len(libraryIDs))+`)
		AND (i.air_date >= ?
			OR EXISTS (SELECT 1 FROM items e WHERE e.show_id=i.id AND e.kind='episode' AND e.air_date >= ?)
			OR EXISTS (SELECT 1 FROM watch_state w JOIN items e ON e.id=w.item_id WHERE w.user_id=? AND e.show_id=i.id))
		ORDER BY i.sort_title`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, nil, err
		}
		shows = append(shows, it)
	}
	return shows, watching, rows.Err()
}

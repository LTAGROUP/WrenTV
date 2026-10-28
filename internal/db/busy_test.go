package db

import (
	"context"
	"sync"
	"testing"
)

// A transaction that reads before it writes mustn't fail with SQLITE_BUSY
// when another writes in between: it has to wait its turn like any write.
func TestTransactionsWaitForWriters(t *testing.T) {
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	if _, err := d.ExecContext(ctx, `CREATE TABLE n (v INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO n VALUES (0)`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8*50)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				tx, err := d.BeginTx(ctx, nil)
				if err != nil {
					errs <- err
					return
				}
				var v int
				if err := tx.QueryRowContext(ctx, `SELECT v FROM n`).Scan(&v); err != nil {
					tx.Rollback()
					errs <- err
					continue
				}
				if _, err := tx.ExecContext(ctx, `UPDATE n SET v=?`, v+1); err != nil {
					tx.Rollback()
					errs <- err
					continue
				}
				if err := tx.Commit(); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var v int
	d.QueryRowContext(ctx, `SELECT v FROM n`).Scan(&v)
	if v != 8*50 {
		t.Fatalf("v = %d, want %d", v, 8*50)
	}
}

package state

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(outDir string) (*Store, error) {
	dbPath := filepath.Join(outDir, "state.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open state db: %w", err)
	}

	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}

	return s.db.Close()
}

func (s *Store) Has(date string) (bool, error) {
	yearMonth, mask, err := parts(date)
	if err != nil {
		return false, err
	}

	var daysMask int64
	err = s.db.QueryRow(`select days_mask from archived_months where year_month = ?`, yearMonth).Scan(&daysMask)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to query archived month: %w", err)
	}

	return daysMask&mask != 0, nil
}

func (s *Store) Mark(date string) error {
	yearMonth, mask, err := parts(date)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`
		insert into archived_months(year_month, days_mask)
		values (?, ?)
		on conflict(year_month) do update set days_mask = archived_months.days_mask | excluded.days_mask
	`, yearMonth, mask)
	if err != nil {
		return fmt.Errorf("failed to mark archived month: %w", err)
	}

	return nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		create table if not exists archived_months (
			year_month text primary key,
			days_mask integer not null
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to migrate state db: %w", err)
	}

	return nil
}

func parts(date string) (string, int64, error) {
	if len(date) != 8 {
		return "", 0, fmt.Errorf("invalid archived date: %s", date)
	}

	day, err := strconv.Atoi(date[6:8])
	if err != nil {
		return "", 0, fmt.Errorf("invalid archived day: %w", err)
	}
	if day < 1 || day > 31 {
		return "", 0, fmt.Errorf("archived day is out of range: %d", day)
	}

	return date[:6], int64(1) << (day - 1), nil
}

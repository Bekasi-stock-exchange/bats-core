package store

import (
	"context"
	"fmt"
)

// Emiten is a listed instrument. ListedShares is used downstream to compute
// market cap for the index.
type Emiten struct {
	ID           int64
	Kode         string
	Nama         string
	ListedShares int64
}

// Participant is a broker (exchange participant).
type Participant struct {
	ID   int64
	Kode string
	Nama string
}

// LoadEmiten returns all emiten as a slice.
func (s *Store) LoadEmiten(ctx context.Context) ([]Emiten, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, kode, nama, listed_shares FROM emiten ORDER BY kode`)
	if err != nil {
		return nil, fmt.Errorf("store: query emiten: %w", err)
	}
	defer rows.Close()

	var out []Emiten
	for rows.Next() {
		var e Emiten
		if err := rows.Scan(&e.ID, &e.Kode, &e.Nama, &e.ListedShares); err != nil {
			return nil, fmt.Errorf("store: scan emiten: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LoadParticipant returns all participants as a slice.
func (s *Store) LoadParticipant(ctx context.Context) ([]Participant, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, kode, nama FROM participant ORDER BY kode`)
	if err != nil {
		return nil, fmt.Errorf("store: query participant: %w", err)
	}
	defer rows.Close()

	var out []Participant
	for rows.Next() {
		var p Participant
		if err := rows.Scan(&p.ID, &p.Kode, &p.Nama); err != nil {
			return nil, fmt.Errorf("store: scan participant: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

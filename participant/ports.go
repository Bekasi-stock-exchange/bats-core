// Package participant owns broker identity: creating brokers, issuing and
// revoking their API keys, and authenticating the keys they present.
//
// It never stores or returns a plaintext key beyond the single response that
// creates one. Only a SHA-256 hash reaches the database.
package participant

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound means no participant matched the lookup — an unknown code, or a key
// that is not valid.
var ErrNotFound = errors.New("participant: not found")

// Record is a broker as stored.
//
// APIKeyPrefix and APIKeyIssuedAt are nil until a key is issued. There is
// deliberately no plaintext key field: a hash cannot be reversed, so the key
// exists only as a local value inside Create and Issue.
type Record struct {
	ID             int64
	Kode           string
	Nama           string
	APIKeyPrefix   *string
	APIKeyIssuedAt *time.Time
}

func (r Record) HasAPIKey() bool { return r.APIKeyPrefix != nil }

// Declared here, in the package that consumes it, and satisfied by the repository
// package — so this package depends on a behaviour, not on a database handle.
type Repository interface {
	Create(ctx context.Context, kode, nama string) (Record, error)
	FindByAPIKeyHash(ctx context.Context, hash string) (Record, error)
	FindByKode(ctx context.Context, kode string) (Record, error)
	List(ctx context.Context) ([]Record, error)
	SetAPIKey(ctx context.Context, kode, hash, prefix string) error
	ClearAPIKey(ctx context.Context, kode string) error
}

package participant

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"bekasi-automatic-trading-system/market"
)

// keyPrefixLen is how much of a key is kept in the clear to identify it in
// listings. Long enough to distinguish keys at a glance, far too short to guess
// the rest.
const keyPrefixLen = 12

// keyRandomBytes is the entropy behind each key: 256 bits, which is not
// brute-forceable and leaves no reason to make it configurable.
const keyRandomBytes = 32

// ValidationError is a rejected request: well-formed, but breaking a business
// rule. Its message is returned to the client verbatim.
type ValidationError struct {
	Msg string
}

func (e ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// Service owns broker identity and credentials.
type Service struct {
	repo Repository
	dir  *market.Directory
}

// NewService wires the participant domain to its repository and the directory it
// keeps in step.
func NewService(repo Repository, dir *market.Directory) *Service {
	return &Service{repo: repo, dir: dir}
}

// Authenticate resolves a presented API key to the broker that owns it.
//
// The key is hashed and looked up by hash, so the plaintext is never compared
// against anything stored and never needs to be. An unknown key is
// indistinguishable from a revoked one, which is what stops this being a probe for
// valid keys.
func (s *Service) Authenticate(ctx context.Context, presented string) (Identity, error) {
	if presented == "" {
		return Identity{}, ErrNotFound
	}

	rec, err := s.repo.FindByAPIKeyHash(ctx, hashKey(presented))
	if err != nil {
		return Identity{}, err
	}
	return Identity{ID: rec.ID, Kode: rec.Kode, Nama: rec.Nama}, nil
}

// Create registers a broker and issues its first key, returning the key in the
// clear exactly once.
//
// The row is written before the directory is updated: the database owns
// uniqueness, so a duplicate code must fail before any in-memory state moves.
func (s *Service) Create(ctx context.Context, kode, nama string) (Record, string, error) {
	kode = strings.TrimSpace(kode)
	nama = strings.TrimSpace(nama)
	if kode == "" {
		return Record{}, "", invalid("kode is required")
	}
	if nama == "" {
		return Record{}, "", invalid("nama is required")
	}

	rec, err := s.repo.Create(ctx, kode, nama)
	if err != nil {
		return Record{}, "", err
	}
	s.dir.AddParticipant(market.Participant{ID: rec.ID, Kode: rec.Kode, Nama: rec.Nama})

	key, err := s.issue(ctx, rec.Kode)
	if err != nil {
		return Record{}, "", err
	}
	return rec, key, nil
}

// Issue mints a new key for an existing broker, invalidating any previous one.
func (s *Service) Issue(ctx context.Context, kode string) (string, error) {
	if _, err := s.repo.FindByKode(ctx, kode); err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", invalid("unknown participant: %s", kode)
		}
		return "", err
	}
	return s.issue(ctx, kode)
}

func (s *Service) issue(ctx context.Context, kode string) (string, error) {
	key, err := generateKey(kode)
	if err != nil {
		return "", err
	}
	if err := s.repo.SetAPIKey(ctx, kode, hashKey(key), key[:keyPrefixLen]); err != nil {
		return "", err
	}
	return key, nil
}

// Revoke removes a broker's key. The next request carrying it fails immediately,
// because authentication reads the database rather than a cache.
func (s *Service) Revoke(ctx context.Context, kode string) error {
	if _, err := s.repo.FindByKode(ctx, kode); err != nil {
		if errors.Is(err, ErrNotFound) {
			return invalid("unknown participant: %s", kode)
		}
		return err
	}
	return s.repo.ClearAPIKey(ctx, kode)
}

// List returns every broker. Keys are not included and cannot be: only their
// hashes are stored.
func (s *Service) List(ctx context.Context) ([]Record, error) {
	return s.repo.List(ctx)
}

// generateKey builds a key of the form jast_<KODE>_<random>. The code is embedded
// so a key found in a log or a config file can be traced to its owner without
// consulting the database.
func generateKey(kode string) (string, error) {
	buf := make([]byte, keyRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("participant: generate key: %w", err)
	}
	return fmt.Sprintf("jast_%s_%s", kode, base64.RawURLEncoding.EncodeToString(buf)), nil
}

// hashKey returns the hex SHA-256 of a key. This is what the database stores and
// what lookups match on, so a stolen dump yields no usable credential.
func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Package corporateaction is the admin surface for aksi korporasi: the events an
// issuer performs on its own instrument, which either restate what a share is or
// hand something to whoever holds one.
//
// Four kinds, along one dividing line. A split, a reverse split, and a bonus
// change the *share count* — every holder's position is restated and so is the
// instrument's listed_shares, but nobody becomes richer or poorer for it. A
// dividend changes no share count at all; it moves cash out of nowhere into the
// holders' wallets.
//
// Every action is announced first and executed second. That is not ceremony: an
// announced action can still be cancelled, while an executed one has moved every
// holder's ledger and cannot be undone from here. The gap is also where a
// participant learns what is coming, which is the entire purpose of announcing a
// corporate action in the real market.
//
// Admin-only, for the same reason an IPO is: a broker that could split its own
// holdings or pay itself a dividend could mint shares and cash at will. The
// package sits beside underwriter rather than under emiten, because an action is
// an event over an instrument rather than a property of one.
package corporateaction

import (
	"context"
	"time"
)

// Jenis is the kind of corporate action.
//
// A string rather than an integer because it is stored, returned on the wire, and
// read by an operator — three places where 2 means nothing and "BONUS" means
// exactly one thing.
type Jenis string

const (
	// Split multiplies every holding: a 1:2 split turns one share into two, and
	// halves the price so the holding is worth what it was.
	Split Jenis = "SPLIT"

	// ReverseSplit divides every holding: a 2:1 reverse turns two shares into
	// one. The mirror of Split, and separate only so the announcement says which
	// direction was intended — the arithmetic is the same ratio either way.
	ReverseSplit Jenis = "REVERSE_SPLIT"

	// Bonus issues new shares to existing holders at no cost. Ratio 2:3 hands a
	// holder of two shares a third. Unlike a split, this genuinely increases the
	// shares outstanding rather than re-cutting them, so it is not price-neutral
	// by construction — but the reference price is still adjusted, because the
	// same company value is now spread over more shares.
	Bonus Jenis = "BONUS"

	// Dividend pays cash per share held. It restates no share count.
	Dividend Jenis = "DIVIDEND"
)

func (j Jenis) Valid() bool {
	switch j {
	case Split, ReverseSplit, Bonus, Dividend:
		return true
	}
	return false
}

// Everything but a dividend restates holdings and listed_shares.
func (j Jenis) AffectsShares() bool { return j != Dividend }

// Status is where an action stands in its lifecycle.
//
// An action enters Announced and leaves for exactly one of the other two. There
// is no path back: an executed action has moved real balances, and a cancelled
// one was never allowed to.
type Status string

const (
	// Announced is decided but not yet applied. The only status from which an
	// action may be executed or cancelled.
	Announced Status = "ANNOUNCED"

	// Executed means the ledgers have moved. Terminal.
	Executed Status = "EXECUTED"

	// Cancelled means it never will. Terminal, and kept rather than deleted:
	// participants were told about it, so it is part of the instrument's history.
	Cancelled Status = "CANCELLED"
)

// Record is one corporate action as stored.
//
// RatioFrom/RatioTo and Amount are pointers because neither applies to every
// kind, and nil is the honest representation of "this action has no such term" —
// a dividend with RatioFrom 0 would read as a ratio that was set to something
// meaningless rather than one that was never set.
//
// EmitenID rather than a code: the code is the directory's to resolve, and
// storing a second copy would be free to drift when an instrument is renamed.
type Record struct {
	ID        int64
	EmitenID  int64
	Jenis     Jenis
	Status    Status
	RatioFrom *int64
	RatioTo   *int64
	Amount    *int64

	// RecordDate is when holdings are read to decide who receives what. Stored
	// because it is what an issuer announces and a participant plans against;
	// execution itself reads holdings as they stand at that moment, since this
	// engine keeps no end-of-day snapshot to consult.
	RecordDate *time.Time
	Keterangan string
	CreatedAt  time.Time
	ExecutedAt *time.Time
}

// Entry is what one broker received from an executed action.
//
// SharesBefore and SharesAfter are both recorded rather than just the difference,
// because the point of this row is to answer "what did this broker hold, and what
// did that become" — a delta alone loses the first half, and with it any way to
// verify the ratio was applied correctly.
type Entry struct {
	ParticipantID   int64
	ParticipantKode string
	ParticipantNama string
	SharesBefore    int64
	SharesAfter     int64
	CashAmount      int64
}

// Holding is one broker's stake in the instrument an action is about, read at
// execution to decide what that broker receives.
type Holding struct {
	ParticipantID int64
	Shares        int64
}

// Repository reads and writes corporate actions and their execution entries.
//
// Declared here, in the package that consumes it, and satisfied by the repository
// package — so this package depends on a behaviour rather than on a database
// handle.
type Repository interface {
	CreateAction(ctx context.Context, rec Record) (Record, error)

	// Newest first. A nil emitenID means every instrument; a non-nil one scopes
	// to that instrument alone.
	ListActions(ctx context.Context, emitenID *int64, limit, offset int) ([]Record, error)

	// Totals the same filter, for the pagination envelope.
	CountActions(ctx context.Context, emitenID *int64) (int, error)

	// Returns ErrNotFound when the id does not exist.
	FindAction(ctx context.Context, id int64) (Record, error)

	// Empty for an action that has not executed.
	EntriesByAction(ctx context.Context, actionID int64) ([]Entry, error)

	// HoldersOf returns every broker holding this instrument, with a non-zero
	// position. This is the set an execution distributes to.
	HoldersOf(ctx context.Context, emitenID int64) ([]Holding, error)

	// CancelAction moves an announced action to CANCELLED, returning
	// ErrNotAnnounced if it has already been executed or cancelled. The status
	// check is in the UPDATE's WHERE clause rather than in the service, because
	// only the database sees two concurrent requests.
	CancelAction(ctx context.Context, id int64) error

	// ExecuteShareAction applies a split, reverse split, or bonus in one
	// transaction: the per-broker share restatements, the audit entries, the
	// instrument's new listed_shares and reference price, and the action's own
	// status.
	//
	// One transaction because a partial write here is unrecoverable in a way an
	// ordinary failed request is not: holdings restated without the listed_shares
	// to match would leave the instrument's own share count disagreeing with the
	// sum of its holders' positions, and every valuation derived from it wrong,
	// with no record of which brokers were already converted.
	ExecuteShareAction(ctx context.Context, id, emitenID, listedShares int64, reference *int64, entries []Entry) error

	// ExecuteDividend applies a dividend in one transaction: the wallet credits,
	// the audit entries, and the action's status. Same reasoning as
	// ExecuteShareAction — brokers paid with no record of payment cannot be told
	// apart from brokers who were funded by an operator.
	ExecuteDividend(ctx context.Context, id int64, entries []Entry) error
}

// EmitenReader is the instrument lookup an action needs, and the share-count
// restatement a split or bonus performs on it.
//
// Declared as an interface for the same reason underwriter.EmitenLister is: this
// package drives the instrument without reaching into the emiten domain's
// internals, and stays testable without a database.
type EmitenReader interface {
	// RestateShares updates an instrument's listed share count and its band
	// reference after a split or bonus, in the database and in the live directory
	// and registry alike. A stale directory would leave every valuation using the
	// pre-split count.
	RestateShares(ctx context.Context, emitenID, listedShares int64, reference *int64) error
}

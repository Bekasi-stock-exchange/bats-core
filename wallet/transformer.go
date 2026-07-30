package wallet

import (
	"time"

	"bekasi-automatic-trading-system/market"
)

// Codes resolves the id stored on a wallet into its code, using the directory
// already held in memory — so listing wallets needs no join.
type Codes struct {
	dir *market.Directory
}

// NewCodes returns a resolver backed by the directory.
func NewCodes(dir *market.Directory) Codes { return Codes{dir: dir} }

// ToWalletView converts a stored wallet into its API shape.
func (c Codes) ToWalletView(rec Record) WalletView {
	view := WalletView{
		Balance:   rec.Balance,
		UpdatedAt: rec.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if p, ok := c.dir.ParticipantByID(rec.ParticipantID); ok {
		view.Participant = p.Kode
	}
	return view
}

// ToWalletViews converts a batch, preserving order. Always non-nil so the
// field marshals as [] rather than null.
func (c Codes) ToWalletViews(records []Record) []WalletView {
	out := make([]WalletView, 0, len(records))
	for _, rec := range records {
		out = append(out, c.ToWalletView(rec))
	}
	return out
}

package wallet

// AdjustRequest funds or debits one broker's wallet.
type AdjustRequest struct {
	// Broker code the adjustment lands on.
	Participant string `json:"participant" example:"YP"`
	// Amount to move, in whole currency units. Positive credits the broker,
	// negative debits it; zero is rejected. A debit may not exceed the cash the
	// broker has not already committed to resting buy orders.
	Amount int64 `json:"amount" format:"int64" example:"5000000000"`
}

// WalletView is one broker's cash balance.
type WalletView struct {
	Participant string `json:"participant" example:"YP"`
	// Cash balance held by this broker.
	Balance int64 `json:"balance" format:"int64" example:"5000000000"`
	// When this balance last changed.
	UpdatedAt string `json:"updated_at" example:"2026-07-30T09:15:02Z"`
}

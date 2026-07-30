package wallet

// WalletView is one broker's cash balance.
type WalletView struct {
	Participant string `json:"participant" example:"YP"`
	// Cash balance held by this broker.
	Balance int64 `json:"balance" format:"int64" example:"5000000000"`
	// When this balance last changed.
	UpdatedAt string `json:"updated_at" example:"2026-07-30T09:15:02Z"`
}

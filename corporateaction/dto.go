package corporateaction

// AnnounceRequest is the body of POST /api/admin/corporate-actions.
//
// It announces an action; it does not perform one. Nothing moves until the
// execute endpoint is called, which is what leaves room to cancel.
//
// The terms are kind-dependent and only one set is ever read: ratio_from and
// ratio_to for a split, reverse split, or bonus, and amount for a dividend.
// Supplying the wrong set is rejected rather than ignored — an operator who sends
// a dividend with a ratio has misunderstood something, and silently dropping the
// field would let that misunderstanding reach execution.
type AnnounceRequest struct {
	// Instrument code the action is over. Must be listed and trading.
	Kode string `json:"kode" example:"BBCA" validate:"required"`

	// One of SPLIT, REVERSE_SPLIT, BONUS, DIVIDEND.
	Jenis string `json:"jenis" example:"SPLIT" validate:"required" enums:"SPLIT,REVERSE_SPLIT,BONUS,DIVIDEND"`

	// Old shares in the ratio. Required for SPLIT, REVERSE_SPLIT and BONUS;
	// must be omitted for DIVIDEND.
	//
	// A 1:2 split is ratio_from 1, ratio_to 2 — one share becomes two. A 2:1
	// reverse split is ratio_from 2, ratio_to 1. A bonus of one new share per two
	// held is ratio_from 2, ratio_to 3: the holder ends with three where they had
	// two.
	RatioFrom int64 `json:"ratio_from,omitempty" format:"int64"`

	// New shares in the ratio. See ratio_from.
	RatioTo int64 `json:"ratio_to,omitempty" format:"int64"`

	// Rupiah per share held. Required for DIVIDEND; must be omitted otherwise.
	Amount int64 `json:"amount,omitempty" format:"int64"`

	// When holdings are read to decide who receives what, as YYYY-MM-DD.
	// Optional and informational: execution distributes to the holdings as they
	// stand at that moment, since the engine keeps no end-of-day snapshot.
	RecordDate string `json:"record_date,omitempty" example:"2026-08-10"`

	// Free text for the announcement.
	Keterangan string `json:"keterangan,omitempty" example:"Stock split 1:2 untuk meningkatkan likuiditas"`
}

// ActionView is a corporate action as returned by the API.
//
// The ratio and amount fields are pointers so a kind that has no such term omits
// it entirely rather than reporting 0 — which would read as a dividend of nothing
// rather than as an action that pays no dividend at all.
type ActionView struct {
	ID int64 `json:"id" format:"int64" example:"1"`
	// Instrument code, resolved from the directory.
	Kode string `json:"kode" example:"BBCA"`
	Nama string `json:"nama" example:"PT Bank Central Asia Tbk"`

	Jenis  string `json:"jenis" example:"SPLIT" enums:"SPLIT,REVERSE_SPLIT,BONUS,DIVIDEND"`
	Status string `json:"status" example:"ANNOUNCED" enums:"ANNOUNCED,EXECUTED,CANCELLED"`

	// The ratio, for a split, reverse split or bonus. Absent for a dividend.
	RatioFrom *int64 `json:"ratio_from,omitempty" format:"int64" example:"1"`
	RatioTo   *int64 `json:"ratio_to,omitempty" format:"int64" example:"2"`
	// Human-readable form of the same ratio, e.g. "1:2". Absent for a dividend.
	Ratio string `json:"ratio,omitempty" example:"1:2"`

	// Rupiah per share, for a dividend. Absent otherwise.
	Amount *int64 `json:"amount,omitempty" format:"int64" example:"50"`

	RecordDate string `json:"record_date,omitempty" example:"2026-08-10"`
	Keterangan string `json:"keterangan,omitempty" example:"Stock split 1:2"`

	CreatedAt  string `json:"created_at" example:"2026-08-03T09:00:00Z"`
	ExecutedAt string `json:"executed_at,omitempty" example:"2026-08-10T09:00:00Z"`
}

// ActionDetailView is one action with what it actually did.
//
// Entries is empty until the action executes, which is the honest answer for an
// announced action: nobody has received anything yet.
type ActionDetailView struct {
	ActionView

	// The instrument's listed shares before and after. Equal unless a share
	// action has executed.
	ListedSharesBefore int64 `json:"listed_shares_before,omitempty" format:"int64" example:"40000"`
	ListedSharesAfter  int64 `json:"listed_shares_after,omitempty" format:"int64" example:"80000"`

	// Total rupiah paid out, for an executed dividend.
	TotalCash int64 `json:"total_cash,omitempty" format:"int64" example:"2000000"`

	// What each broker received. Empty for an action that has not executed.
	Entries []EntryView `json:"entries"`
}

// EntryView is one broker's share of an executed action.
type EntryView struct {
	// Broker code the shares or cash went to.
	Participant string `json:"participant" example:"YP"`
	Nama        string `json:"nama" example:"Mirae Asset Sekuritas"`

	// Shares held before and after. Equal for a dividend, which restates nothing.
	SharesBefore int64 `json:"shares_before" format:"int64" example:"1000"`
	SharesAfter  int64 `json:"shares_after" format:"int64" example:"2000"`

	// Rupiah paid to this broker. Zero for a split or bonus.
	CashAmount int64 `json:"cash_amount" format:"int64" example:"50000"`
}

// ExecuteResponse is the result of executing an action: the action as it now
// stands, and every ledger movement it caused.
type ExecuteResponse struct {
	ActionDetailView
}

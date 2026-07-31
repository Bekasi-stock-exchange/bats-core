package marketconfig

import "time"

// ConfigView is the exchange's trading parameters as served to a client.
type ConfigView struct {
	// The lowest price a limit order may be submitted at, in rupiah. An order
	// priced below it is rejected with 400 and never reaches the order book.
	MinPrice int64 `json:"min_price" format:"int64" example:"50"`

	// How far one emiten's price may move from its reference price before
	// trading in it is halted, in basis points. 3000 is 30%.
	EmitenHaltBPS int64 `json:"emiten_halt_bps" format:"int64" example:"3000"`

	// How far the index may fall from its opening value before trading halts
	// market-wide, in basis points. 1200 is 12%.
	IndexHaltBPS int64 `json:"index_halt_bps" format:"int64" example:"1200"`

	// How long a triggered halt lasts, in seconds.
	HaltDurationSeconds int64 `json:"halt_duration_seconds" format:"int64" example:"120"`
}

// UpdateRequest changes one or more trading parameters.
//
// Every field is a pointer so that absent and zero are distinguishable. With a
// plain int64, a body that omits min_price is indistinguishable from one that
// sends 0, and the update would silently reset the floor to a value that
// disables the rule. Absent means "leave it alone"; present means "set it to
// this", and 0 is then rejected on its own merits.
//
// The distinction matters more for the halt thresholds than for the floor: a
// zero threshold does not disable a breaker, it arms one that trips on the first
// trade of the day and stops the market.
type UpdateRequest struct {
	// New price floor in rupiah, must be > 0. Omit to leave it unchanged.
	MinPrice *int64 `json:"min_price,omitempty" format:"int64" example:"50"`

	// New single-emiten halt threshold in basis points, must be in 1..10000.
	// Omit to leave it unchanged.
	EmitenHaltBPS *int64 `json:"emiten_halt_bps,omitempty" format:"int64" example:"3000"`

	// New market-wide halt threshold in basis points, must be in 1..10000. Omit
	// to leave it unchanged.
	IndexHaltBPS *int64 `json:"index_halt_bps,omitempty" format:"int64" example:"1200"`

	// New halt duration in seconds, must be in 1..86400. Omit to leave it
	// unchanged.
	HaltDurationSeconds *int64 `json:"halt_duration_seconds,omitempty" format:"int64" example:"120"`
}

// ToConfigView renders settings for the wire.
//
// The duration is carried as a time.Duration internally and published as whole
// seconds, matching both the column and the request field. Validation rejects
// anything that is not a whole number of seconds, so the conversion here is
// exact rather than truncating.
func ToConfigView(s Settings) ConfigView {
	return ConfigView{
		MinPrice:            s.MinPrice,
		EmitenHaltBPS:       s.EmitenHaltBPS,
		IndexHaltBPS:        s.IndexHaltBPS,
		HaltDurationSeconds: int64(s.HaltDuration / time.Second),
	}
}

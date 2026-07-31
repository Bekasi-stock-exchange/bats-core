package index

// IndexView is the composite index level as served to a client.
//
// value is the index level itself; market_cap and divisor are the inputs that
// produced it, included so a reader can verify the level rather than trust it.
type IndexView struct {
	Kode string `json:"kode" example:"IHSG"`
	Nama string `json:"nama" example:"Indeks Harga Saham Gabungan"`
	// The index level, rounded to two decimals.
	Value float64 `json:"value" example:"1247.83"`
	// Summed free-float market capitalisation of every priced instrument, in
	// rupiah.
	MarketCap int64 `json:"market_cap" format:"int64" example:"1247830000000000"`
	// The divisor in force. It is restated whenever an instrument lists, so the
	// level does not jump on an event that moved no price.
	Divisor float64 `json:"divisor" example:"1000000000000"`
	// How many instruments were priced into this level.
	Members int `json:"members" example:"5"`
	// How many instruments are listed in total. members below this means some
	// instrument has no price at all and was excluded rather than counted as zero.
	Total      int    `json:"total" example:"5"`
	CapturedAt string `json:"captured_at" example:"2026-07-30T09:15:02Z"`
}

// SnapshotView is one historical index level.
type SnapshotView struct {
	Value      float64 `json:"value" example:"1247.83"`
	MarketCap  int64   `json:"market_cap" format:"int64" example:"1247830000000000"`
	Divisor    float64 `json:"divisor" example:"1000000000000"`
	Members    int     `json:"members" example:"5"`
	CapturedAt string  `json:"captured_at" example:"2026-07-30T09:15:02Z"`
}

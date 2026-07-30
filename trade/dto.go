package trade

// TradeView is one execution in the admin log.
type TradeView struct {
	ID          int64  `json:"id" format:"int64" example:"412"`
	Seq         int64  `json:"seq" format:"int64" example:"412"`
	Emiten      string `json:"emiten" example:"BBCA"`
	Buyer       string `json:"buyer" example:"YP"`
	Seller      string `json:"seller" example:"PD"`
	BuyOrderID  int64  `json:"buy_order_id" format:"int64" example:"31"`
	SellOrderID int64  `json:"sell_order_id" format:"int64" example:"28"`
	Price       int64  `json:"price" format:"int64" example:"8000"`
	Qty         int64  `json:"qty" format:"int64" example:"500"`
	Value       int64  `json:"value" format:"int64" example:"4000000"`
	ExecutedAt  string `json:"executed_at" example:"2026-07-30T09:15:02Z"`
}

// TransactionView is one fill from the querying broker's point of view.
//
// side and counterparty are relative: the same execution reads buy/PD for YP and
// sell/YP for PD. A broker that matched its own resting order sees two rows, one
// per side, which is what makes buys minus sells reconcile against its holdings.
type TransactionView struct {
	TradeID int64  `json:"trade_id" format:"int64" example:"412"`
	Seq     int64  `json:"seq" format:"int64" example:"412"`
	Emiten  string `json:"emiten" example:"BBCA"`
	// Which side this broker was on.
	Side string `json:"side" enums:"buy,sell" example:"buy"`
	// The broker on the other side.
	Counterparty string `json:"counterparty" example:"PD"`
	Price        int64  `json:"price" format:"int64" example:"8000"`
	Qty          int64  `json:"qty" format:"int64" example:"500"`
	// price × qty.
	Value      int64  `json:"value" format:"int64" example:"4000000"`
	ExecutedAt string `json:"executed_at" example:"2026-07-30T09:15:02Z"`
}

// TickView is one raw execution in a price series.
type TickView struct {
	Seq        int64  `json:"seq" format:"int64" example:"412"`
	Price      int64  `json:"price" format:"int64" example:"8050"`
	Qty        int64  `json:"qty" format:"int64" example:"30"`
	ExecutedAt string `json:"executed_at" example:"2026-07-30T09:15:02Z"`
}

// CandleView is one aggregated OHLC interval.
type CandleView struct {
	// Start of the bucket, UTC-aligned.
	Time   string `json:"time" example:"2026-07-30T09:00:00Z"`
	Open   int64  `json:"open" format:"int64" example:"8000"`
	High   int64  `json:"high" format:"int64" example:"8100"`
	Low    int64  `json:"low" format:"int64" example:"7950"`
	Close  int64  `json:"close" format:"int64" example:"8050"`
	Volume int64  `json:"volume" format:"int64" example:"12400"`
}

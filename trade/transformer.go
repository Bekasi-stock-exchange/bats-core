package trade

import (
	"time"

	"bekasi-automatic-trading-system/market"
)

// Backed by market.Directory, which already holds every emiten and participant in
// memory — so listing trades needs no join. An id with no entry falls back to an
// empty string rather than failing the whole page.
type Codes struct {
	dir *market.Directory
}

func NewCodes(dir *market.Directory) Codes { return Codes{dir: dir} }

func (c Codes) emiten(id int64) string {
	if e, ok := c.dir.EmitenByID(id); ok {
		return e.Kode
	}
	return ""
}

func (c Codes) participant(id int64) string {
	if p, ok := c.dir.ParticipantByID(id); ok {
		return p.Kode
	}
	return ""
}

// Always non-nil so the field marshals as [] rather than null.
func (c Codes) ToTradeViews(records []Record) []TradeView {
	out := make([]TradeView, 0, len(records))
	for _, t := range records {
		out = append(out, TradeView{
			ID:          t.ID,
			Seq:         t.Seq,
			Emiten:      c.emiten(t.EmitenID),
			Buyer:       c.participant(t.BuyParticipantID),
			Seller:      c.participant(t.SellParticipantID),
			BuyOrderID:  t.BuyOrderID,
			SellOrderID: t.SellOrderID,
			Price:       t.Price,
			Qty:         t.Qty,
			Value:       t.Price * t.Qty,
			ExecutedAt:  utc(t.ExecutedAt),
		})
	}
	return out
}

func (c Codes) ToTransactionViews(txs []Transaction) []TransactionView {
	out := make([]TransactionView, 0, len(txs))
	for _, t := range txs {
		out = append(out, TransactionView{
			TradeID:      t.TradeID,
			Seq:          t.Seq,
			Emiten:       c.emiten(t.EmitenID),
			Side:         t.Side,
			Counterparty: c.participant(t.CounterpartyID),
			Price:        t.Price,
			Qty:          t.Qty,
			Value:        t.Price * t.Qty,
			ExecutedAt:   utc(t.ExecutedAt),
		})
	}
	return out
}

func ToTickViews(ticks []Tick) []TickView {
	out := make([]TickView, 0, len(ticks))
	for _, t := range ticks {
		out = append(out, TickView{
			Seq:        t.Seq,
			Price:      t.Price,
			Qty:        t.Qty,
			ExecutedAt: utc(t.ExecutedAt),
		})
	}
	return out
}

func ToCandleViews(candles []Candle) []CandleView {
	out := make([]CandleView, 0, len(candles))
	for _, c := range candles {
		out = append(out, CandleView{
			Time:   utc(c.Time),
			Open:   c.Open,
			High:   c.High,
			Low:    c.Low,
			Close:  c.Close,
			Volume: c.Volume,
		})
	}
	return out
}

// RFC 3339 UTC, so clients never have to reason about the server's timezone.
func utc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

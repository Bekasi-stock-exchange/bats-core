package corporateaction

import (
	"strconv"
	"time"

	"bekasi-automatic-trading-system/market"
)

// Codes resolves the emiten id stored on an action into the code and name a
// reader sees, using the directory already held in memory — so listing needs no
// join.
type Codes struct {
	dir *market.Directory
}

func NewCodes(dir *market.Directory) Codes { return Codes{dir: dir} }

func (c Codes) ToActionView(rec Record) ActionView {
	view := ActionView{
		ID:         rec.ID,
		Jenis:      string(rec.Jenis),
		Status:     string(rec.Status),
		RatioFrom:  rec.RatioFrom,
		RatioTo:    rec.RatioTo,
		Amount:     rec.Amount,
		Keterangan: rec.Keterangan,
		CreatedAt:  rec.CreatedAt.UTC().Format(time.RFC3339),
	}

	if e, ok := c.dir.EmitenByID(rec.EmitenID); ok {
		view.Kode = e.Kode
		view.Nama = e.Nama
	}

	// The ratio is also rendered as one string, because "1:2" is how a split is
	// written everywhere a human reads one, and reassembling it from two fields
	// is work every client would otherwise repeat.
	if rec.RatioFrom != nil && rec.RatioTo != nil {
		view.Ratio = strconv.FormatInt(*rec.RatioFrom, 10) + ":" + strconv.FormatInt(*rec.RatioTo, 10)
	}
	if rec.RecordDate != nil {
		view.RecordDate = rec.RecordDate.UTC().Format(dateLayout)
	}
	if rec.ExecutedAt != nil {
		view.ExecutedAt = rec.ExecutedAt.UTC().Format(time.RFC3339)
	}
	return view
}

// Preserves order. Always non-nil so the field marshals as [] rather than null.
func (c Codes) ToActionViews(recs []Record) []ActionView {
	out := make([]ActionView, 0, len(recs))
	for _, rec := range recs {
		out = append(out, c.ToActionView(rec))
	}
	return out
}

// The before/after share totals are summed from the entries rather than read off
// the instrument, so the detail of an executed action keeps reporting what that
// action did even after later actions have moved the instrument on again. Reading
// the directory would report today's count for an action from last month.
func (c Codes) ToDetailView(rec Record, entries []Entry) ActionDetailView {
	detail := ActionDetailView{
		ActionView: c.ToActionView(rec),
		Entries:    make([]EntryView, 0, len(entries)),
	}

	for _, en := range entries {
		view := EntryView{
			Participant:  en.ParticipantKode,
			Nama:         en.ParticipantNama,
			SharesBefore: en.SharesBefore,
			SharesAfter:  en.SharesAfter,
			CashAmount:   en.CashAmount,
		}
		// The repository joins these, but an entry for a participant that has since
		// been removed would arrive blank; fall back to the directory so the code
		// is still reported.
		if view.Participant == "" {
			if p, ok := c.dir.ParticipantByID(en.ParticipantID); ok {
				view.Participant = p.Kode
				view.Nama = p.Nama
			}
		}

		detail.ListedSharesBefore += en.SharesBefore
		detail.ListedSharesAfter += en.SharesAfter
		detail.TotalCash += en.CashAmount
		detail.Entries = append(detail.Entries, view)
	}
	return detail
}

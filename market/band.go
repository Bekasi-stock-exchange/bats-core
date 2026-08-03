package market

// Band is the price range an instrument may trade in for the session: the
// reference price it is anchored to, and the ceiling and floor derived from it.
//
// Both limits are inclusive. An order exactly at the ceiling is accepted and
// trades; what it then does is trip the circuit breaker, because the instrument
// has reached the edge of its permitted range. Making the boundary rejectable
// instead would leave the band unreachable and the breaker unreachable with it —
// the ceiling would be a price no order could ever print at.
type Band struct {
	Reference int64
	Ceiling   int64
	Floor     int64
}

// NewBand derives the session's price range from a reference price and a
// threshold in basis points.
//
// The arithmetic stays in integers throughout: ceiling is ref + ref*bps/10000
// and floor is ref - ref*bps/10000, with the division last so the rounding
// error is at most one rupiah rather than compounding through a float. Both
// round toward zero, which widens the band by under a rupiah at the ceiling and
// narrows it by the same at the floor — below the Rp 1 tick, so no order can
// land in the difference.
//
// A floor of zero or below is clamped to 1. A threshold of 10000 bps (100%)
// would otherwise put the floor at 0, and a price of 0 is not a cheap quote but
// a broken one — the exchange's own minimum price rule rejects it anyway, so the
// clamp keeps the two rules from disagreeing about what the floor even is.
func NewBand(reference, bps int64) Band {
	delta := reference * bps / bpsDenominator

	floor := reference - delta
	if floor < 1 {
		floor = 1
	}

	return Band{
		Reference: reference,
		Ceiling:   reference + delta,
		Floor:     floor,
	}
}

// bpsDenominator is the number of basis points in 100%. Duplicated from
// marketconfig rather than imported: market is the shared kernel and sits below
// every domain package, so importing a domain here would invert the dependency
// the whole layout rests on. The constant is a unit definition, not a policy —
// it cannot drift the way a threshold can.
const bpsDenominator int64 = 10_000

// Allows reports whether a price may be quoted within this band.
func (b Band) Allows(price int64) bool {
	return price >= b.Floor && price <= b.Ceiling
}

// AtLimit reports whether a price has reached either edge of the band.
//
// This is the circuit breaker's trigger condition, and it is deliberately
// distinct from !Allows: a price outside the band never executes, because
// auto-rejection refuses it at the gate. The breaker fires on a price that was
// admitted and printed — one that reached the edge legally. Without this
// distinction the breaker could only ever be tripped by an order the exchange
// had already refused, which is to say never.
func (b Band) AtLimit(price int64) bool {
	return price >= b.Ceiling || price <= b.Floor
}

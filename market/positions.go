package market

// positions is the share ledger: what each broker holds, and how much of that is
// already committed to resting sell orders.
//
// Every method requires Registry.mu to be held. That is the whole point: the
// availability check and the commitment that follows it must be one atomic step
// with matching, or two concurrent sells can both pass a check that only one of
// them can afford. Guarding this with its own lock would not help — the gap
// between "check" and "match" is exactly where the race lives.
type positions struct {
	held     map[int64]map[int64]int64 // participant -> emiten -> shares owned
	reserved map[int64]map[int64]int64 // participant -> emiten -> shares committed to resting sells
}

func newPositions() *positions {
	return &positions{
		held:     make(map[int64]map[int64]int64),
		reserved: make(map[int64]map[int64]int64),
	}
}

// available is what a broker may still sell: what it owns, minus what its
// resting sell orders have already promised away.
func (p *positions) available(participantID, emitenID int64) int64 {
	return p.held[participantID][emitenID] - p.reserved[participantID][emitenID]
}

// Held returns a broker's current holding of one emiten.
func (p *positions) Held(participantID, emitenID int64) int64 {
	return p.held[participantID][emitenID]
}

func (p *positions) addHeld(participantID, emitenID, delta int64) {
	add(p.held, participantID, emitenID, delta)
}

func (p *positions) addReserved(participantID, emitenID, delta int64) {
	add(p.reserved, participantID, emitenID, delta)
}

// add applies a delta, creating the inner map on demand and dropping entries that
// fall back to zero so the maps do not grow without bound.
func add(m map[int64]map[int64]int64, outer, inner, delta int64) {
	if delta == 0 {
		return
	}
	byEmiten := m[outer]
	if byEmiten == nil {
		byEmiten = make(map[int64]int64)
		m[outer] = byEmiten
	}
	byEmiten[inner] += delta
	if byEmiten[inner] == 0 {
		delete(byEmiten, inner)
		if len(byEmiten) == 0 {
			delete(m, outer)
		}
	}
}

// Holding is one broker's stake in one emiten, used to seed the ledger at startup.
type Holding struct {
	ParticipantID int64
	EmitenID      int64
	AmountShared  int64
}

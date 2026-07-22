package api

import "net/http"

// handleOrderBook handles GET /orderbook/{kode}: the current book state for one
// emiten, aggregated by price level.
func (s *Server) handleOrderBook(w http.ResponseWriter, r *http.Request) {
	kode := r.PathValue("kode")
	em, ok := s.emitenByKode[kode]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown emiten: "+kode)
		return
	}

	s.mu.Lock()
	snap := snapshotBook(em.Kode, s.engineFor(em.ID).Book())
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, snap)
}

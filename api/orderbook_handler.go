package api

import (
	"net/http"
	"sort"
	"strconv"
)

// handleOrderBooks handles GET /orderbook: returns paginated book states for
// all emitens.
func (s *Server) handleOrderBooks(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	kodes := make([]string, 0, len(s.emitenByKode))
	for k := range s.emitenByKode {
		kodes = append(kodes, k)
	}
	sort.Strings(kodes)

	start := (page - 1) * limit
	if start > len(kodes) {
		start = len(kodes)
	}
	end := start + limit
	if end > len(kodes) {
		end = len(kodes)
	}

	pagedKodes := kodes[start:end]
	results := make([]bookSnapshot, 0, len(pagedKodes))

	s.mu.Lock()
	for _, kode := range pagedKodes {
		em := s.emitenByKode[kode]
		snap := snapshotBook(em.Kode, s.engineFor(em.ID).Book())
		results = append(results, snap)
	}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"page":  page,
		"limit": limit,
		"total": len(kodes),
		"data":  results,
	})
}

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

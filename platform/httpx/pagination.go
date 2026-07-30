package httpx

import (
	"net/http"
	"strconv"
)

// Pagination defaults and bounds for list endpoints.
const (
	DefaultLimit = 10
	MaxLimit     = 100
)

// Page is the envelope for a paginated list response.
//
// Fields are declared in alphabetical order on purpose. This shape previously
// came from a map[string]any, and encoding/json sorts map keys — so the wire
// order was data, limit, page, total. A struct serializes in declaration order,
// so declaring them alphabetically keeps the response bytes identical.
type Page[T any] struct {
	Data  []T `json:"data"`
	Limit int `json:"limit"`
	Page  int `json:"page"`
	Total int `json:"total"`
}

// NewPage builds a page envelope, normalizing a nil slice to an empty one so the
// JSON is [] rather than null.
func NewPage[T any](data []T, page, limit, total int) Page[T] {
	if data == nil {
		data = []T{}
	}
	return Page[T]{Data: data, Limit: limit, Page: page, Total: total}
}

// ParsePagination reads the page and limit query parameters, clamping them to
// sane bounds: page >= 1, limit in [1, MaxLimit] defaulting to DefaultLimit.
//
// Unparseable values fall back to the defaults rather than erroring, preserving
// the existing behaviour where ?page=abc is treated as page 1.
func ParsePagination(r *http.Request) (page, limit int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	return page, limit
}

// Slice returns the half-open bounds of the requested page within a collection
// of size total, clamped so they are always a valid slice range.
func Slice(page, limit, total int) (start, end int) {
	start = (page - 1) * limit
	if start > total {
		start = total
	}
	end = start + limit
	if end > total {
		end = total
	}
	return start, end
}

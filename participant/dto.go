package participant

// CreateRequest is the body of POST /api/admin/participants.
type CreateRequest struct {
	// Broker code, unique across the exchange.
	Kode string `json:"kode" example:"BB" validate:"required"`
	// Broker display name.
	Nama string `json:"nama" example:"Broker B" validate:"required"`
}

// KeyRequest names the broker an admin key operation targets.
//
// The code travels in the body rather than the path so no participant identifier
// lands in access logs, proxy logs, or browser history.
type KeyRequest struct {
	Participant string `json:"participant" example:"YP" validate:"required"`
}

// ParticipantView is a broker as returned by the admin listing.
//
// It carries no API key, and no endpoint can ever add one: only a SHA-256 hash is
// stored, and a hash does not reverse. The prefix identifies which key is in place
// without exposing it.
type ParticipantView struct {
	Kode string `json:"kode" example:"YP"`
	Nama string `json:"nama" example:"Mirae Asset Sekuritas"`
	// Whether a key has been issued to this broker.
	HasAPIKey bool `json:"has_api_key" example:"true"`
	// Leading fragment of the key, for identification. Null when none is issued.
	APIKeyPrefix *string `json:"api_key_prefix,omitempty" example:"jast_YP_8fK2"`
	// When the current key was issued. Null when none is issued.
	APIKeyIssuedAt *string `json:"api_key_issued_at,omitempty" example:"2026-07-30T09:14:22Z"`
}

// IssuedKeyResponse carries a newly minted key.
//
// This is the only shape in the API that contains a plaintext key, and it is
// returned exactly once — at creation or re-issue. A lost key cannot be recovered,
// only replaced.
type IssuedKeyResponse struct {
	Kode string `json:"kode" example:"BB"`
	Nama string `json:"nama,omitempty" example:"Broker B"`
	// The full API key. Store it now; it is not retrievable later.
	APIKey string `json:"api_key" example:"jast_BB_9xQ2mR7tL4nK8wZ3yB6cF1hJ5dS0gAv2"`
}

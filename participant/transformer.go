package participant

import "time"

func ToParticipantView(rec Record) ParticipantView {
	view := ParticipantView{
		Kode:         rec.Kode,
		Nama:         rec.Nama,
		HasAPIKey:    rec.HasAPIKey(),
		APIKeyPrefix: rec.APIKeyPrefix,
	}
	if rec.APIKeyIssuedAt != nil {
		issued := rec.APIKeyIssuedAt.UTC().Format(time.RFC3339)
		view.APIKeyIssuedAt = &issued
	}
	return view
}

// Always non-nil so the field marshals as [] rather than null.
func ToParticipantViews(recs []Record) []ParticipantView {
	out := make([]ParticipantView, 0, len(recs))
	for _, rec := range recs {
		out = append(out, ToParticipantView(rec))
	}
	return out
}

func ToIssuedKeyResponse(rec Record, key string) IssuedKeyResponse {
	return IssuedKeyResponse{Kode: rec.Kode, Nama: rec.Nama, APIKey: key}
}

package market

import "context"

// MasterRepository reads the master data the market needs at startup.
//
// The interface is declared here, in the package that consumes it, and satisfied
// by the repository package. That direction keeps market free of any database
// dependency: repository imports market, never the reverse.
type MasterRepository interface {
	LoadEmiten(ctx context.Context) ([]Emiten, error)
	LoadParticipant(ctx context.Context) ([]Participant, error)
}

package marketconfig

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stubRepo records what it was asked to save and hands it straight back, so a
// test can tell what the service decided to persist without a database.
type stubRepo struct {
	stored Settings
	saved  *Settings // nil until SaveSettings is called
	err    error     // returned from SaveSettings when set
}

func (r *stubRepo) LoadSettings(context.Context) (Settings, error) {
	return r.stored, nil
}

func (r *stubRepo) SaveSettings(_ context.Context, s Settings) (Settings, error) {
	if r.err != nil {
		return Settings{}, r.err
	}
	r.saved = &s
	return s, nil
}

// newTestService returns a service whose cache already holds the defaults, the
// state the exchange runs in after a normal startup.
func newTestService() (*Service, *stubRepo) {
	repo := &stubRepo{}
	return NewService(repo, NewCache()), repo
}

func ptr(v int64) *int64 { return &v }

// The defaults are the policy this exchange ships with. They are asserted
// because they are the values in force before an operator ever calls the API,
// and a silent change to one of them changes when the market stops.
func TestDefaultsAreTheShippedPolicy(t *testing.T) {
	got := NewCache().Settings()

	if got.EmitenHaltBPS != 3000 {
		t.Errorf("emiten halt = %d bps, want 3000 (30%%)", got.EmitenHaltBPS)
	}
	if got.IndexHaltBPS != 1200 {
		t.Errorf("index halt = %d bps, want 1200 (12%%)", got.IndexHaltBPS)
	}
	if got.HaltDuration != 2*time.Minute {
		t.Errorf("halt duration = %v, want 2m", got.HaltDuration)
	}
}

// An update naming one field must leave the others alone. This is the property
// the pointer fields exist for, and the one an operator relies on when changing
// a single threshold in a live market.
func TestUpdateLeavesUnnamedFieldsAlone(t *testing.T) {
	svc, repo := newTestService()

	got, err := svc.Update(context.Background(), UpdateRequest{
		EmitenHaltBPS: ptr(2500),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if got.EmitenHaltBPS != 2500 {
		t.Errorf("emiten halt = %d, want 2500", got.EmitenHaltBPS)
	}
	if got.IndexHaltBPS != DefaultIndexHaltBPS {
		t.Errorf("index halt = %d, want it untouched at %d", got.IndexHaltBPS, DefaultIndexHaltBPS)
	}
	if got.HaltDuration != DefaultHaltDuration {
		t.Errorf("halt duration = %v, want it untouched at %v", got.HaltDuration, DefaultHaltDuration)
	}
	if got.MinPrice != DefaultMinPrice {
		t.Errorf("min price = %d, want it untouched at %d", got.MinPrice, DefaultMinPrice)
	}
	if repo.saved == nil {
		t.Fatal("nothing was persisted")
	}
}

// Seconds on the wire become a duration in the domain. The conversion is the
// one place the unit is decided, so it is worth pinning.
func TestUpdateConvertsSecondsToDuration(t *testing.T) {
	svc, _ := newTestService()

	got, err := svc.Update(context.Background(), UpdateRequest{
		HaltDurationSeconds: ptr(300),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.HaltDuration != 5*time.Minute {
		t.Errorf("halt duration = %v, want 5m", got.HaltDuration)
	}
	if view := ToConfigView(got); view.HaltDurationSeconds != 300 {
		t.Errorf("round trip through the wire gave %d seconds, want 300", view.HaltDurationSeconds)
	}
}

func TestUpdateRejectsOutOfRange(t *testing.T) {
	cases := []struct {
		name string
		req  UpdateRequest
	}{
		// Zero is the dangerous input, not merely an invalid one: a zero
		// threshold arms a breaker that trips on the first trade of the session.
		{"zero emiten threshold", UpdateRequest{EmitenHaltBPS: ptr(0)}},
		{"zero index threshold", UpdateRequest{IndexHaltBPS: ptr(0)}},
		{"zero halt duration", UpdateRequest{HaltDurationSeconds: ptr(0)}},

		{"negative emiten threshold", UpdateRequest{EmitenHaltBPS: ptr(-1)}},
		{"negative halt duration", UpdateRequest{HaltDurationSeconds: ptr(-1)}},

		// Above 100% the breaker can never trip, which reads as protection and
		// gives none.
		{"emiten threshold above 100%", UpdateRequest{EmitenHaltBPS: ptr(10_001)}},
		{"index threshold above 100%", UpdateRequest{IndexHaltBPS: ptr(10_001)}},

		{"halt longer than a day", UpdateRequest{HaltDurationSeconds: ptr(86_401)}},

		// Large enough that seconds-to-nanoseconds overflows int64 and wraps
		// negative. Caught before the multiplication, so it is reported as the
		// out-of-range value it is rather than as a mysterious negative duration.
		{"halt duration overflowing a duration", UpdateRequest{HaltDurationSeconds: ptr(1 << 62)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := newTestService()

			_, err := svc.Update(context.Background(), tc.req)

			var invalid ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("err = %v, want a ValidationError", err)
			}
			if repo.saved != nil {
				t.Error("a rejected update was persisted anyway")
			}
			if svc.Current().EmitenHaltBPS != DefaultEmitenHaltBPS {
				t.Error("a rejected update changed the cached configuration")
			}
		})
	}
}

// A failed write must leave the enforced rule where it was. Otherwise the
// exchange enforces a threshold that no stored row records, and a restart
// silently changes the policy back.
func TestFailedSaveLeavesCacheOnLastPersistedValue(t *testing.T) {
	repo := &stubRepo{err: errors.New("database down")}
	svc := NewService(repo, NewCache())

	if _, err := svc.Update(context.Background(), UpdateRequest{EmitenHaltBPS: ptr(2500)}); err == nil {
		t.Fatal("Update succeeded despite the write failing")
	}

	if got := svc.Current().EmitenHaltBPS; got != DefaultEmitenHaltBPS {
		t.Errorf("cached emiten halt = %d, want it left at %d", got, DefaultEmitenHaltBPS)
	}
}

// Load is what makes an operator's stored policy the one in force, replacing the
// built-in defaults the cache starts on.
func TestLoadReplacesDefaultsWithStoredPolicy(t *testing.T) {
	repo := &stubRepo{stored: Settings{
		MinPrice:      100,
		EmitenHaltBPS: 2000,
		IndexHaltBPS:  800,
		HaltDuration:  90 * time.Second,
	}}
	svc := NewService(repo, NewCache())

	if err := svc.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := svc.Halt()
	if got.EmitenBPS != 2000 || got.IndexBPS != 800 || got.Duration != 90*time.Second {
		t.Errorf("halt policy = %+v, want the stored one", got)
	}
}

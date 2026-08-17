// Package contract holds contract-level tests that specify an API surface
// before it exists, so a failing build is the expected "RED" state until the
// corresponding implementation lands.
//
// This file specifies the required behavior of the future
// backend-go/internal/cost package (Go package name: cost), specifically its
// Money value type (task T013a: "a fixed-scale decimal plus a required ISO
// 4217 currency, with arithmetic that carries intermediates at the rate's
// scale and exposes rounding only through an explicit Assert(scale, mode) at
// a stated boundary"). The implementer is expected to produce an API shaped
// like this:
//
//	type RoundingMode int
//
//	const (
//		RoundHalfUp RoundingMode = iota
//		RoundHalfEven
//	)
//
//	type Money struct {
//		// unexported: an exact *big.Rat amount plus a required, validated
//		// ISO 4217 currency code. The zero value is NOT a valid Money --
//		// every instance must be produced by NewMoney.
//	}
//
//	var (
//		ErrNilAmount        = errors.New("cost: amount is required")
//		ErrMissingCurrency  = errors.New("cost: currency is required")
//		ErrInvalidCurrency  = errors.New("cost: currency must be a 3-letter ISO 4217 code")
//		ErrCurrencyMismatch = errors.New("cost: currency mismatch")
//		ErrInvalidScale     = errors.New("cost: scale must be non-negative")
//	)
//
//	func NewMoney(amount *big.Rat, currency string) (Money, error)
//	func (m Money) Currency() string
//	func (m Money) Rat() *big.Rat // the exact, never-rounded internal value
//	func (m Money) Add(other Money) (Money, error)
//	func (m Money) Mul(factor *big.Rat) (Money, error)
//	func (m Money) Assert(scale int, mode RoundingMode) (Money, error)
//
// Add and Mul MUST NOT round: every intermediate is carried as an exact
// rational until Assert(scale, mode) is called, which is the ONLY place
// rounding may occur (FR-180, SC-076). Assert must honor the requested
// RoundingMode, including for exact ties (e.g. 0.125 at scale 2 rounds to
// 0.13 under RoundHalfUp but 0.12 under RoundHalfEven, because 2 is already
// even).
package contract

import (
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/truongpx396/nexus-agent/backend-go/internal/cost"
)

// mustMoney constructs a cost.Money for test setup and fails the test
// immediately if construction is rejected, so setup errors are never
// confused with the behavior under test.
func mustMoney(t *testing.T, amount *big.Rat, currency string) cost.Money {
	t.Helper()

	m, err := cost.NewMoney(amount, currency)
	if err != nil {
		t.Fatalf("NewMoney(%v, %q): %v", amount, currency, err)
	}
	return m
}

func TestNewMoney(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		amount   *big.Rat
		currency string
		wantErr  error
	}{
		{
			name:     "valid amount and currency construct cleanly",
			amount:   big.NewRat(1050, 100),
			currency: "USD",
			wantErr:  nil,
		},
		{
			name:     "empty currency is rejected, never silently zeroed",
			amount:   big.NewRat(100, 1),
			currency: "",
			wantErr:  cost.ErrMissingCurrency,
		},
		{
			name:     "whitespace-only currency is rejected",
			amount:   big.NewRat(100, 1),
			currency: "   ",
			wantErr:  cost.ErrMissingCurrency,
		},
		{
			name:     "lowercase currency code is rejected",
			amount:   big.NewRat(100, 1),
			currency: "usd",
			wantErr:  cost.ErrInvalidCurrency,
		},
		{
			name:     "wrong-length currency code is rejected",
			amount:   big.NewRat(100, 1),
			currency: "US",
			wantErr:  cost.ErrInvalidCurrency,
		},
		{
			name:     "nil amount is rejected",
			amount:   nil,
			currency: "USD",
			wantErr:  cost.ErrNilAmount,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := cost.NewMoney(tc.amount, tc.currency)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("NewMoney(%v, %q) error = %v, want errors.Is match for %v", tc.amount, tc.currency, err, tc.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("NewMoney(%v, %q) unexpected error: %v", tc.amount, tc.currency, err)
			}
			if got.Currency() != tc.currency {
				t.Errorf("Currency() = %q, want %q", got.Currency(), tc.currency)
			}
		})
	}
}

func TestMoney_Add_PreservesFullPrecisionUntilAssert(t *testing.T) {
	t.Run("summing three exact thirds recovers a whole unit only if unrounded until Assert", func(t *testing.T) {
		third := mustMoney(t, big.NewRat(1, 3), "USD")

		sum := third
		var err error
		for i := 0; i < 2; i++ {
			sum, err = sum.Add(third)
			if err != nil {
				t.Fatalf("Add: %v", err)
			}
		}

		// If Add rounded each intermediate to, say, 2 decimal places
		// (0.33 + 0.33 + 0.33 = 0.99), this would fail. Money must carry
		// the exact rational sum (1/3 + 1/3 + 1/3 == 1 exactly) and defer
		// any rounding to Assert.
		asserted, err := sum.Assert(2, cost.RoundHalfEven)
		if err != nil {
			t.Fatalf("Assert: %v", err)
		}

		want := big.NewRat(1, 1)
		if asserted.Rat().Cmp(want) != 0 {
			t.Errorf("Assert(2, RoundHalfEven).Rat() = %s, want %s (exact 1.00 -- proves no intermediate rounding occurred in Add)", asserted.Rat().RatString(), want.RatString())
		}
	})

	t.Run("a rate multiplied across many additions is not truncated before Assert", func(t *testing.T) {
		// Models "carries intermediates at the rate's scale": a rate with
		// more precision than the final asserted scale (e.g. a
		// numeric(20,10) price-book rate) must not be truncated to a
		// coarser scale before the terms it prices are summed.
		rate := big.NewRat(1, 7) // a rate that does not terminate in decimal
		unit := mustMoney(t, big.NewRat(1, 1), "USD")

		priced, err := unit.Mul(rate)
		if err != nil {
			t.Fatalf("Mul: %v", err)
		}

		sum := priced
		for i := 0; i < 6; i++ {
			sum, err = sum.Add(priced)
			if err != nil {
				t.Fatalf("Add: %v", err)
			}
		}

		// 7 * (1/7) == 1 exactly, but only if none of the seven additions
		// rounded the 1/7 rate down to a finite decimal first.
		asserted, err := sum.Assert(6, cost.RoundHalfEven)
		if err != nil {
			t.Fatalf("Assert: %v", err)
		}
		want := big.NewRat(1, 1)
		if asserted.Rat().Cmp(want) != 0 {
			t.Errorf("summed rate*7 asserted to scale 6 = %s, want %s (proves Mul/Add carry the rate's full precision until Assert)", asserted.Rat().RatString(), want.RatString())
		}
	})
}

func TestMoney_Add_CurrencyMismatchIsError(t *testing.T) {
	usd := mustMoney(t, big.NewRat(500, 100), "USD")
	eur := mustMoney(t, big.NewRat(500, 100), "EUR")

	_, err := usd.Add(eur)
	if err == nil {
		t.Fatal("Add(USD, EUR) returned a nil error, want a currency-mismatch error -- currencies must never be silently coerced")
	}
	if !errors.Is(err, cost.ErrCurrencyMismatch) {
		t.Fatalf("Add(USD, EUR) error = %v, want errors.Is match for cost.ErrCurrencyMismatch", err)
	}
}

func TestMoney_Assert_RoundingModeIsHonored(t *testing.T) {
	cases := []struct {
		name   string
		amount *big.Rat
		scale  int
		mode   cost.RoundingMode
		want   *big.Rat
	}{
		{
			name:   "round-half-up rounds an exact tie away from zero",
			amount: big.NewRat(125, 1000), // 0.125
			scale:  2,
			mode:   cost.RoundHalfUp,
			want:   big.NewRat(13, 100), // 0.13
		},
		{
			name:   "round-half-even rounds the same exact tie down to the even digit",
			amount: big.NewRat(125, 1000), // 0.125
			scale:  2,
			mode:   cost.RoundHalfEven,
			want:   big.NewRat(12, 100), // 0.12 (2 is already even)
		},
		{
			name:   "round-half-up rounds a second exact tie away from zero",
			amount: big.NewRat(145, 1000), // 0.145
			scale:  2,
			mode:   cost.RoundHalfUp,
			want:   big.NewRat(15, 100), // 0.15
		},
		{
			name:   "round-half-even rounds the second exact tie up to the even digit",
			amount: big.NewRat(145, 1000), // 0.145
			scale:  2,
			mode:   cost.RoundHalfEven,
			want:   big.NewRat(14, 100), // 0.14 (4 is already even)
		},
		{
			name:   "a non-tie value rounds identically under both modes",
			amount: big.NewRat(126, 1000), // 0.126
			scale:  2,
			mode:   cost.RoundHalfUp,
			want:   big.NewRat(13, 100), // 0.13, unambiguous
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := mustMoney(t, tc.amount, "USD")

			got, err := m.Assert(tc.scale, tc.mode)
			if err != nil {
				t.Fatalf("Assert(%d, %v): %v", tc.scale, tc.mode, err)
			}
			if got.Rat().Cmp(tc.want) != 0 {
				t.Errorf("Assert(%d, %v).Rat() = %s, want %s", tc.scale, tc.mode, got.Rat().RatString(), tc.want.RatString())
			}
			if got.Currency() != "USD" {
				t.Errorf("Assert must preserve currency: Currency() = %q, want USD", got.Currency())
			}
		})
	}
}

func TestMoney_Assert_RejectsNegativeScale(t *testing.T) {
	m := mustMoney(t, big.NewRat(100, 1), "USD")

	_, err := m.Assert(-1, cost.RoundHalfUp)
	if err == nil {
		t.Fatal("Assert(-1, RoundHalfUp) returned a nil error, want a scale-validation error")
	}
	if !errors.Is(err, cost.ErrInvalidScale) {
		t.Fatalf("Assert(-1, RoundHalfUp) error = %v, want errors.Is match for cost.ErrInvalidScale", err)
	}
}

// TestCost_NoFloatInMonetaryCode is the CI guard required by T013a: it fails
// the build if float32/float64 shows up in a context that suggests a
// monetary, rate, ledger, or reservation field anywhere under
// backend-go/internal/cost or backend-go/migrations. It is a real
// filesystem walk plus a regex heuristic, not a policy document.
//
// This test can legitimately PASS today even though internal/cost and
// migrations are empty or missing -- it is a guard against a future
// regression, not a RED test for missing implementation. Money's own
// exactness is proven by the other tests in this file.
func TestCost_NoFloatInMonetaryCode(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to resolve this test file's own path via runtime.Caller")
	}

	// backend-go/tests/contract -> backend-go
	backendGoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))

	targets := []string{
		filepath.Join(backendGoRoot, "internal", "cost"),
		filepath.Join(backendGoRoot, "migrations"),
	}

	floatToken := regexp.MustCompile(`\bfloat(32|64)\b`)
	moneyContext := regexp.MustCompile(`(?i)\b(amount|rate|price|cost|ledger|reservation|budget|ceiling|balance|currency|invoice|charge|spend|credit|payment|fee|fx)\b`)

	var violations []string

	for _, dir := range targets {
		info, statErr := os.Stat(dir)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				// Nothing to scan yet: not a violation, just an
				// implementation that has not landed. The guard exists to
				// catch a future regression, not to gate on presence.
				continue
			}
			t.Fatalf("stat %s: %v", dir, statErr)
		}
		if !info.IsDir() {
			continue
		}

		walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return fmt.Errorf("walking %s: %w", path, err)
			}
			if d.IsDir() {
				return nil
			}

			ext := filepath.Ext(path)
			if ext != ".go" && ext != ".sql" {
				return nil
			}

			contents, readErr := os.ReadFile(path)
			if readErr != nil {
				return fmt.Errorf("reading %s: %w", path, readErr)
			}

			for lineNum, line := range strings.Split(string(contents), "\n") {
				if floatToken.MatchString(line) && moneyContext.MatchString(line) {
					violations = append(violations, fmt.Sprintf("%s:%d: %s", path, lineNum+1, strings.TrimSpace(line)))
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walking %s: %v", dir, walkErr)
		}
	}

	if len(violations) > 0 {
		t.Errorf("found float32/float64 used in an apparent monetary/rate/ledger/reservation field -- money must be an exact fixed-scale type with a currency, never a float:\n%s", strings.Join(violations, "\n"))
	}
}

// Package cost implements the exact-decimal Money value type (T013a) and
// declares the BudgetGate pre-spend reservation seam (T016a). No monetary,
// rate, ledger, or reservation value in this package is ever represented as
// a Go floating-point type -- only *big.Rat, int64, and int are used, so
// intermediate arithmetic never silently loses precision (FR-180, SC-076).
package cost

import (
	"errors"
	"math/big"
	"strings"
)

var (
	// ErrNilAmount is returned by NewMoney when the supplied amount is nil.
	ErrNilAmount = errors.New("cost: amount is required")
	// ErrMissingCurrency is returned by NewMoney when the supplied currency
	// is empty or contains only whitespace.
	ErrMissingCurrency = errors.New("cost: currency is required")
	// ErrInvalidCurrency is returned by NewMoney when the supplied currency
	// is not exactly three uppercase ASCII letters.
	ErrInvalidCurrency = errors.New("cost: currency must be a 3-letter ISO 4217 code")
	// ErrCurrencyMismatch is returned by Money.Add when the two operands
	// carry different currencies.
	ErrCurrencyMismatch = errors.New("cost: currency mismatch")
	// ErrInvalidScale is returned by Money.Assert when scale is negative.
	ErrInvalidScale = errors.New("cost: scale must be non-negative")
)

// RoundingMode selects the tie-breaking rule Money.Assert applies when a
// scaled value falls exactly halfway between two representable digits at
// the target scale.
type RoundingMode int

const (
	// RoundHalfUp rounds an exact tie away from zero.
	RoundHalfUp RoundingMode = iota
	// RoundHalfEven rounds an exact tie to whichever neighboring digit at
	// the target scale is even (aka "banker's rounding").
	RoundHalfEven
)

// Money is a fixed-scale decimal value plus a required ISO 4217 currency
// code. The zero value is NOT valid -- every instance must be produced by
// NewMoney. Money holds its amount internally as an exact *big.Rat so
// arithmetic (Add, Mul) never rounds; rounding happens only when Assert is
// called explicitly, at a stated scale and mode (FR-180, SC-076).
type Money struct {
	amount   *big.Rat
	currency string
}

// isUpperASCIILetters reports whether s consists only of uppercase ASCII
// letters A-Z.
func isUpperASCIILetters(s string) bool {
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// NewMoney constructs a Money from an exact amount and a required ISO 4217
// currency code. It rejects a nil amount (ErrNilAmount), an empty or
// whitespace-only currency (ErrMissingCurrency), and any currency that is
// not exactly three uppercase ASCII letters (ErrInvalidCurrency). On
// success, Currency() returns exactly the string passed in.
func NewMoney(amount *big.Rat, currency string) (Money, error) {
	if amount == nil {
		return Money{}, ErrNilAmount
	}
	if strings.TrimSpace(currency) == "" {
		return Money{}, ErrMissingCurrency
	}
	if len(currency) != 3 || !isUpperASCIILetters(currency) {
		return Money{}, ErrInvalidCurrency
	}

	return Money{
		amount:   new(big.Rat).Set(amount),
		currency: currency,
	}, nil
}

// Currency returns the ISO 4217 currency code m was constructed with.
func (m Money) Currency() string {
	return m.currency
}

// Rat returns a copy of m's exact, never-rounded internal rational value.
// The copy is defensive: Money is an immutable value type, so handing out
// the internal *big.Rat would let any caller mutate m's amount in place
// (m.Rat().SetInt64(...) previously did exactly that). Mutating the
// returned *big.Rat is therefore harmless -- it cannot affect m.
//
// A zero-value Money (never produced by NewMoney) has a nil internal
// amount; Rat returns nil for it, preserving the prior nil passthrough.
func (m Money) Rat() *big.Rat {
	if m.amount == nil {
		return nil
	}
	return new(big.Rat).Set(m.amount)
}

// Add returns a new Money holding the exact sum of m and other, carried as
// an unrounded rational -- no intermediate rounding occurs. Add fails with
// ErrNilAmount if either operand is the zero value (a zero-value Money has
// a nil internal amount, which two zero-value operands' matching empty
// currencies would otherwise let slip past the currency check into a
// math/big nil-pointer panic), or ErrCurrencyMismatch if the two operands'
// currencies differ; currencies are never silently coerced.
func (m Money) Add(other Money) (Money, error) {
	if m.amount == nil || other.amount == nil {
		return Money{}, ErrNilAmount
	}
	if other.currency != m.currency {
		return Money{}, ErrCurrencyMismatch
	}
	sum := new(big.Rat).Add(m.amount, other.amount)
	return Money{amount: sum, currency: m.currency}, nil
}

// Mul returns a new Money holding the exact product of m's amount and
// factor, carried as an unrounded rational. The currency is unchanged.
// Mul fails with ErrNilAmount if m is the zero value or factor is nil.
func (m Money) Mul(factor *big.Rat) (Money, error) {
	if m.amount == nil || factor == nil {
		return Money{}, ErrNilAmount
	}
	product := new(big.Rat).Mul(m.amount, factor)
	return Money{amount: product, currency: m.currency}, nil
}

// Assert rounds m's exact amount to scale decimal digits using mode and
// returns a new Money holding that rounded value, with the same currency
// preserved. Assert is the ONLY place a Money value is ever rounded
// (FR-180, SC-076). It returns ErrNilAmount if m is the zero value, or
// ErrInvalidScale if scale is negative.
//
// RoundHalfUp breaks an exact tie away from zero (0.125 at scale 2 ->
// 0.13). RoundHalfEven breaks the same exact tie toward whichever
// neighboring digit at the target scale is even (0.125 at scale 2 -> 0.12,
// because 2 is even). Non-tie values round identically under both modes.
func (m Money) Assert(scale int, mode RoundingMode) (Money, error) {
	if m.amount == nil {
		return Money{}, ErrNilAmount
	}
	if scale < 0 {
		return Money{}, ErrInvalidScale
	}

	pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	scaled := new(big.Rat).Mul(m.amount, new(big.Rat).SetInt(pow))

	num := scaled.Num()   // signed
	den := scaled.Denom() // always > 0

	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	// QuoRem truncates toward zero: q is the truncated quotient, r shares
	// num's sign (or is zero).

	sign := int64(1)
	if num.Sign() < 0 {
		sign = -1
	}

	absR := new(big.Int).Abs(r)
	twiceAbsR := new(big.Int).Lsh(absR, 1)

	switch twiceAbsR.Cmp(den) {
	case -1:
		// Below the halfway point: truncated quotient is already nearest.
	case 1:
		// Above the halfway point: round away from zero.
		q.Add(q, big.NewInt(sign))
	default:
		// Exact tie: apply the requested tie-breaking rule.
		switch mode {
		case RoundHalfUp:
			q.Add(q, big.NewInt(sign))
		case RoundHalfEven:
			if q.Bit(0) == 1 {
				// q is odd; its away-from-zero neighbor is the even one.
				q.Add(q, big.NewInt(sign))
			}
		}
	}

	result := new(big.Rat).SetFrac(q, pow)
	return Money{amount: result, currency: m.currency}, nil
}

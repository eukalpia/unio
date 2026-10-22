package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// MaxEndpoints bounds how many drive endpoints a configuration may expand to.
const MaxEndpoints = 4096

// maxPatternLength bounds the length of one endpoint pattern.
const maxPatternLength = 4096

// maxRangeDigits bounds the digits of each range bound, so parsing cannot overflow.
const maxRangeDigits = 9

// segment is one piece of a parsed pattern: either literal text or a numeric range.
type segment struct {
	literal string
	isRange bool
	start   int
	count   int
	width   int // zero-padding width; 0 means no padding
}

// ExpandPattern expands MinIO-style ellipses in pattern. A range {a...b} is
// an inclusive decimal range. Several ranges multiply out, and the leftmost
// range varies slowest, so https://n{1...2}:9100/d{1...2} yields n1/d1,
// n1/d2, n2/d1 and n2/d2. A range whose start has a leading zero is
// zero-padded to the width of the start: {01...03} gives 01, 02 and 03.
//
// Braces must form well-formed ranges. Nested or unclosed braces, stray
// closing braces, non-numeric bounds, reversed ranges and expansions larger
// than MaxEndpoints are rejected. A pattern without braces expands to itself.
func ExpandPattern(pattern string) ([]string, error) {
	if len(pattern) > maxPatternLength {
		return nil, fmt.Errorf("pattern is longer than %d characters", maxPatternLength)
	}
	segs, total, err := parsePattern(pattern)
	if err != nil {
		return nil, fmt.Errorf("pattern %q: %w", pattern, err)
	}
	out := make([]string, 0, total)
	for n := range total {
		out = append(out, render(segs, n))
	}
	return out, nil
}

// parsePattern splits pattern into segments and returns the number of
// expansions it produces.
func parsePattern(pattern string) ([]segment, int, error) {
	var segs []segment
	var lit strings.Builder
	total := 1
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '}':
			return nil, 0, fmt.Errorf("stray '}' at position %d", i)
		case '{':
			end := strings.IndexAny(pattern[i+1:], "{}")
			if end < 0 || pattern[i+1+end] != '}' {
				return nil, 0, fmt.Errorf("unclosed '{' at position %d", i)
			}
			seg, err := parseRange(pattern[i+1 : i+1+end])
			if err != nil {
				return nil, 0, fmt.Errorf("range at position %d: %w", i, err)
			}
			if total > MaxEndpoints/seg.count {
				return nil, 0, fmt.Errorf("pattern expands to more than %d endpoints", MaxEndpoints)
			}
			total *= seg.count
			if lit.Len() > 0 {
				segs = append(segs, segment{literal: lit.String()})
				lit.Reset()
			}
			segs = append(segs, seg)
			i += end + 1
		default:
			lit.WriteByte(pattern[i])
		}
	}
	if lit.Len() > 0 || len(segs) == 0 {
		segs = append(segs, segment{literal: lit.String()})
	}
	return segs, total, nil
}

// parseRange parses the body of a range, the text between { and }.
func parseRange(body string) (segment, error) {
	lo, hi, ok := strings.Cut(body, "...")
	if !ok {
		return segment{}, fmt.Errorf("{%s} must have the form {start...end}", body)
	}
	if !isDigits(lo) || !isDigits(hi) {
		return segment{}, fmt.Errorf("{%s} bounds must be decimal numbers", body)
	}
	start, err := strconv.Atoi(lo)
	if err != nil {
		return segment{}, fmt.Errorf("{%s}: %w", body, err)
	}
	end, err := strconv.Atoi(hi)
	if err != nil {
		return segment{}, fmt.Errorf("{%s}: %w", body, err)
	}
	if start > end {
		return segment{}, fmt.Errorf("{%s} is reversed: start %d is greater than end %d", body, start, end)
	}
	width := 0
	if len(lo) > 1 && lo[0] == '0' {
		width = len(lo)
	}
	return segment{isRange: true, start: start, count: end - start + 1, width: width}, nil
}

// isDigits reports whether s is a non-empty run of at most maxRangeDigits ASCII digits.
func isDigits(s string) bool {
	if s == "" || len(s) > maxRangeDigits {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// render builds the n-th expansion (0-based) of segs. Ranges are decoded as
// a mixed-radix number whose leftmost digit is the most significant.
func render(segs []segment, n int) string {
	vals := make([]int, len(segs))
	rem := n
	for i := len(segs) - 1; i >= 0; i-- {
		if !segs[i].isRange {
			continue
		}
		vals[i] = rem % segs[i].count
		rem /= segs[i].count
	}
	var b strings.Builder
	for i, s := range segs {
		if !s.isRange {
			b.WriteString(s.literal)
			continue
		}
		num := strconv.Itoa(s.start + vals[i])
		for pad := len(num); pad < s.width; pad++ {
			b.WriteByte('0')
		}
		b.WriteString(num)
	}
	return b.String()
}

// ExpandEndpoints expands every pattern and parses the results into
// endpoints. It reports every problem it finds, not just the first. Duplicate
// endpoints across all patterns are rejected, and the total is capped at
// MaxEndpoints.
func ExpandEndpoints(patterns []string) ([]Endpoint, error) {
	var errs []error
	var out []Endpoint
	seen := make(map[string]struct{})
	capped := false
	for _, p := range patterns {
		raws, err := ExpandPattern(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, raw := range raws {
			ep, err := ParseEndpoint(raw)
			if err != nil {
				errs = append(errs, fmt.Errorf("pattern %q: %w", p, err))
				continue
			}
			key := ep.String()
			if _, dup := seen[key]; dup {
				errs = append(errs, fmt.Errorf("duplicate endpoint %s (from pattern %q)", key, p))
				continue
			}
			if len(out) >= MaxEndpoints {
				if !capped {
					errs = append(errs, fmt.Errorf("more than %d endpoints configured", MaxEndpoints))
					capped = true
				}
				continue
			}
			seen[key] = struct{}{}
			out = append(out, ep)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

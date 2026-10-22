package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Size is a byte count. All units are binary: 1 KB, 1 KiB and 1 K are 1024
// bytes, and the same holds for M, G and T.
type Size int64

const (
	kiB Size = 1 << 10
	miB Size = 1 << 20
	giB Size = 1 << 30
	tiB Size = 1 << 40
)

// sizeUnits maps lower-case unit suffixes to their multipliers.
var sizeUnits = map[string]Size{
	"":    1,
	"b":   1,
	"k":   kiB,
	"kb":  kiB,
	"kib": kiB,
	"m":   miB,
	"mb":  miB,
	"mib": miB,
	"g":   giB,
	"gb":  giB,
	"gib": giB,
	"t":   tiB,
	"tb":  tiB,
	"tib": tiB,
}

// ParseSize parses a non-negative integer with an optional unit, for example
// "4096", "4KiB", "64 MiB" or "1g". Fractions and signs are rejected.
func ParseSize(s string) (Size, error) {
	trimmed := strings.TrimSpace(s)
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	digits, unit := trimmed[:i], strings.ToLower(strings.TrimSpace(trimmed[i:]))
	if digits == "" {
		return 0, fmt.Errorf("invalid size %q: expected a number such as 4KiB or 1GiB", s)
	}
	mult, ok := sizeUnits[unit]
	if !ok {
		return 0, fmt.Errorf("invalid size %q: unknown unit %q (use B, KiB, MiB, GiB or TiB)", s, unit)
	}
	n, err := strconv.ParseUint(digits, 10, 63)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", s, err)
	}
	if n > uint64(math.MaxInt64)/uint64(mult) {
		return 0, fmt.Errorf("invalid size %q: value overflows", s)
	}
	return Size(n) * mult, nil
}

// String formats the size with the largest exact binary unit, for example
// "4KiB" or "1GiB". Values that are not an exact multiple of KiB are shown
// in bytes.
func (s Size) String() string {
	switch {
	case s != 0 && s%tiB == 0:
		return fmt.Sprintf("%dTiB", s/tiB)
	case s != 0 && s%giB == 0:
		return fmt.Sprintf("%dGiB", s/giB)
	case s != 0 && s%miB == 0:
		return fmt.Sprintf("%dMiB", s/miB)
	case s != 0 && s%kiB == 0:
		return fmt.Sprintf("%dKiB", s/kiB)
	default:
		return strconv.FormatInt(int64(s), 10)
	}
}

// MarshalYAML writes the size in its canonical form.
func (s Size) MarshalYAML() (any, error) { return s.String(), nil }

// UnmarshalYAML reads a size from a YAML scalar such as 4KiB or 4096.
func (s *Size) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: size must be a scalar such as 4KiB", node.Line)
	}
	v, err := ParseSize(node.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", node.Line, err)
	}
	*s = v
	return nil
}

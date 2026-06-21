package utils

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var customDurationUnits = map[string]time.Duration{
	"ns": time.Nanosecond,
	"us": time.Microsecond,
	"µs": time.Microsecond,
	"ms": time.Millisecond,
	"s":  time.Second,
	"m":  time.Minute,
	"h":  time.Hour,
	"d":  24 * time.Hour,
	"w":  7 * 24 * time.Hour,
}

// ParseDuration parses Go duration strings and also supports day/week units (d, w).
// Examples: "30s", "12h", "7d", "2w", "1w2d3h".
func ParseDuration(input string) (time.Duration, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return 0, fmt.Errorf("invalid duration %q", input)
	}

	if d, err := time.ParseDuration(input); err == nil {
		return d, nil
	}

	sign := 1.0
	if strings.HasPrefix(input, "-") {
		sign = -1
		input = input[1:]
	} else if strings.HasPrefix(input, "+") {
		input = input[1:]
	}

	if input == "" {
		return 0, fmt.Errorf("invalid duration %q", input)
	}

	var total float64
	for len(input) > 0 {
		numEnd := 0
		dotSeen := false
		for numEnd < len(input) {
			ch := input[numEnd]
			if ch >= '0' && ch <= '9' {
				numEnd++
				continue
			}
			if ch == '.' && !dotSeen {
				dotSeen = true
				numEnd++
				continue
			}
			break
		}

		if numEnd == 0 {
			return 0, fmt.Errorf("invalid duration %q", input)
		}

		value, err := strconv.ParseFloat(input[:numEnd], 64)
		if err != nil {
			return 0, err
		}
		input = input[numEnd:]

		unitEnd := 0
		for unitEnd < len(input) {
			ch := input[unitEnd]
			if (ch >= 'a' && ch <= 'z') || ch == 'µ' {
				unitEnd++
				continue
			}
			break
		}

		if unitEnd == 0 {
			return 0, fmt.Errorf("missing duration unit in %q", input)
		}

		unit := input[:unitEnd]
		mult, ok := customDurationUnits[unit]
		if !ok {
			return 0, fmt.Errorf("unknown duration unit %q", unit)
		}

		total += value * float64(mult)
		input = input[unitEnd:]
	}

	return time.Duration(sign * total), nil
}

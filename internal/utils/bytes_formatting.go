package utils

import "fmt"

func FormatBytesSI(n int64) string {
	const unit = int64(1000)
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	div := float64(unit)
	exp := 0
	for v := n / unit; v >= unit && exp < 2; v /= unit {
		div *= float64(unit)
		exp++
	}

	value := float64(n) / div
	suffixes := []string{"kB", "MB", "GB"}
	return fmt.Sprintf("%.2f %s", value, suffixes[exp])
}

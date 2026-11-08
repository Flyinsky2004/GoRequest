package desktop

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Display formatting only: stored measurements and exported reports retain
// their original units and precision.
func formatDuration(d time.Duration) string {
	return formatDurationMs(float64(d) / float64(time.Millisecond))
}

func formatDurationMs(ms float64) string {
	if math.IsNaN(ms) || math.IsInf(ms, 0) {
		return "—"
	}
	if ms <= 0 {
		return "0ms"
	}
	if ms < 0.005 {
		return "<0.01ms"
	}
	// Promote values that would round to the next unit, too.
	if math.Round(ms*100)/100 < 1000 {
		return compactNumber(ms) + "ms"
	}
	seconds := ms / 1000
	if math.Round(seconds*100)/100 < 60 {
		return compactNumber(seconds) + "s"
	}
	// Long durations read naturally as 1m 30s or 2h 5m rather than decimals.
	seconds = math.Round(seconds)
	hours := math.Floor(seconds / 3600)
	minutes := math.Floor(math.Mod(seconds, 3600) / 60)
	remaining := math.Mod(seconds, 60)
	var parts []string
	if hours > 0 {
		parts = append(parts, strconv.FormatFloat(hours, 'f', 0, 64)+"h")
	}
	if minutes > 0 {
		parts = append(parts, strconv.FormatFloat(minutes, 'f', 0, 64)+"m")
	}
	if remaining > 0 {
		parts = append(parts, strconv.FormatFloat(remaining, 'f', 0, 64)+"s")
	}
	return strings.Join(parts, " ")
}

func formatByteSize(n int64) string {
	if n < 0 {
		return "—" // A negative content length means unknown, not zero bytes.
	}
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := [...]string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	value, unit := float64(n), 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if math.Round(value*100)/100 >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	return compactNumber(value) + " " + units[unit]
}

func formatDurationDeltaMs(ms float64) string {
	if math.IsNaN(ms) || math.IsInf(ms, 0) {
		return "—"
	}
	if ms < 0 {
		return "-" + formatDurationMs(-ms)
	}
	if ms > 0 {
		return "+" + formatDurationMs(ms)
	}
	return "0ms"
}

func compactNumber(value float64) string {
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(value, 'f', 2, 64), "0"), ".")
}

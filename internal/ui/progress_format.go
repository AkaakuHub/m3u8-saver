package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
)

func formatProgressLine(entry *progressEntry, terminalWidth int) string {
	current := entry.current
	total := entry.total
	if total <= 0 {
		total = 1
	}

	percent := float64(current) * 100 / float64(total)
	if percent > 100 {
		percent = 100
	}

	eta := etaValue(entry, current, total)
	leftText := fmt.Sprintf("%s %s / %s ", entry.label, formatBytes(current), formatBytes(total))
	rightText := fmt.Sprintf(" %.0f %% %s", percent, eta)
	barWidth := calculateBarWidth(terminalWidth, leftText, rightText)
	filled := int(percent / 100 * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	bar := buildProgressBar(barWidth, filled, current >= total)

	return fmt.Sprintf(
		"%s %s / %s [%s] %.0f %% %s",
		entry.label,
		formatBytes(current),
		formatBytes(total),
		bar,
		percent,
		eta,
	)
}

func calculateBarWidth(terminalWidth int, leftText, rightText string) int {
	const minimumBarWidth = 12
	if terminalWidth <= 0 {
		return 40
	}

	reservedWidth := len(leftText) + len(rightText) + 2
	barWidth := terminalWidth - reservedWidth
	if barWidth < minimumBarWidth {
		return minimumBarWidth
	}
	return barWidth
}

func etaValue(entry *progressEntry, current, total int64) string {
	if current >= total {
		return "0s"
	}
	if current <= 0 {
		return "?"
	}

	elapsed := time.Since(entry.startedAt)
	remainingBytes := total - current
	remainingDuration := time.Duration(float64(elapsed) * (float64(remainingBytes) / float64(current)))
	return roundDuration(remainingDuration)
}

func buildProgressBar(width, filled int, completed bool) string {
	if completed {
		return strings.Repeat("=", width)
	}
	if filled <= 0 {
		return ">" + strings.Repeat("-", width-1)
	}
	if filled >= width {
		return strings.Repeat("=", width)
	}

	return strings.Repeat("=", filled-1) + ">" + strings.Repeat("-", width-filled)
}

func roundDuration(value time.Duration) string {
	if value < time.Second {
		return "0s"
	}
	if value < time.Minute {
		return fmt.Sprintf("%ds", int(value.Round(time.Second)/time.Second))
	}
	if value < time.Hour {
		minutes := int(value / time.Minute)
		seconds := int((value % time.Minute).Round(time.Second) / time.Second)
		if seconds == 60 {
			minutes++
			seconds = 0
		}
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}

	hours := int(value / time.Hour)
	minutes := int((value % time.Hour) / time.Minute)
	return fmt.Sprintf("%dh%dm", hours, minutes)
}

func removeEntryByID(entries []*progressEntry, entryID int) []*progressEntry {
	result := entries[:0]
	for _, entry := range entries {
		if entry.id != entryID {
			result = append(result, entry)
		}
	}
	return result
}

func removeEntryByLabel(entries []*progressEntry, label string) []*progressEntry {
	result := entries[:0]
	removed := false
	for _, entry := range entries {
		if !removed && entry.label == label {
			removed = true
			continue
		}
		result = append(result, entry)
	}
	return result
}

func formatBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}

	divisor := int64(unit)
	suffix := "KiB"
	for _, currentSuffix := range []string{"MiB", "GiB", "TiB"} {
		if value < divisor*unit {
			break
		}
		divisor *= unit
		suffix = currentSuffix
	}

	return fmt.Sprintf("%.1f %s", float64(value)/float64(divisor), suffix)
}

func isTerminalWriter(output io.Writer) bool {
	file, ok := output.(*os.File)
	if !ok {
		return false
	}

	return isatty.IsTerminal(file.Fd()) || isatty.IsCygwinTerminal(file.Fd())
}

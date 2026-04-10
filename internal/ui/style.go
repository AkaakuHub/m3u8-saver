package ui

import (
	"fmt"

	"github.com/fatih/color"
)

var (
	progressColor = color.New(color.FgBlue)
	successColor  = color.New(color.FgGreen)
	failedColor   = color.New(color.FgRed)
	skippedColor  = color.New(color.FgYellow)
)

func SuccessLabel(date, status string) string {
	return successColor.Sprintf("%s %s", date, status)
}

func SkippedLabel(date, status string) string {
	return skippedColor.Sprintf("%s %s", date, status)
}

func FailedLabel(date string, err error) string {
	return failedColor.Sprintf("%s failed:", date) + " " + err.Error()
}

func ProgressLine(prefix string, processed, total, succeeded, failed, skipped int) string {
	return fmt.Sprintf(
		"%s processed=%d/%d %s %s %s",
		progressColor.Sprintf("%s", prefix),
		processed,
		total,
		successColor.Sprintf("success=%d", succeeded),
		failedColor.Sprintf("failed=%d", failed),
		skippedColor.Sprintf("skipped=%d", skipped),
	)
}

package ui

import (
	"fmt"
	"io"
	"os"

	"github.com/fatih/color"
	"github.com/mattn/go-isatty"
)

var (
	progressColor = color.New(color.FgBlue)
	successColor  = color.New(color.FgGreen)
	failedColor   = color.New(color.FgRed)
	archivedColor = color.New(color.FgCyan)
	missingColor  = color.New(color.FgYellow)
)

func ConfigureColor(output io.Writer) {
	file, ok := output.(*os.File)
	if !ok {
		color.NoColor = true
		return
	}

	color.NoColor = !isatty.IsTerminal(file.Fd()) && !isatty.IsCygwinTerminal(file.Fd())
}

func SuccessLabel(date, status string) string {
	return successColor.Sprintf("%s %s", date, status)
}

func ArchivedLabel(date, status string) string {
	return archivedColor.Sprintf("%s %s", date, status)
}

func MissingLabel(date, status string) string {
	return missingColor.Sprintf("%s %s", date, status)
}

func IncompleteLabel(date, status string) string {
	return missingColor.Sprintf("%s %s", date, status)
}

func FailedLabel(date string, err error) string {
	return failedColor.Sprintf("%s failed:", date) + " " + err.Error()
}

func InventorySummaryLine(archived, scanned int) string {
	return fmt.Sprintf(
		"%s %s scanned=%d",
		progressColor.Sprintf("completed"),
		archivedColor.Sprintf("archived=%d", archived),
		scanned,
	)
}

func ProgressLine(prefix string, processed, total, succeeded, failed, archived, missing int) string {
	return fmt.Sprintf(
		"%s processed=%d/%d %s %s %s %s",
		progressColor.Sprintf("%s", prefix),
		processed,
		total,
		successColor.Sprintf("success=%d", succeeded),
		failedColor.Sprintf("failed=%d", failed),
		archivedColor.Sprintf("archived=%d", archived),
		missingColor.Sprintf("missing=%d", missing),
	)
}

func PlainProgressLine(prefix string, processed, total, succeeded, failed, archived, missing int) string {
	return fmt.Sprintf(
		"%s processed=%d/%d success=%d failed=%d archived=%d missing=%d",
		prefix,
		processed,
		total,
		succeeded,
		failed,
		archived,
		missing,
	)
}

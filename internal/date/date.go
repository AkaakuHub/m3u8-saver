package date

import (
	"fmt"
	"time"
)

const layout = "20060102"

func Count(start, end string) (int, error) {
	startTime, endTime, err := parseRange(start, end)
	if err != nil {
		return 0, err
	}

	return int(endTime.Sub(startTime).Hours()/24) + 1, nil
}

func Each(start, end string, visit func(string) error) error {
	startTime, endTime, err := parseRange(start, end)
	if err != nil {
		return err
	}

	for current := startTime; !current.After(endTime); current = current.AddDate(0, 0, 1) {
		if err := visit(current.Format(layout)); err != nil {
			return err
		}
	}

	return nil
}

func parseRange(start, end string) (time.Time, time.Time, error) {
	startTime, err := time.Parse(layout, start)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("failed to parse startDate: %w", err)
	}

	endTime, err := time.Parse(layout, end)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("failed to parse endDate: %w", err)
	}

	if startTime.After(endTime) {
		return time.Time{}, time.Time{}, fmt.Errorf("startDate must be less than or equal to endDate")
	}

	return startTime, endTime, nil
}

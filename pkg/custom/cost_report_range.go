package custom

import (
	"fmt"
	"time"
	_ "time/tzdata" // Keep IANA date boundaries available on supported platforms.
)

type costReportRange struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Timezone  string `json:"timezone"`
	StartTime int64  `json:"start_time"`
	EndTime   int64  `json:"end_time"`
}

func parseCostReportRange(from, to, timezone string) (costReportRange, error) {
	period := costReportRange{From: from, To: to, Timezone: timezone}
	// Local depends on the execution host, so require a named, reproducible zone.
	if timezone == "" || timezone == "Local" {
		return period, fmt.Errorf("cost reports require UTC or an IANA timezone name")
	}
	zone, err := time.LoadLocation(timezone)
	if err != nil {
		return period, fmt.Errorf("cost reports require UTC or a valid IANA timezone name")
	}
	start, err := costReportMidnight(from, zone)
	if err != nil {
		return period, fmt.Errorf("--from: %w", err)
	}
	end, err := costReportMidnight(to, zone)
	if err != nil {
		return period, fmt.Errorf("--to: %w", err)
	}
	if !start.Before(end) {
		return period, fmt.Errorf("--to must be later than --from; the end date is exclusive")
	}
	period.StartTime, period.EndTime = start.Unix(), end.Unix()
	return period, nil
}

func costReportMidnight(value string, zone *time.Location) (time.Time, error) {
	date, err := time.Parse("2006-01-02", value)
	if err != nil || len(value) != 10 || date.Year() < 1970 {
		return time.Time{}, fmt.Errorf("use an explicit YYYY-MM-DD date on or after 1970-01-01")
	}
	local := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, zone)
	want := value + " 00:00:00"
	if local.Format("2006-01-02 15:04:05") != want {
		return time.Time{}, fmt.Errorf("this date has no local midnight; choose another boundary or UTC")
	}
	// A backward transition can create two midnights. Reject ambiguous boundaries.
	_, offset := local.Zone()
	zoneStart, zoneEnd := local.ZoneBounds()
	if !zoneStart.IsZero() {
		zoneStart = zoneStart.Add(-time.Second)
	}
	for _, nearby := range []time.Time{zoneStart, zoneEnd} {
		if nearby.IsZero() {
			continue
		}
		_, otherOffset := nearby.Zone()
		alternative := local.Add(time.Duration(offset-otherOffset) * time.Second)
		if otherOffset != offset && alternative.Format("2006-01-02 15:04:05") == want {
			return time.Time{}, fmt.Errorf("this date has an ambiguous local midnight; choose another boundary or UTC")
		}
	}
	if local.Unix() < 0 {
		return time.Time{}, fmt.Errorf("the date boundary must be on or after the Unix epoch")
	}
	return local, nil
}

package costs

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCostReportRangeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		from, to, zone, start string
		hours                 int64
	}{
		{"2026-10-01", "2026-10-08", "UTC", "2026-10-01T00:00:00Z", 168},
		{"2026-01-01", "2026-01-08", "Europe/London", "2026-01-01T00:00:00Z", 168},
		{"2026-03-08", "2026-03-09", "Africa/Abidjan", "2026-03-08T00:00:00Z", 24},
		{"1970-01-02", "1970-01-03", "UTC", "1970-01-02T00:00:00Z", 24},
	} {
		t.Run(tc.from+tc.zone, func(t *testing.T) {
			period, err := parseCostReportRange(tc.from, tc.to, tc.zone)
			require.NoError(t, err)
			require.Equal(t, tc.start, time.Unix(period.StartTime, 0).UTC().Format(time.RFC3339))
			require.Equal(t, tc.hours*3600, period.EndTime-period.StartTime)
		})
	}
}

func TestCostReportRangeRejectsInvalidBoundaries(t *testing.T) {
	for _, tc := range []struct{ from, to, zone string }{
		{"today", "2026-10-08", "UTC"}, {"2026-2-01", "2026-10-08", "UTC"},
		{"2026-02-29", "2026-10-08", "UTC"}, {"2026-10-08", "2026-10-08", "UTC"},
		{"2026-10-09", "2026-10-08", "UTC"}, {"1969-01-01", "2026-10-08", "UTC"},
		{"2026-10-01", "2026-10-08", "Local"}, {"2026-10-01", "2026-10-08", ""},
		{"2026-10-01", "2026-10-08", "invalid/private-zone"},
		{"1970-01-01", "1970-01-02", "UTC"},
		{"2026-03-08", "2026-03-09", "America/Los_Angeles"},
		{"2026-11-01", "2026-11-02", "America/Los_Angeles"},
		{"2026-10-01", "2026-10-02", "Asia/Kathmandu"},
		{"2026-01-01", "2026-07-01", "Europe/London"},
		{"2026-07-01", "2026-12-01", "Europe/London"},
		{"2011-12-30", "2011-12-31", "Pacific/Apia"},
		{"2018-11-04", "2018-11-05", "America/Sao_Paulo"},
		{"2026-11-01", "2026-11-02", "America/Havana"},
	} {
		t.Run(tc.from+tc.to+tc.zone, func(t *testing.T) {
			_, err := parseCostReportRange(tc.from, tc.to, tc.zone)
			require.Error(t, err)
		})
	}
}

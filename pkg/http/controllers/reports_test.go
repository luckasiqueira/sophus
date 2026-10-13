package controllers

import (
	"sophus/internal/repo"
	"testing"
	"time"
)

func TestParseReportFilter(t *testing.T) {
	for _, test := range []struct {
		name    string
		values  [3]string
		want    repo.ReportFilter
		invalid bool
	}{
		{name: "defaults"},
		{name: "combined", values: [3]string{"12", "4", "9"}, want: repo.ReportFilter{ConnectionID: 12, DepartmentID: 4, AgentID: 9}},
		{name: "unassigned", values: [3]string{"-1", "-1", "-1"}, want: repo.ReportFilter{ConnectionID: -1, DepartmentID: -1, AgentID: -1}},
		{name: "invalid connection", values: [3]string{"abc", "", ""}, invalid: true},
		{name: "invalid queue", values: [3]string{"", "-2", ""}, invalid: true},
		{name: "invalid agent", values: [3]string{"", "", "1.5"}, invalid: true},
		{name: "overflow", values: [3]string{"99999999999999999999999999", "", ""}, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseReportFilter(test.values[0], test.values[1], test.values[2])
			if (err != nil) != test.invalid {
				t.Fatalf("error = %v, invalid = %v", err, test.invalid)
			}
			if !test.invalid && got != test.want {
				t.Fatalf("got %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestReportPeriodsRespectTimezoneAndInclusiveDates(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 9, 2, 0, 0, 0, time.UTC) // March 8 locally, with a DST change.
	for _, test := range []struct {
		period, from, to, wantFrom, wantTo string
		invalid                            bool
	}{
		{period: "all"},
		{period: "today", wantFrom: "2026-03-08", wantTo: "2026-03-08"},
		{period: "7", wantFrom: "2026-03-02", wantTo: "2026-03-08"},
		{period: "30", wantFrom: "2026-02-07", wantTo: "2026-03-08"},
		{period: "month", wantFrom: "2026-03-01", wantTo: "2026-03-08"},
		{period: "custom", from: "2026-03-08", to: "2026-03-08", wantFrom: "2026-03-08", wantTo: "2026-03-08"},
		{period: "custom", from: "2026-03-09", to: "2026-03-08", invalid: true},
		{period: "custom", from: "2026-02-30", to: "2026-03-08", invalid: true},
		{period: "custom", invalid: true},
		{period: "unknown", invalid: true},
	} {
		t.Run(test.period+test.from, func(t *testing.T) {
			filter, err := parseReportPeriod(repo.ReportFilter{AgentID: 9}, test.period, test.from, test.to, now, location)
			if (err != nil) != test.invalid {
				t.Fatalf("error = %v", err)
			}
			if test.invalid {
				return
			}
			if filter.FromDate() != test.wantFrom || filter.ToDate() != test.wantTo || filter.AgentID != 9 {
				t.Fatalf("unexpected filter: %+v", filter)
			}
			if test.period == "today" && filter.To.Sub(filter.From) != 23*time.Hour {
				t.Fatalf("DST day duration = %v", filter.To.Sub(filter.From))
			}
		})
	}
}

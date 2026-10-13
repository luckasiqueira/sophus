package repo

import (
	"net/url"
	"testing"
	"time"
)

func TestReportExportURLFreezesAppliedDates(t *testing.T) {
	filter := ReportFilter{Period: "7", ConnectionID: 4, DepartmentID: -1, AgentID: 9,
		From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)}
	u, err := url.Parse(filter.ExportURL())
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	if u.Path != "/reports/pdf" || query.Get("period") != "custom" || query.Get("from") != "2026-10-01" || query.Get("to") != "2026-10-07" || query.Get("department") != "-1" || query.Get("agent") != "9" || query.Get("connection") != "4" {
		t.Fatalf("URL = %s", u)
	}
}

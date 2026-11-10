package controllers

import (
	"strings"
	"testing"
	"time"

	"sophus/internal/media"
	"sophus/internal/repo"
)

func TestParseScheduleTimeUsesCompanyTimezoneAndLimits(t *testing.T) {
	location, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skip("timezone database unavailable")
	}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) // 09:00 in São Paulo
	value, problem := parseScheduleTime("2026-10-20", "14:30", location, now)
	if problem != "" || !value.Equal(time.Date(2026, 10, 20, 17, 30, 0, 0, time.UTC)) {
		t.Fatalf("value=%v problem=%q", value, problem)
	}
	for _, test := range []struct{ date, clock, want string }{
		{"2026-10-20", "09:00", "1 minuto"},
		{"2026-10-19", "18:00", "1 minuto"},
		{"2027-12-01", "09:00", "1 ano"},
		{"20/10/2026", "10:00", "data e um horário válidos"},
		{"2026-10-20", "25:00", "data e um horário válidos"},
	} {
		if _, problem := parseScheduleTime(test.date, test.clock, location, now); !strings.Contains(problem, test.want) {
			t.Fatalf("%s %s: problem=%q, want %q", test.date, test.clock, problem, test.want)
		}
	}
}

func TestScheduleLocationFallsBackForInvalidTimezone(t *testing.T) {
	if got := scheduleLocation("Invalid/Zone").String(); got != "America/Sao_Paulo" && got != "UTC" {
		t.Fatalf("fallback location = %s", got)
	}
}

func TestScheduleAttachmentPolicyCapsByCompanyLimit(t *testing.T) {
	policy := scheduleAttachmentPolicy(repo.CompanyOperationSettings{AllowedAttachmentTypes: []string{"image", "video", "sticker"}, MaxAttachmentMB: 20})
	if strings.Join(policy.AllowedTypes, ",") != "image,video" {
		t.Fatalf("allowed types = %v", policy.AllowedTypes)
	}
	if policy.MaxBytes["image"] != media.MaxSize("image") || policy.MaxBytes["video"] != 20<<20 {
		t.Fatalf("limits = %v", policy.MaxBytes)
	}
}

func TestParseRecurrenceUntilBounds(t *testing.T) {
	location, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skip("timezone database unavailable")
	}
	first := time.Date(2026, 11, 2, 9, 0, 0, 0, location)
	if until, problem := parseRecurrenceUntil("2026-11-02", first, location); problem != "" || until.Day() != 2 {
		t.Fatalf("same-day end rejected: %v %q", until, problem)
	}
	for value, want := range map[string]string{"2026-11-01": "depois do primeiro envio", "2028-11-03": "2 anos", "02/11/2026": "válida"} {
		if _, problem := parseRecurrenceUntil(value, first, location); !strings.Contains(problem, want) {
			t.Fatalf("%s: problem=%q, want %q", value, problem, want)
		}
	}
}

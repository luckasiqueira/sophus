package controllers

import (
	"testing"
	"time"
)

func TestFormatBRL(t *testing.T) {
	cases := map[int64]string{
		0:         "R$ 0,00",
		5:         "R$ 0,05",
		14990:     "R$ 149,90",
		123456789: "R$ 1.234.567,89",
		100000000: "R$ 1.000.000,00",
	}
	for cents, want := range cases {
		if got := formatBRL(cents); got != want {
			t.Fatalf("formatBRL(%d) = %q, want %q", cents, got, want)
		}
	}
}

func TestFormatDatesUseBrazilTime(t *testing.T) {
	// 02:30 UTC on 19/10 is still 18/10 at 23:30 in Brasília.
	value := time.Date(2026, 10, 19, 2, 30, 0, 0, time.UTC)
	if got := formatDate(value); got != "18/10/2026" {
		t.Fatalf("formatDate = %q", got)
	}
	if got := formatDateTime(value); got != "18/10/2026 23:30" {
		t.Fatalf("formatDateTime = %q", got)
	}
}

func TestAppLoginURLNormalizesDomain(t *testing.T) {
	t.Setenv("APP_DOMAIN", "")
	if got := appLoginURL(); got != "" {
		t.Fatalf("empty domain should yield no link, got %q", got)
	}
}

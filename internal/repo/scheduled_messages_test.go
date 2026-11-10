package repo

import (
	"testing"
	"time"
)

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("timezone %s unavailable: %v", name, err)
	}
	return location
}

func TestNextScheduledOccurrenceKeepsMonthlyDayAnchored(t *testing.T) {
	location := mustLocation(t, "America/Sao_Paulo")
	start := time.Date(2027, 1, 31, 9, 0, 0, 0, location)
	message := ScheduledMessage{Recurrence: ScheduledRecurrenceMonthly, ScheduledFor: start, SeriesStart: start}
	want := []time.Time{
		time.Date(2027, 2, 28, 9, 0, 0, 0, location), // shorter month: last day
		time.Date(2027, 3, 31, 9, 0, 0, 0, location), // back on the 31st, no drift
		time.Date(2027, 4, 30, 9, 0, 0, 0, location),
	}
	now := start.Add(-time.Hour)
	for _, expected := range want {
		next, ok := NextScheduledOccurrence(message, location, now)
		if !ok || !next.Equal(expected) {
			t.Fatalf("after %v: next=%v ok=%v, want %v", message.ScheduledFor, next, ok, expected)
		}
		message.ScheduledFor = next
	}
}

func TestNextScheduledOccurrenceDailyAndWeeklyKeepLocalTimeAcrossDST(t *testing.T) {
	location := mustLocation(t, "America/New_York")
	start := time.Date(2027, 3, 13, 9, 0, 0, 0, location) // DST starts on 2027-03-14
	daily := ScheduledMessage{Recurrence: ScheduledRecurrenceDaily, ScheduledFor: start, SeriesStart: start}
	next, ok := NextScheduledOccurrence(daily, location, start)
	if !ok || next.In(location).Hour() != 9 || next.In(location).Day() != 14 {
		t.Fatalf("daily across DST = %v ok=%v", next.In(location), ok)
	}
	weekly := ScheduledMessage{Recurrence: ScheduledRecurrenceWeekly, ScheduledFor: start, SeriesStart: start}
	next, ok = NextScheduledOccurrence(weekly, location, start)
	if !ok || !next.Equal(time.Date(2027, 3, 20, 9, 0, 0, 0, location)) {
		t.Fatalf("weekly = %v ok=%v", next.In(location), ok)
	}
}

func TestNextScheduledOccurrenceSkipsMissedDatesAndHonorsUntil(t *testing.T) {
	location := mustLocation(t, "America/Sao_Paulo")
	start := time.Date(2027, 5, 3, 8, 30, 0, 0, location)
	message := ScheduledMessage{Recurrence: ScheduledRecurrenceDaily, ScheduledFor: start, SeriesStart: start}
	// Sent late (worker was down for 3 days): the next one is after now, not a backlog.
	now := time.Date(2027, 5, 6, 10, 0, 0, 0, location)
	next, ok := NextScheduledOccurrence(message, location, now)
	if !ok || !next.Equal(time.Date(2027, 5, 7, 8, 30, 0, 0, location)) {
		t.Fatalf("next after downtime = %v ok=%v", next, ok)
	}
	// "Send now" on an occurrence keeps the calendar: the next one follows the original date.
	early := ScheduledMessage{Recurrence: ScheduledRecurrenceWeekly, ScheduledFor: start, SeriesStart: start}
	next, ok = NextScheduledOccurrence(early, location, start.AddDate(0, 0, -2))
	if !ok || !next.Equal(start.AddDate(0, 0, 7)) {
		t.Fatalf("next after early send = %v ok=%v", next, ok)
	}
	message.RecurrenceUntil = "2027-05-04"
	if next, ok := NextScheduledOccurrence(message, location, start); !ok || next.Day() != 4 {
		t.Fatalf("the last day must still be included: %v ok=%v", next, ok)
	}
	message.ScheduledFor = time.Date(2027, 5, 4, 8, 30, 0, 0, location)
	if next, ok := NextScheduledOccurrence(message, location, message.ScheduledFor); ok {
		t.Fatalf("series must end after its last day, got %v", next)
	}
	if _, ok := NextScheduledOccurrence(ScheduledMessage{Recurrence: ScheduledRecurrenceNone, ScheduledFor: start}, location, start); ok {
		t.Fatal("non-recurring schedule produced a next occurrence")
	}
}

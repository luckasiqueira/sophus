package controllers

import (
	"strings"
	"testing"
)

func TestContactTagNameCountsCharactersInsteadOfBytes(t *testing.T) {
	for _, test := range []struct {
		name  string
		valid bool
	}{
		{"VIP", true}, {strings.Repeat("á", 80), true}, {strings.Repeat("á", 81), false}, {"", false}, {"invalid\x00tag", false},
	} {
		if got := validContactTagName(test.name); got != test.valid {
			t.Errorf("validContactTagName(%q) = %v, want %v", test.name, got, test.valid)
		}
	}
}

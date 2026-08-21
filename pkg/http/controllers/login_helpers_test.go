package controllers

import "testing"

func TestSameOrigin(t *testing.T) {
	tests := []struct {
		name      string
		origin    string
		fetchSite string
		host      string
		want      bool
	}{
		{name: "same origin", origin: "https://app.example.com", fetchSite: "same-origin", host: "app.example.com", want: true},
		{name: "cross origin", origin: "https://attacker.example", fetchSite: "cross-site", host: "app.example.com"},
		{name: "mismatched host", origin: "https://other.example.com", fetchSite: "same-site", host: "app.example.com"},
		{name: "non browser client", host: "app.example.com", want: true},
		{name: "invalid origin", origin: "://invalid", host: "app.example.com"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sameOrigin(test.origin, test.fetchSite, test.host); got != test.want {
				t.Fatalf("sameOrigin() = %t, want %t", got, test.want)
			}
		})
	}
}

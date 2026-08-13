package instances

import "testing"

func TestParseStatus(t *testing.T) {
	tests := []struct {
		name      string
		payload   map[string]interface{}
		connected bool
		found     bool
	}{
		{name: "nested connected", payload: map[string]interface{}{"data": map[string]interface{}{"connected": true}}, connected: true, found: true},
		{name: "open state", payload: map[string]interface{}{"state": "open"}, connected: true, found: true},
		{name: "offline status", payload: map[string]interface{}{"status": "disconnected"}, found: true},
		{name: "unknown", payload: map[string]interface{}{"message": "ok"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, found := parseStatus(test.payload)
			if found != test.found || status.Connected != test.connected {
				t.Fatalf("parseStatus() = %#v, %v", status, found)
			}
		})
	}
}

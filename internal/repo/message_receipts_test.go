package repo

import "testing"

func TestParseMessageReceipt(t *testing.T) {
	for _, tc := range []struct {
		name, body    string
		status, count int
	}{
		{"delivery", `{"data":{"MessageIDs":["ABC","ABC","","DEF"],"Type":""}}`, 2, 2},
		{"read", `{"data":{"messageIDs":["ABC"],"type":"read"}}`, 3, 1},
		{"played", `{"data":{"MessageIDs":["ABC"],"Type":"played"}}`, 3, 1},
		{"own device", `{"data":{"MessageIDs":["ABC"],"Type":"read-self"}}`, 0, 1},
		{"sender", `{"data":{"MessageIDs":["ABC"],"Type":"sender"}}`, 0, 1},
		{"unknown", `{"data":{"MessageIDs":["ABC"],"Type":"new-status"}}`, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ids, status, err := ParseMessageReceipt([]byte(tc.body))
			if err != nil || status != tc.status || len(ids) != tc.count {
				t.Fatalf("got %v, %d, %v", ids, status, err)
			}
		})
	}
	if _, _, err := ParseMessageReceipt([]byte(`{"data":`)); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

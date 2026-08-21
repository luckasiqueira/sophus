package payments

import (
	"errors"
	"testing"
)

func TestIsDefinitiveHTTPFailure(t *testing.T) {
	tests := []struct {
		status int
		want   bool
	}{
		{status: 400, want: true},
		{status: 401, want: true},
		{status: 408, want: false},
		{status: 409, want: false},
		{status: 422, want: true},
		{status: 429, want: false},
		{status: 500, want: false},
	}
	for _, test := range tests {
		err := &HTTPError{Provider: "test", StatusCode: test.status, Status: "status", Message: "message"}
		if got := IsDefinitiveHTTPFailure(err); got != test.want {
			t.Fatalf("IsDefinitiveHTTPFailure(status=%d) = %t, want %t", test.status, got, test.want)
		}
		if got := IsDefinitiveHTTPFailure(errors.New("transport error")); got {
			t.Fatal("transport error must remain ambiguous")
		}
	}
}

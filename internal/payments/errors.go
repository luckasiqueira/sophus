package payments

import (
	"errors"
	"fmt"
)

type HTTPError struct {
	Provider   string
	StatusCode int
	Status     string
	Message    string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s API returned %s: %s", e.Provider, e.Status, e.Message)
}

func IsDefinitiveHTTPFailure(err error) bool {
	var httpError *HTTPError
	if !errors.As(err, &httpError) || httpError.StatusCode < 400 || httpError.StatusCode >= 500 {
		return false
	}
	switch httpError.StatusCode {
	case 408, 409, 425, 429:
		return false
	default:
		return true
	}
}

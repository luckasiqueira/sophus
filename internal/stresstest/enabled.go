// Package stresstest simulates online agents and WhatsApp instances against
// this server and samples its resource consumption while they run.
package stresstest

import (
	"strings"

	"sophus/utils/env"
)

// Enabled reports whether STRESS_TEST_ENABLED allows the super-admin stress
// tools. It is off unless explicitly enabled, since the tools write test data
// into real companies.
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(env.Backend["STRESS_TEST_ENABLED"])) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

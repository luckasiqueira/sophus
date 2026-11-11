// Package waprovider selects the WhatsApp engine used by DoveChat.
//
// WHATSAPP_PROVIDER=evolution (default) keeps the Evolution GO integration.
// WHATSAPP_PROVIDER=waha routes the same Evolution-shaped calls through the
// in-process WAHA bridge (internal/waha), so business logic stays unchanged.
package waprovider

import (
	"strings"

	"sophus/utils/env"
)

const (
	Evolution = "evolution"
	WAHA      = "waha"

	// BridgeBaseURL replaces the Evolution GO base URL when WAHA is selected.
	// The custom scheme can only be served by the registered request
	// interceptor: if it is ever missing, requests fail instead of leaking to
	// an external host.
	BridgeBaseURL = "waha-bridge://evolution"
)

// Name returns the configured provider, defaulting to Evolution GO.
func Name() string {
	return normalize(env.Backend["WHATSAPP_PROVIDER"])
}

func normalize(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case WAHA, "waha-gows", "gows":
		return WAHA
	default:
		return Evolution
	}
}

func IsWAHA() bool {
	return Name() == WAHA
}

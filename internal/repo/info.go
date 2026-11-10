package repo

import (
	"fmt"
	"sophus/internal/waprovider"
	"sophus/utils/env"
)

// ApiBaseURL is the Evolution GO API. With WHATSAPP_PROVIDER=waha it points to
// the in-process WAHA bridge, which serves the same Evolution contract.
var ApiBaseURL = whatsappAPIBaseURL()

func whatsappAPIBaseURL() string {
	if waprovider.IsWAHA() {
		return waprovider.BridgeBaseURL
	}
	return fmt.Sprintf("https://%s", env.Backend["WPP_API_DOMAIN"])
}

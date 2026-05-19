package wpp

import (
	"fmt"
	"zubly/backend/pkg/http/requests"
	"zubly/backend/utils/env"

	"github.com/google/uuid"
)

type Instance struct {
	ID         int
	Name       string
	Connected  bool
	WebhookURL string
	Token      uuid.UUID
}

var apiBaseURL = fmt.Sprintf("https://%s", env.Backend["WPP_API_DOMAIN"])

func (i *Instance) Connect() error {
	headers := map[string]string{
		"Content-Type": "application/json",
		"Apikey":       i.Token.String(),
	}
	payload := map[string]any{
		"webhookUrl": i.WebhookURL,
		"subscribe":  []string{"ALL"},
		"immediate":  true,
	}
	r := requests.Request{
		URL:     fmt.Sprintf("%s/instance/connect", apiBaseURL),
		Payload: payload,
		Headers: headers,
	}
	return r.Do()
}

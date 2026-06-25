package instances

import "github.com/google/uuid"

type InstanceEVO struct {
	ID            int       `json:"id"`
	Name          string    `json:"name"`
	Connected     bool      `json:"connected"`
	WebhookURL    string    `json:"webhookURL"`
	InstanceID    uuid.UUID `json:"instanceId"`
	ConnectionKey uuid.UUID `json:"connectionKey"`
	APIToken      string    `json:"apiToken"`
}

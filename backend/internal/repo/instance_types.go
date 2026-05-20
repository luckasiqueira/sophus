package repo

import "github.com/google/uuid"

type InstanceEVO struct {
	ID         int
	Name       string
	Connected  bool
	WebhookURL string
	Token      uuid.UUID
}

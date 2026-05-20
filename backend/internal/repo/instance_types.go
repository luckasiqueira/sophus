package repo

import "github.com/google/uuid"

type Instance struct {
	ID         int
	Name       string
	Connected  bool
	WebhookURL string
	Token      uuid.UUID
}

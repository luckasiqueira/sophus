package database

import (
	"time"

	"github.com/google/uuid"
)

type Connection struct {
	Id            int
	Name          string
	Number        string
	Status        string
	CompanyID     int
	QRCode        string
	CreatedAt     time.Time
	InstanceID    uuid.UUID
	Webhook       uuid.UUID
	APIToken      string
	ConnectionKey uuid.UUID
}

package repo

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

type ConnectionEVO struct {
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

func GetConnectionByToken(apiToken string) (ConnectionEVO, error) {
	stmt, err := db.Prepare(`SELECT "id", "status", "instanceId", "connectionKey" FROM connections WHERE "apiToken" = $1`)
	if err != nil {
		fmt.Println("GetConnectionByToken", err)
		return ConnectionEVO{}, err
	}
	defer stmt.Close()
	var c ConnectionEVO
	err = stmt.QueryRow(apiToken).Scan(&c.Id, &c.Status, &c.InstanceID, &c.ConnectionKey)
	return c, err
}

func GetConnectionByWebhook(webhookId string) (ConnectionEVO, error) {
	stmt, err := db.Prepare(`SELECT "id", "status", "instanceId", "connectionKey" FROM connections WHERE "webhook" = $1`)
	if err != nil {
		fmt.Println(err)
		return ConnectionEVO{}, err
	}
	defer stmt.Close()
	var c ConnectionEVO
	err = stmt.QueryRow(webhookId).Scan(&c.Id, &c.Status, &c.InstanceID, &c.ConnectionKey)
	return c, err
}

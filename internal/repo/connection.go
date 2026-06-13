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

func GetConnectionByAPI(apiToken string) (ConnectionEVO, error) {
	stmt, err := db.Prepare(`SELECT "id", "status", "instanceId", "connectionKey" FROM connections WHERE "apiToken" = $1`)
	if err != nil {
		return ConnectionEVO{}, err
	}
	defer stmt.Close()
	var c ConnectionEVO
	err = stmt.QueryRow(apiToken).Scan(&c.Id, &c.Status, &c.InstanceID, &c.ConnectionKey)
	return c, err
}

func GetConnectionByWebhook(webhookId string) (ConnectionEVO, error) {
	stmt, err := db.Prepare(`SELECT "id", "status", "instanceId", "connectionKey", "companyId" FROM connections WHERE "webhook" = $1`)
	if err != nil {
		return ConnectionEVO{}, err
	}
	defer stmt.Close()
	var c ConnectionEVO
	err = stmt.QueryRow(webhookId).Scan(&c.Id, &c.Status, &c.InstanceID, &c.ConnectionKey, &c.CompanyID)
	return c, err
}

func GetConnectionByConversationURL(url uuid.UUID) (ConnectionEVO, error) {
	stmt, err := db.Prepare(`SELECT c."id", c."status", c."instanceId", c."connectionKey", c."companyId"
		FROM conversations cv
		INNER JOIN connections c
			ON c.id = cv."connectionId"
		WHERE cv."url" = $1`)
	if err != nil {
		fmt.Println(err)
		return ConnectionEVO{}, err
	}
	defer stmt.Close()
	var c ConnectionEVO
	err = stmt.QueryRow().Scan(&c.Id, &c.Status, &c.InstanceID, &c.ConnectionKey, &c.CompanyID)
	fmt.Println(err)
	return c, err
}

func GetConnectionListByCompany(companyId int) ([]ConnectionEVO, error) {
	stmt, err := db.Prepare(`SELECT * FROM public.connections WHERE "companyId" = $1`)
	if err != nil {
		return []ConnectionEVO{}, err
	}
	defer stmt.Close()
	connectionsList := []ConnectionEVO{}
	rows, err := stmt.Query(companyId)
	for rows.Next() {
		var c ConnectionEVO
		err = rows.Scan(&c.Id, &c.Name, &c.Number, &c.Status, &c.CompanyID, &c.QRCode, &c.CreatedAt, &c.InstanceID, &c.Webhook, &c.APIToken, &c.ConnectionKey)
		if err != nil {
			return []ConnectionEVO{}, err
		}
		connectionsList = append(connectionsList, c)
	}
	return connectionsList, nil
}

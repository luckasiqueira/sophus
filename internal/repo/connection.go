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

func (c ConnectionEVO) EvolutionAPIKey() string {
	if c.APIToken != "" {
		return c.APIToken
	}
	return c.ConnectionKey.String()
}

func CreateConnection(c ConnectionEVO) (int, error) {
	return insertInt(`INSERT INTO connections
		(name, number, status, "companyId", qrcode, "createdAt", "instanceId", webhook, "apiToken", "connectionKey")
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		c.Name, c.Number, c.Status, c.CompanyID, c.QRCode, c.CreatedAt,
		c.InstanceID, c.Webhook, c.APIToken, c.ConnectionKey)
}

func UpdateConnectionQRCode(id int, qrcode string) (bool, error) {
	result, err := db.Exec(`UPDATE connections SET qrcode = $1, status = 'connecting'
		WHERE id = $2 AND status IN ('creating', 'connecting')`, qrcode, id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func UpdateConnectionStatus(id int, status string) error {
	_, err := db.Exec(`UPDATE connections SET status = $1 WHERE id = $2`, status, id)
	return err
}

func MarkConnectionQRTimeout(id int) (bool, error) {
	result, err := db.Exec(`UPDATE connections SET status = 'disconnected', qrcode = ''
		WHERE id = $1 AND status IN ('creating', 'connecting')`, id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func UpdateConnectionCredentials(id int, instanceID, connectionKey uuid.UUID) error {
	_, err := db.Exec(`UPDATE connections SET "instanceId" = $1, "connectionKey" = $2 WHERE id = $3`, instanceID, connectionKey, id)
	return err
}

func SetConnectionConnected(id int, number string) (bool, error) {
	result, err := db.Exec(`UPDATE connections SET status = 'connected', number = $1, qrcode = ''
		WHERE id = $2 AND status IN ('creating', 'connecting')`, number, id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func GetConnectionByAPI(apiToken string) (ConnectionEVO, error) {
	stmt, err := db.Prepare(`SELECT "id", "status", "instanceId", "connectionKey" FROM connections WHERE "apiToken" = $1`)
	if err != nil {
		return ConnectionEVO{}, err
	}
	defer stmt.Close()
	var c ConnectionEVO
	err = stmt.QueryRow(apiToken).Scan(&c.Id, &c.Status, &c.InstanceID, &c.ConnectionKey)
	c.APIToken = apiToken
	return c, err
}

func GetConnectionByWebhook(webhookId string) (ConnectionEVO, error) {
	stmt, err := db.Prepare(`SELECT "id", "status", "instanceId", "connectionKey", "companyId", COALESCE("apiToken", '') FROM connections WHERE "webhook" = $1`)
	if err != nil {
		return ConnectionEVO{}, err
	}
	defer stmt.Close()
	var c ConnectionEVO
	err = stmt.QueryRow(webhookId).Scan(&c.Id, &c.Status, &c.InstanceID, &c.ConnectionKey, &c.CompanyID, &c.APIToken)
	return c, err
}

func GetConnectionByConversationURL(url uuid.UUID) (ConnectionEVO, error) {
	stmt, err := db.Prepare(`SELECT c."id", c."status", c."instanceId", c."connectionKey", c."companyId", COALESCE(c."apiToken", '')
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
	err = stmt.QueryRow(url).Scan(&c.Id, &c.Status, &c.InstanceID, &c.ConnectionKey, &c.CompanyID, &c.APIToken)
	fmt.Println(err)
	return c, err
}

func GetConnectionListByCompany(companyId int) ([]ConnectionEVO, error) {
	stmt, err := db.Prepare(`SELECT id, name, number, status, "companyId", COALESCE(qrcode,''), "createdAt",
		"instanceId", webhook, COALESCE("apiToken", ''), "connectionKey"
		FROM connections WHERE "companyId" = $1 ORDER BY id DESC`)
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

package repo

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrConnectionInUse = errors.New("connection is in use")

const (
	ConnectionStatusCreating     = "creating"
	ConnectionStatusConnecting   = "connecting"
	ConnectionStatusConnected    = "connected"
	ConnectionStatusDisconnected = "disconnected"
	ConnectionStatusError        = "error"
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
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var companyID int
	if err := tx.QueryRow(`SELECT id FROM companies WHERE id = $1 FOR UPDATE`, c.CompanyID).Scan(&companyID); err != nil {
		return 0, err
	}
	var connectionLimit, connectionCount int
	err = tx.QueryRow(`SELECT limits."connectionLimit", COUNT(c.id)
		FROM subscriptions s
		JOIN subscription_effective_limits limits ON limits."subscriptionId"=s.id
		LEFT JOIN connections c ON c."companyId" = s."companyId"
		WHERE s."companyId" = $1 AND s.status IN ('trialing', 'active', 'past_due')
		GROUP BY limits."connectionLimit"`, c.CompanyID).Scan(&connectionLimit, &connectionCount)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, err
		}
		return 0, err
	}
	if connectionCount >= connectionLimit {
		return 0, ErrConnectionLimitReached
	}

	var id int
	err = tx.QueryRow(`INSERT INTO connections
		(name, number, status, "companyId", qrcode, "createdAt", "instanceId", webhook, "apiToken", "connectionKey")
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		c.Name, c.Number, c.Status, c.CompanyID, c.QRCode, c.CreatedAt,
		c.InstanceID, c.Webhook, c.APIToken, c.ConnectionKey).Scan(&id)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
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

func RenameConnection(companyID, id int, name string) (bool, error) {
	result, err := db.Exec(`UPDATE connections SET name = $1 WHERE id = $2 AND "companyId" = $3`, name, id, companyID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func DeleteConnection(companyID, id int, deleteHistory bool) (bool, error) {
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM connections WHERE id = $1 AND "companyId" = $2 FOR UPDATE)`, id, companyID).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	if deleteHistory {
		if _, err := tx.Exec(`DELETE FROM conversations WHERE "connectionId" = $1`, id); err != nil {
			return false, err
		}
	} else {
		if _, err := tx.Exec(`UPDATE conversations SET "connectionId" = NULL, status = 'closed' WHERE "connectionId" = $1`, id); err != nil {
			return false, err
		}
	}
	result, err := tx.Exec(`DELETE FROM connections WHERE id = $1 AND "companyId" = $2`, id, companyID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return false, err
	}
	return true, tx.Commit()
}

func ConnectionInUse(companyID, id int) (bool, error) {
	var used bool
	err := db.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM conversations conversation
		JOIN connections connection ON connection.id = conversation."connectionId"
		WHERE conversation."connectionId" = $1 AND connection."companyId" = $2
	)`, id, companyID).Scan(&used)
	return used, err
}

func ReconcileConnectionStatus(id int, status, number string) (bool, error) {
	result, err := db.Exec(`UPDATE connections
		SET status = $1,
			number = CASE WHEN $2 <> '' THEN $2 ELSE number END,
			qrcode = CASE WHEN $1 IN ('connected', 'disconnected') THEN '' ELSE qrcode END
		WHERE id = $3 AND (status <> $1 OR ($2 <> '' AND number <> $2))
		AND NOT (status IN ('creating', 'connecting') AND $1 = 'disconnected')`, status, number, id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
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
		WHERE id = $2 AND (status <> 'connected' OR number <> $1 OR qrcode <> '')`, number, id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func GetConnectionByAPI(apiToken string) (ConnectionEVO, error) {
	stmt, err := db.Prepare(`SELECT "id", "status", "instanceId", "connectionKey", "companyId"
		FROM connections
		WHERE "apiToken" = $1
			AND (SELECT COUNT(*) FROM connections WHERE "apiToken" = $1) = 1`)
	if err != nil {
		return ConnectionEVO{}, err
	}
	defer stmt.Close()
	var c ConnectionEVO
	err = stmt.QueryRow(apiToken).Scan(&c.Id, &c.Status, &c.InstanceID, &c.ConnectionKey, &c.CompanyID)
	c.APIToken = apiToken
	return c, err
}

func GetConnectionByID(companyID, id int) (ConnectionEVO, error) {
	var connection ConnectionEVO
	err := db.QueryRow(`SELECT id, name, number, status, "companyId", COALESCE(qrcode,''), "createdAt",
		"instanceId", webhook, COALESCE("apiToken", ''), "connectionKey"
		FROM connections WHERE id = $1 AND "companyId" = $2`, id, companyID).Scan(
		&connection.Id, &connection.Name, &connection.Number, &connection.Status, &connection.CompanyID,
		&connection.QRCode, &connection.CreatedAt, &connection.InstanceID, &connection.Webhook, &connection.APIToken,
		&connection.ConnectionKey)
	return connection, err
}

func GetConnectionByWebhook(webhookId string) (ConnectionEVO, error) {
	stmt, err := webhookDB.Prepare(`SELECT "id", "status", "instanceId", "connectionKey", "companyId", COALESCE("apiToken", '') FROM connections WHERE "webhook" = $1`)
	if err != nil {
		return ConnectionEVO{}, err
	}
	defer stmt.Close()
	var c ConnectionEVO
	err = stmt.QueryRow(webhookId).Scan(&c.Id, &c.Status, &c.InstanceID, &c.ConnectionKey, &c.CompanyID, &c.APIToken)
	return c, err
}

func GetConnectionByConversation(conversationID, companyID int) (ConnectionEVO, error) {
	stmt, err := db.Prepare(`SELECT c."id", c."status", c."instanceId", c."connectionKey", c."companyId", COALESCE(c."apiToken", '')
		FROM conversations cv
		INNER JOIN connections c
			ON c.id = cv."connectionId"
		WHERE cv.id = $1 AND c."companyId" = $2`)
	if err != nil {
		return ConnectionEVO{}, err
	}
	defer stmt.Close()
	var c ConnectionEVO
	err = stmt.QueryRow(conversationID, companyID).Scan(&c.Id, &c.Status, &c.InstanceID, &c.ConnectionKey, &c.CompanyID, &c.APIToken)
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
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ConnectionEVO
		err = rows.Scan(&c.Id, &c.Name, &c.Number, &c.Status, &c.CompanyID, &c.QRCode, &c.CreatedAt, &c.InstanceID, &c.Webhook, &c.APIToken, &c.ConnectionKey)
		if err != nil {
			return []ConnectionEVO{}, err
		}
		connectionsList = append(connectionsList, c)
	}
	return connectionsList, rows.Err()
}

func GetConnectionList() ([]ConnectionEVO, error) {
	stmt, err := db.Prepare(`SELECT id, name, number, status, "companyId", COALESCE(qrcode,''), "createdAt",
		"instanceId", webhook, COALESCE("apiToken", ''), "connectionKey"
		FROM connections ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	rows, err := stmt.Query()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	connections := []ConnectionEVO{}
	for rows.Next() {
		var connection ConnectionEVO
		if err := rows.Scan(&connection.Id, &connection.Name, &connection.Number, &connection.Status, &connection.CompanyID,
			&connection.QRCode, &connection.CreatedAt, &connection.InstanceID, &connection.Webhook, &connection.APIToken,
			&connection.ConnectionKey); err != nil {
			return nil, err
		}
		connections = append(connections, connection)
	}
	return connections, rows.Err()
}

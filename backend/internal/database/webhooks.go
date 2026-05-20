package database

import "fmt"

func GetConnectionByWebhook(webhookId string) (Connection, error) {
	stmt, err := db.Prepare(`SELECT "id", "status", "instanceId" FROM public.connections WHERE "webhook" = $1`)
	if err != nil {
		fmt.Println(err)
		return Connection{}, err
	}
	defer stmt.Close()
	var c Connection
	err = stmt.QueryRow(webhookId).Scan(&c.Id, &c.Status, &c.InstanceID)
	return c, err
}

package database

import (
	"fmt"

	"github.com/kataras/iris/v12"
)

func CheckValidToken(apiToken string) bool {
	stmt, err := db.Prepare("SELECT COUNT(*) FROM tokens WHERE apitoken = $1")
	if err != nil {
		fmt.Println(err)
		return false
	}
	defer stmt.Close()
	var count int
	err = stmt.QueryRow(apiToken).Scan(&count)
	fmt.Println(err)
	return err == nil && count == 1
}

func CheckAPIToken(apiToken string) (string, int) {
	stmt, err := db.Prepare(`SELECT "connectionkey" FROM tokens WHERE apitoken = $1`)
	defer stmt.Close()
	if err != nil {
		return "", iris.StatusBadRequest
	}
	var connectionKey string
	err = stmt.QueryRow(apiToken).Scan(&connectionKey)
	if err != nil {
		return "", iris.StatusInternalServerError
	}
	return connectionKey, iris.StatusOK
}

package repo

import (
	"fmt"
)

func IsValidAPITokenEVO(apiToken string) bool {
	stmt, err := db.Prepare(`SELECT COUNT(*) FROM connections WHERE "apiToken" = $1`)
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

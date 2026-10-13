package repo

func insert(query string, args ...interface{}) error {
	_, err := db.Exec(query, args...)
	return err
}

func insertInt(query string, args ...interface{}) (int, error) {
	var num int
	err := db.QueryRow(query, args...).Scan(&num)
	return num, err
}

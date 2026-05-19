package database

func insert(query string, args ...interface{}) error {
	stmt, err := db.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()
	if args == nil {
		_, err = stmt.Exec()
	} else {
		_, err = stmt.Exec(args...)
	}
	if err != nil {
		return err
	}
	return nil
}

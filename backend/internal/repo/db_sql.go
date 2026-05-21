package repo

func insert(query string, args ...interface{}) error {
	stmt, err := db.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()
	if args == nil {
		err = stmt.QueryRow().Err()
	} else {
		err = stmt.QueryRow(args...).Err()
	}
	if err != nil {
		return err
	}
	return nil
}

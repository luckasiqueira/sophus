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
	return err
}

func insertInt(query string, args ...interface{}) (int, error) {
	stmt, err := db.Prepare(query)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	var num int
	if args == nil {
		err = stmt.QueryRow().Err()
	} else {
		err = stmt.QueryRow(args...).Scan(&num)
	}
	return num, err
}

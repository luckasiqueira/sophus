package database

import (
	"database/sql"
	"fmt"
	_ "github.com/lib/pq"
	"log"
	"zubly/backend/utils/env"
)

func connect() *sql.DB {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		env.Backend["DB_USER"],
		env.Backend["DB_PASS"],
		env.Backend["DB_HOST"],
		env.Backend["DB_PORT"],
		env.Backend["DB_NAME"],
	)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Panic(err)
	}
	return db
}

package database

import (
	"database/sql"
	"fmt"
	"log"
	"time"
	"zubly/backend/utils/env"

	_ "github.com/lib/pq"
)

var DB = connect()

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
	db.SetMaxOpenConns(30)
	db.SetMaxIdleConns(15)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	return db
}

package repo

import (
	"database/sql"
	"fmt"
	"log"
	"sophus/backend/utils/env"
	"time"

	_ "github.com/lib/pq"
)

var db = connect()

func connect() *sql.DB {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		env.Backend["POSTGRES_USER"],
		env.Backend["POSTGRES_PASSWORD"],
		env.Backend["POSTGRES_HOST"],
		env.Backend["POSTGRES_PORT"],
		env.Backend["POSTGRES_DB"],
	)
	sdb, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Panic(err)
	}
	sdb.SetMaxOpenConns(30)
	sdb.SetMaxIdleConns(15)
	sdb.SetConnMaxLifetime(30 * time.Minute)
	sdb.SetConnMaxIdleTime(5 * time.Minute)
	return sdb
}

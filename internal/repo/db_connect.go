package repo

import (
	"database/sql"
	"fmt"
	"log"
	"sophus/utils/env"
	"time"

	_ "github.com/lib/pq"
)

var db = connect(8, 3)
var webhookDB = connect(4, 1)
var DB = db

func connect(maxOpen, maxIdle int) *sql.DB {
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
	sdb.SetMaxOpenConns(maxOpen)
	sdb.SetMaxIdleConns(maxIdle)
	sdb.SetConnMaxLifetime(30 * time.Minute)
	sdb.SetConnMaxIdleTime(5 * time.Minute)
	return sdb
}

package repo

import (
	"embed"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

func RunMigrations() error {
	goose.SetBaseFS(migrations)
	err := goose.SetDialect("postgres")
	if err != nil {
		return err
	}
	return goose.Up(db, "migrations")
}

package main

import (
	"fmt"
	"os"
	"sophus/backend/internal/repo"
	"sophus/backend/pkg/http"
	"sophus/backend/utils/env"
)

func main() {
	err := repo.RunMigrations()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	err = http.Start(env.Backend["SERVER_PORT"])
	if err != nil {
		panic(err)
	}
}

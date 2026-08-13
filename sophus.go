package main

import (
	"context"
	"fmt"
	"os"
	"sophus/internal/instancesync"
	"sophus/internal/repo"
	"sophus/pkg/http/routers"
	"sophus/utils/env"

	"github.com/kataras/iris/v12"
)

func main() {
	err := repo.RunMigrations()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	srv := iris.Default()
	routers.Router(srv)
	instancesync.Start(context.Background())
	err = srv.Listen(":" + env.Backend["SERVER_PORT"])
	if err != nil {
		panic(err)
	}
}

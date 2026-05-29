package main

import (
	"fmt"
	"os"
	"sophus/backend/internal/repo"
	"sophus/backend/pkg/http/routers"
	"sophus/backend/utils/env"

	"github.com/kataras/iris/v12"
)

func main() {
	err := repo.RunMigrations()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	srv := iris.Default()
	srv.HandleDir("/medias", iris.Dir(env.Backend["MEDIA_DIRECTORY"]))
	routers.Router(srv)
	err = srv.Listen(":" + env.Backend["SERVER_PORT"])
	if err != nil {
		panic(err)
	}
}

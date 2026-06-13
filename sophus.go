package main

import (
	"fmt"
	"os"
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
	srv.HandleDir("/medias", iris.Dir(env.Backend["MEDIA_DIRECTORY"]), iris.DirOptions{
		Attachments: iris.Attachments{
			Enable: true,
		},
	})
	routers.Router(srv)
	err = srv.Listen(":" + env.Backend["SERVER_PORT"])
	if err != nil {
		panic(err)
	}
}

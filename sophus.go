package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sophus/internal/flowengine"
	"sophus/internal/instancesync"
	"sophus/internal/repo"
	"sophus/pkg/http/routers"
	"sophus/utils/env"
	"syscall"

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
	backgroundCtx, stopBackground := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopBackground()
	instancesync.Start(backgroundCtx)
	flowengine.StartScheduler(backgroundCtx)
	err = srv.Listen(":" + env.Backend["SERVER_PORT"])
	if err != nil {
		panic(err)
	}
}

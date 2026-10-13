package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sophus/internal/avatarworker"
	"sophus/internal/billingworker"
	"sophus/internal/flowengine"
	"sophus/internal/instancesync"
	"sophus/internal/repo"
	"sophus/internal/storageworker"
	"sophus/pkg/http/controllers"
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
	avatarworker.Start(backgroundCtx)
	billingworker.Start(backgroundCtx)
	if err := controllers.StartWebhookQueue(backgroundCtx); err != nil {
		log.Printf("webhook queue unavailable: %v", err)
	}
	flowengine.StartScheduler(backgroundCtx)
	storageworker.Start(backgroundCtx)
	err = srv.Listen(":" + env.Backend["SERVER_PORT"])
	if err != nil {
		panic(err)
	}
}

package controllers

import (
	"log"
	"strconv"
	"time"

	"sophus/internal/monitor"
	"sophus/internal/repo"
	"sophus/internal/stresstest"
	"sophus/pkg/http/middlewares"

	"github.com/kataras/iris/v12"
)

// GetMonitoring feeds the super-admin home: resource samples newer than the
// "since" cursor (Unix milliseconds) and the companies currently active.
func GetMonitoring(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	companies, err := repo.ListCompanyActivity(middlewares.OnlineAgentIDs())
	if err != nil {
		log.Printf("monitoring company activity failed: %v", err)
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível consultar a atividade das empresas")
		return
	}
	ctx.JSON(iris.Map{"data": iris.Map{
		"host": monitor.HostInfo(), "samples": monitor.Samples(sinceParam(ctx)), "companies": companies,
		"simulationRunning": stresstest.SimulationRunning(),
	}})
}

func sinceParam(ctx iris.Context) time.Time {
	if value, err := strconv.ParseInt(ctx.URLParam("since"), 10, 64); err == nil && value > 0 {
		return time.UnixMilli(value)
	}
	return time.Time{}
}

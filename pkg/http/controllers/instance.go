package controllers

import (
	"fmt"
	"net/http"
	"sophus/internal/repo"
	"sophus/internal/repo/instances"
	"sophus/pkg/http/middlewares"
	"sophus/web"
	"strings"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

func Instances(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		fmt.Println(err)
		ctx.StopWithStatus(http.StatusUnauthorized)
		return
	}
	connectionsList, err := repo.GetConnectionListByCompany(agent.CompanyId)
	if err != nil {
		fmt.Println(err)
		ctx.StopWithStatus(http.StatusBadRequest)
		return
	}
	ctx.RenderComponent(web.Instances(connectionsList))
}

func InstancePopup(ctx iris.Context) {
	ctx.RenderComponent(web.InstanceCreatePopup())
}

func NewInstance(ctx iris.Context) {
	if ctx.GetHeader("apitoken") == "" && !middlewares.IsValidJWT(ctx) {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}

	i := instances.InstanceEVO{
		WebhookURL:    uuid.NewString(),
		ConnectionKey: uuid.New(),
	}
	if ctx.GetHeader("apitoken") != "" {
		i.Type = "meta"
	}
	var serveHX bool
	if middlewares.IsValidJWT(ctx) {
		serveHX = true
		agent, err := middlewares.AgentIdentifier(ctx)
		if err != nil {

		}
		fmt.Println(agent, serveHX)

		i.Name = ctx.FormValue("connection_name")
		i.Type = ctx.FormValue("connection_type")
		i.Token = strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	i.Create()
	fmt.Println(i)
}

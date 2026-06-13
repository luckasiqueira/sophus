package controllers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/pkg/http/requests"
	"sophus/utils/env"
	"sophus/web"

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

func NewInstance(ctx iris.Context) {
	body, err := io.ReadAll(ctx.Request().Body)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	instance := repo.InstanceEVO{}
	err = json.Unmarshal(body, &instance)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	instance.InstanceID = uuid.New()
	i := struct {
		Name  string
		Token uuid.UUID
	}{
		Name:  instance.Name,
		Token: instance.InstanceID,
	}
	r := requests.Request{
		URL:     repo.ApiBaseURL + "/instance/create",
		Payload: i,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"apikey":       env.Backend["WPP_API_GLOBAL_TOKEN"],
		},
		Method: "POST",
	}
	err = r.Do()
	if err != nil {
		fmt.Println(err)
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	fmt.Println(string(r.Response.Body))
}

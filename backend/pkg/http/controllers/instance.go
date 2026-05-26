package controllers

import (
	"encoding/json"
	"fmt"
	"io"
	"sophus/backend/internal/repo"
	"sophus/backend/pkg/http/requests"
	"sophus/backend/utils/env"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

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

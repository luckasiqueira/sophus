package controllers

import (
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"

	"github.com/kataras/iris/v12"
)

type flowConnectionDTO struct {
	Id     int    `json:"id"`
	Name   string `json:"name"`
	Number string `json:"number"`
	Status string `json:"status"`
}

// ListConnectionsForFlows returns a safe (no API token / connection key)
// JSON list of this company's WhatsApp connections, used by the flow
// builder's "which connection does this flow run on" dropdown.
func ListConnectionsForFlows(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	connections, err := repo.GetConnectionListByCompany(agent.CompanyId)
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	out := make([]flowConnectionDTO, 0, len(connections))
	for _, c := range connections {
		out = append(out, flowConnectionDTO{Id: c.Id, Name: c.Name, Number: c.Number, Status: c.Status})
	}
	ctx.JSON(iris.Map{"data": out})
}

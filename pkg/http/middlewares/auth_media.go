package middlewares

import (
	"strconv"
	"strings"

	"github.com/kataras/iris/v12"
)

func AuthMediaCompany(ctx iris.Context) {
	agent, err := AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	companyID, err := mediaCompanyID(ctx.Path())
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	if companyID != agent.CompanyId {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	ctx.Next()
}

func mediaCompanyID(mediaPath string) (int, error) {
	path := strings.TrimPrefix(mediaPath, "/medias/")
	companyPart, _, found := strings.Cut(path, "/")
	if !found {
		return 0, strconv.ErrSyntax
	}
	companyID, err := strconv.Atoi(companyPart)
	if err != nil {
		return 0, err
	}
	return companyID, nil
}

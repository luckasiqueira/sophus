package controllers

import (
	"net/http"

	"sophus/internal/media"
	"sophus/pkg/http/middlewares"

	"github.com/kataras/iris/v12"
)

func UploadFlowMedia(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	ctx.Request().Body = http.MaxBytesReader(ctx.ResponseWriter(), ctx.Request().Body, 52<<20)
	file, header, err := ctx.FormFile("file")
	if err != nil {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "arquivo é obrigatório"})
		return
	}
	defer file.Close()

	stored, err := media.Save(agent.CompanyId, ctx.FormValue("kind"), header.Filename, header.Header.Get("Content-Type"), file)
	if err != nil {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": err.Error()})
		return
	}
	ctx.StatusCode(iris.StatusCreated)
	ctx.JSON(iris.Map{"data": stored})
}

func ServeFlowMedia(ctx iris.Context) {
	companyID, err := ctx.Params().GetInt("companyId")
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	filePath, err := media.FilePath(companyID, ctx.Params().Get("file"))
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	ctx.Header("Cache-Control", "public, max-age=86400")
	if err := ctx.ServeFile(filePath); err != nil {
		ctx.StopWithStatus(iris.StatusNotFound)
	}
}

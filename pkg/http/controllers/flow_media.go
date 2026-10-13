package controllers

import (
	"errors"
	"net/http"

	"sophus/internal/media"
	"sophus/internal/repo"

	"github.com/kataras/iris/v12"
)

func UploadFlowMedia(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	ctx.Request().Body = http.MaxBytesReader(ctx.ResponseWriter(), ctx.Request().Body, 52<<20)
	file, header, err := ctx.FormFile("file")
	if err != nil {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "arquivo é obrigatório"})
		return
	}
	defer file.Close()

	kind := ctx.FormValue("kind")
	reserved := media.MaxSize(kind)
	if reserved == 0 {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "tipo de mídia inválido"})
		return
	}
	if err := repo.ReserveCompanyStorage(agent.CompanyId, reserved); err != nil {
		if errors.Is(err, repo.ErrStorageLimitReached) {
			ctx.StopWithJSON(iris.StatusInsufficientStorage, iris.Map{"error": "limite de armazenamento atingido", "code": "storage_quota_exceeded", "upgradeURL": "/settings?reason=storage_quota#settings-billing"})
			return
		}
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": "não foi possível reservar armazenamento"})
		return
	}
	completed := false
	defer func() {
		if !completed {
			_ = repo.ReleaseCompanyStorageReservation(agent.CompanyId, reserved)
		}
	}()
	stored, err := media.Save(agent.CompanyId, kind, header.Filename, header.Header.Get("Content-Type"), file)
	if err != nil {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": err.Error()})
		return
	}
	if err := repo.CompleteCompanyStorageReservation(agent.CompanyId, reserved, stored.Size); err != nil {
		_, _ = media.Delete(agent.CompanyId, media.ScopeFlows, stored.Path)
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": "não foi possível contabilizar a mídia"})
		return
	}
	completed = true
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

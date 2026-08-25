package controllers

import (
	"errors"
	"mime"
	"os"

	"sophus/internal/media"
	"sophus/internal/repo"
	"sophus/web"

	"github.com/kataras/iris/v12"
)

func MediaLibraryPage(ctx iris.Context) {
	if _, ok := requireAdmin(ctx); !ok {
		return
	}
	ctx.RenderComponent(web.MediaLibrary())
}

func ListCompanyMedia(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	items, err := media.List(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar as mídias")
		return
	}
	usage, err := repo.GetCompanyStorageUsage(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível consultar o armazenamento")
		return
	}
	ctx.JSON(iris.Map{"data": iris.Map{"items": items, "usage": usage}})
}

func DownloadCompanyMedia(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	path, err := media.ScopedPath(agent.CompanyId, ctx.Params().Get("scope"), ctx.Params().Get("file"))
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": ctx.Params().Get("file")}))
	if err := ctx.ServeFile(path); err != nil {
		ctx.StopWithStatus(iris.StatusNotFound)
	}
}

func DeleteCompanyMedia(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	scope := ctx.Params().Get("scope")
	fileName := ctx.Params().Get("file")
	if scope == media.ScopeFlows {
		references, err := repo.CountFlowMediaReferences(agent.CompanyId, fileName)
		if err != nil {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível validar o uso da mídia")
			return
		}
		if references > 0 {
			writeJSONError(ctx, iris.StatusConflict, "esta mídia está sendo usada por uma automação ou execução")
			return
		}
	}
	size, err := media.Delete(agent.CompanyId, scope, fileName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSONError(ctx, iris.StatusNotFound, "mídia não encontrada")
			return
		}
		writeJSONError(ctx, iris.StatusBadRequest, "não foi possível excluir a mídia")
		return
	}
	if scope == media.ScopeMessages {
		if err := repo.ClearMessageMediaPath(agent.CompanyId, fileName); err != nil {
			writeJSONError(ctx, iris.StatusInternalServerError, "mídia excluída, mas não foi possível atualizar as mensagens")
			return
		}
	}
	if err := repo.DecrementCompanyStorage(agent.CompanyId, size); err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "mídia excluída, mas não foi possível atualizar o consumo")
		return
	}
	ctx.StatusCode(iris.StatusNoContent)
}

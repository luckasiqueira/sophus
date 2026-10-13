package controllers

import (
	"database/sql"
	"errors"
	"github.com/kataras/iris/v12"
	"net/http"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/web"
)

func StartCompanySupport(ctx iris.Context) {
	admin, ok := requirePlatformAdmin(ctx)
	if !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	token, _, err := repo.StartPlatformSupportSession(admin, id, ctx.RemoteAddr())
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(ctx, iris.StatusNotFound, "empresa ou perfil de acesso não encontrado")
		return
	}
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível iniciar o acesso de suporte")
		return
	}
	ctx.SetCookie(&http.Cookie{Name: middlewares.SupportCookieName, Value: token, Path: "/", HttpOnly: true, Secure: securePlatformCookie(ctx), SameSite: http.SameSiteStrictMode})
	ctx.Header("Cache-Control", "private, no-store")
	ctx.JSON(iris.Map{"data": iris.Map{"redirectURL": "/messages"}})
}

func StopCompanySupport(ctx iris.Context) {
	admin, err := middlewares.SupportSessionOwner(ctx)
	if err != nil {
		writeJSONError(ctx, iris.StatusUnauthorized, "entre novamente no super admin")
		return
	}
	if err := repo.EndPlatformSupportSession(admin, ctx.GetCookie(middlewares.SupportCookieName), ctx.RemoteAddr()); err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível encerrar a sessão de suporte")
		return
	}
	ctx.RemoveCookie(middlewares.SupportCookieName, iris.CookiePath("/"))
	ctx.Header("Cache-Control", "private, no-store")
	redirectRequest(ctx, "/")
}

func GetSupportSession(ctx iris.Context) {
	ctx.Header("Cache-Control", "private, no-store")
	session, ok := middlewares.SupportSession(ctx)
	if !ok {
		ctx.JSON(iris.Map{"data": nil})
		return
	}
	ctx.JSON(iris.Map{"data": iris.Map{"id": session.ID, "companyId": session.CompanyID, "companyName": session.CompanyName, "adminName": session.Admin.Name, "expiresAt": session.ExpiresAt}})
}

func SupportReturnPage(ctx iris.Context) {
	ctx.Header("Cache-Control", "no-store")
	ctx.RenderComponent(web.SupportReturn())
}

func clearSupportSessionForLogin(ctx iris.Context) error {
	if !middlewares.HasSupportCookie(ctx) {
		return nil
	}
	if admin, err := middlewares.SupportSessionOwner(ctx); err == nil {
		if err := repo.EndPlatformSupportSession(admin, ctx.GetCookie(middlewares.SupportCookieName), ctx.RemoteAddr()); err != nil {
			return err
		}
	}
	ctx.RemoveCookie(middlewares.SupportCookieName, iris.CookiePath("/"))
	return nil
}

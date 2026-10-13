package middlewares

import (
	"errors"
	"github.com/kataras/iris/v12"
	"sophus/internal/repo"
	"strings"
)

const SupportCookieName = "platform_support_session"
const supportContextKey = "platformSupportSession"

func HasSupportCookie(ctx iris.Context) bool { return ctx.GetCookie(SupportCookieName) != "" }
func supportExitRequest(ctx iris.Context) bool {
	return ctx.Path() == "/management/api/support/stop" && ctx.Method() == "POST"
}
func SupportSession(ctx iris.Context) (repo.PlatformSupportSession, bool) {
	session, ok := ctx.Values().Get(supportContextKey).(repo.PlatformSupportSession)
	return session, ok
}

// This verifies the original platform session, including its TOTP claim. Only
// exit/logout handlers use it while the company support context is active.
func SupportSessionOwner(ctx iris.Context) (repo.PlatformAdmin, error) {
	return identifyPlatformAdmin(ctx)
}

func identifySupportAgent(ctx iris.Context) (repo.Agent, error) {
	admin, err := identifyPlatformAdmin(ctx)
	if err != nil {
		return repo.Agent{}, err
	}
	session, err := repo.GetPlatformSupportSession(admin, ctx.GetCookie(SupportCookieName))
	if err != nil {
		return repo.Agent{}, err
	}
	ctx.Values().Set(supportContextKey, session)
	return session.Agent, nil
}

func supportAuthenticationFailure(ctx iris.Context) {
	if strings.HasPrefix(ctx.Path(), "/management/api/") {
		ctx.StopWithJSON(iris.StatusUnauthorized, iris.Map{"error": "sessão de suporte expirada; retorne ao painel do super admin", "code": "support_session_expired"})
		return
	}
	ctx.Redirect("/support/return", iris.StatusFound)
}

var errPlatformUnavailableInSupport = errors.New("platform administration is unavailable inside a company support session")

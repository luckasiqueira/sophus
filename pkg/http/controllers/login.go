package controllers

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/utils/env"
	"sophus/web"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/middleware/jwt"
	"golang.org/x/crypto/bcrypt"
)

func Login(ctx iris.Context) {
	count, err := repo.CountPlatformAdmins()
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	if count == 0 {
		ctx.Redirect("/setup", iris.StatusFound)
		return
	}
	ctx.RenderComponent(web.Login())
}

func Home(ctx iris.Context) {
	if _, err := middlewares.PlatformAdminIdentifier(ctx); err == nil {
		ctx.RenderComponent(web.SaaSDashboard())
		return
	}
	ctx.Redirect("/messages", iris.StatusFound)
}

func DoLogin(ctx iris.Context) {
	if !sameOriginRequest(ctx) {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	email := normalizeEmail(ctx.FormValue("email"))
	password := ctx.FormValue("password")
	if !validEmail(email) || password == "" {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	admin, err := repo.GetPlatformAdminByEmail(email)
	if err == nil {
		if bcrypt.CompareHashAndPassword([]byte(admin.Password), []byte(password)) != nil || !admin.IsActive {
			ctx.StopWithStatus(iris.StatusUnauthorized)
			return
		}
		if err := setPlatformSession(ctx, admin); err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
			return
		}
		ctx.RemoveCookie("token", iris.CookiePath("/"))
		ctx.Header("HX-Redirect", "/")
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}

	agent, status, err := checkCredentials(ctx)
	if err != nil {
		ctx.StopWithStatus(status)
		return
	}
	type AgentJWT struct {
		Id      int `json:"id"`
		Version int `json:"version"`
	}
	secret := strings.TrimSpace(env.Backend["SALT_JWT"])
	if secret == "" {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	signer := jwt.NewSigner(jwt.HS256, []byte(secret), (7*24)*time.Hour)
	token, err := signer.Sign(AgentJWT{
		Id:      agent.Id,
		Version: agent.SessionVersion,
	})
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}

	const lifetime = 7 * 24 * time.Hour
	ctx.SetCookie(&http.Cookie{
		Name:     "token",
		Value:    string(token),
		Path:     "/",
		Expires:  time.Now().Add(lifetime),
		MaxAge:   int(lifetime.Seconds()),
		HttpOnly: true,
		Secure:   securePlatformCookie(ctx),
		SameSite: http.SameSiteLaxMode,
	})
	ctx.RemoveCookie(platformCookieName, iris.CookiePath("/"))
	ctx.Header("HX-Redirect", "/")
}

func Logout(ctx iris.Context) {
	if !sameOriginRequest(ctx) {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	ctx.RemoveCookie("token", iris.CookiePath("/"))
	ctx.RemoveCookie(platformCookieName, iris.CookiePath("/"))
	redirectRequest(ctx, "/login")
}

func sameOriginRequest(ctx iris.Context) bool {
	return sameOrigin(ctx.GetHeader("Origin"), ctx.GetHeader("Sec-Fetch-Site"), ctx.Host())
}

func sameOrigin(origin, fetchSite, host string) bool {
	if strings.EqualFold(strings.TrimSpace(fetchSite), "cross-site") {
		return false
	}
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, host)
}

func setPlatformSession(ctx iris.Context, admin repo.PlatformAdmin) error {
	secret := strings.TrimSpace(env.Backend["SALT_JWT"])
	if secret == "" {
		return errors.New("platform JWT secret is empty")
	}
	const lifetime = 7 * 24 * time.Hour
	platformSecret := sha256.Sum256([]byte(secret + ":platform"))
	signer := jwt.NewSigner(jwt.HS256, platformSecret[:], lifetime)
	token, err := signer.Sign(struct {
		Id      int `json:"id"`
		Version int `json:"version"`
	}{Id: admin.Id, Version: admin.SessionVersion})
	if err != nil {
		return err
	}
	ctx.SetCookie(&http.Cookie{
		Name:     platformCookieName,
		Value:    string(token),
		Path:     "/",
		Expires:  time.Now().Add(lifetime),
		MaxAge:   int(lifetime.Seconds()),
		HttpOnly: true,
		Secure:   securePlatformCookie(ctx),
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func checkCredentials(ctx iris.Context) (repo.Agent, int, error) {
	givenEmail := normalizeEmail(ctx.FormValue("email"))
	givenPassword := ctx.FormValue("password")
	agent, err := repo.GetAgentByEmail(givenEmail)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.Agent{}, iris.StatusUnauthorized, err
		}
		return repo.Agent{}, iris.StatusInternalServerError, err
	}
	err = bcrypt.CompareHashAndPassword([]byte(agent.Password), []byte(givenPassword))
	if err != nil {
		return repo.Agent{}, iris.StatusUnauthorized, err
	}
	if !agent.IsActive {
		return repo.Agent{}, iris.StatusUnauthorized, errors.New("agent is inactive")
	}
	companyActive, err := repo.IsCompanyActive(agent.CompanyId)
	if err != nil || !companyActive {
		return repo.Agent{}, iris.StatusUnauthorized, errors.New("company is inactive")
	}
	return agent, iris.StatusOK, nil
}

package controllers

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"sophus/internal/authenticator"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/utils/env"
	"sophus/web"

	"github.com/kataras/iris/v12"
	"github.com/skip2/go-qrcode"
)

const platformChallengeCookieName = "platform_login_challenge"

func beginPlatformSecondFactor(ctx iris.Context, admin repo.PlatformAdmin) error {
	if err := clearSupportSessionForLogin(ctx); err != nil {
		return err
	}
	token, err := repo.StartPlatformLoginChallenge(admin)
	if err != nil {
		return err
	}
	ctx.RemoveCookie(platformCookieName, iris.CookiePath("/"))
	ctx.RemoveCookie("token", iris.CookiePath("/"))
	ctx.RemoveCookie(middlewares.SupportCookieName, iris.CookiePath("/"))
	ctx.SetCookie(&http.Cookie{Name: platformChallengeCookieName, Value: token, Path: "/login/2fa",
		Expires: time.Now().Add(5 * time.Minute), MaxAge: 300, HttpOnly: true, Secure: securePlatformCookie(ctx), SameSite: http.SameSiteStrictMode})
	redirectRequest(ctx, "/login/2fa")
	return nil
}

func PlatformSecondFactorPage(ctx iris.Context) {
	ctx.Header("Cache-Control", "no-store")
	ctx.Header("Referrer-Policy", "no-referrer")
	challenge, err := repo.GetPlatformLoginChallenge(ctx.GetCookie(platformChallengeCookieName))
	if errors.Is(err, repo.ErrInvalidPlatformChallenge) {
		ctx.Redirect("/login", iris.StatusFound)
		return
	}
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	secret, image := "", ""
	if challenge.Enrollment {
		secret, err = authenticator.Decrypt(challenge.SecretCiphertext, strings.TrimSpace(env.Backend["SALT_JWT"]))
		if err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
			return
		}
		png, err := qrcode.Encode(authenticator.URI(secret, challenge.Email), qrcode.Medium, 256)
		if err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
			return
		}
		image = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	}
	ctx.RenderComponent(web.PlatformSecondFactor(challenge.Enrollment, secret, image))
}

func VerifyPlatformSecondFactor(ctx iris.Context) {
	ctx.Header("Cache-Control", "no-store")
	if !sameOriginRequest(ctx) {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	token := ctx.GetCookie(platformChallengeCookieName)
	challenge, err := repo.GetPlatformLoginChallenge(token)
	if err != nil {
		if errors.Is(err, repo.ErrInvalidPlatformChallenge) {
			writeJSONError(ctx, iris.StatusUnauthorized, err.Error())
		} else {
			ctx.StopWithStatus(iris.StatusInternalServerError)
		}
		return
	}
	admin, err := repo.CompletePlatformLoginChallenge(token, strings.TrimSpace(ctx.FormValue("code")))
	if errors.Is(err, repo.ErrInvalidTOTPCode) || errors.Is(err, repo.ErrInvalidPlatformChallenge) {
		recordLoginAttempt(ctx, challenge.Email, nil, &challenge.AdminID, "platform", false)
		writeJSONError(ctx, iris.StatusUnauthorized, err.Error())
		return
	}
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	if err := setPlatformSession(ctx, admin); err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	ctx.RemoveCookie(platformChallengeCookieName, iris.CookiePath("/login/2fa"))
	recordLoginAttempt(ctx, admin.Email, nil, &admin.Id, "platform", true)
	redirectRequest(ctx, "/")
}

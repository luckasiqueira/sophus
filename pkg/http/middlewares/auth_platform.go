package middlewares

import (
	"crypto/sha256"
	"errors"
	"strings"

	"sophus/internal/repo"
	"sophus/utils/env"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/middleware/jwt"
)

const platformAdminContextKey = "authenticatedPlatformAdmin"
const platformCookieName = "platform_token"

func AuthPlatform(ctx iris.Context) {
	admin, err := identifyPlatformAdmin(ctx)
	if err != nil {
		if strings.HasPrefix(ctx.Path(), "/management/api/") {
			ctx.StopWithStatus(iris.StatusUnauthorized)
			return
		}
		ctx.Redirect("/login", iris.StatusFound)
		return
	}
	ctx.Values().Set(platformAdminContextKey, admin)
	ctx.Next()
}

func PlatformAdminIdentifier(ctx iris.Context) (repo.PlatformAdmin, error) {
	if admin, ok := ctx.Values().Get(platformAdminContextKey).(repo.PlatformAdmin); ok {
		return admin, nil
	}
	admin, err := identifyPlatformAdmin(ctx)
	if err != nil {
		return repo.PlatformAdmin{}, err
	}
	ctx.Values().Set(platformAdminContextKey, admin)
	return admin, nil
}

func identifyPlatformAdmin(ctx iris.Context) (repo.PlatformAdmin, error) {
	token := ctx.GetCookie(platformCookieName)
	if token == "" {
		return repo.PlatformAdmin{}, errors.New("platform token is empty")
	}
	secret := strings.TrimSpace(env.Backend["SALT_JWT"])
	if secret == "" {
		return repo.PlatformAdmin{}, errors.New("platform JWT secret is empty")
	}
	platformSecret := sha256.Sum256([]byte(secret + ":platform"))
	jwtToken, err := jwt.Verify(jwt.HS256, platformSecret[:], []byte(token))
	if err != nil {
		return repo.PlatformAdmin{}, errors.New("error decoding platform token")
	}
	var claims struct {
		Id      int `json:"id"`
		Version int `json:"version"`
	}
	if err := jwtToken.Claims(&claims); err != nil || claims.Id <= 0 {
		return repo.PlatformAdmin{}, errors.New("invalid platform claims")
	}
	admin, err := repo.GetPlatformAdminById(claims.Id)
	if err != nil {
		return repo.PlatformAdmin{}, err
	}
	if !admin.IsActive {
		return repo.PlatformAdmin{}, errors.New("platform admin is inactive")
	}
	if claims.Version != admin.SessionVersion {
		return repo.PlatformAdmin{}, errors.New("platform session is no longer valid")
	}
	return admin, nil
}

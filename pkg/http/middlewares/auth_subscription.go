package middlewares

import (
	"strings"
	"time"

	"sophus/internal/repo"

	"github.com/kataras/iris/v12"
)

func AuthSubscription(ctx iris.Context) {
	started := time.Now()
	agent, err := AgentIdentifier(ctx)
	if err != nil {
		return
	}
	if _, support := SupportSession(ctx); support {
		ctx.Next()
		return
	}
	allowed, err := repo.CompanyHasAccess(agent.CompanyId)
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": "não foi possível validar a assinatura"})
		return
	}
	if !allowed {
		usage, usageErr := repo.GetCompanyStorageUsage(agent.CompanyId)
		if usageErr == nil && usage.Exceeded {
			if ctx.Method() == "GET" && !strings.Contains(ctx.GetHeader("Accept"), "application/json") {
				ctx.Redirect("/settings?reason=storage_quota#settings-billing", iris.StatusFound)
				return
			}
			ctx.StopWithJSON(iris.StatusInsufficientStorage, iris.Map{"error": "limite de armazenamento atingido", "code": "storage_quota_exceeded", "usedBytes": usage.UsedBytes, "quotaBytes": usage.StorageLimitBytes, "upgradeURL": "/settings?reason=storage_quota#settings-billing"})
			return
		}
		if ctx.Method() == "GET" && !strings.Contains(ctx.GetHeader("Accept"), "application/json") {
			ctx.Redirect("/settings#settings-billing", iris.StatusFound)
			return
		}
		ctx.StopWithJSON(iris.StatusPaymentRequired, iris.Map{"error": "assinatura vencida ou empresa inativa"})
		return
	}
	ServerTiming(ctx, "access", started)
	ctx.Next()
}

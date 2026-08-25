package middlewares

import (
	"strings"

	"sophus/internal/repo"

	"github.com/kataras/iris/v12"
)

func AuthSubscription(ctx iris.Context) {
	agent, err := AgentIdentifier(ctx)
	if err != nil {
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
				ctx.Redirect("/billing?reason=storage_quota", iris.StatusFound)
				return
			}
			ctx.StopWithJSON(iris.StatusInsufficientStorage, iris.Map{"error": "limite de armazenamento atingido", "code": "storage_quota_exceeded", "usedBytes": usage.UsedBytes, "quotaBytes": usage.StorageLimitBytes, "upgradeURL": "/billing"})
			return
		}
		if ctx.Method() == "GET" && !strings.Contains(ctx.GetHeader("Accept"), "application/json") {
			ctx.Redirect("/billing", iris.StatusFound)
			return
		}
		ctx.StopWithJSON(iris.StatusPaymentRequired, iris.Map{"error": "assinatura vencida ou empresa inativa"})
		return
	}
	ctx.Next()
}

package controllers

import (
	"database/sql"
	"errors"
	"github.com/kataras/iris/v12"
	"sophus/internal/repo"
	"strconv"
)

func ListCompanyAddons(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	listCompanyAddons(ctx, agent.CompanyId)
}
func ListSaaSCompanyAddons(ctx iris.Context) {
	if _, ok := requirePlatformAdmin(ctx); !ok {
		return
	}
	id, err := ctx.Params().GetInt("id")
	if err != nil || id <= 0 {
		writeJSONError(ctx, 400, "empresa inválida")
		return
	}
	listCompanyAddons(ctx, id)
}
func listCompanyAddons(ctx iris.Context, id int) {
	state, err := repo.GetCompanyAddons(id)
	if err != nil {
		writeAddonError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	ctx.JSON(iris.Map{"data": state})
}
func UpdateCompanyAddons(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	if updateCompanyAddons(ctx, agent.CompanyId, "/settings#settings-billing") {
		recordCompanyAction(ctx, agent, "update_addons", "company", strconv.Itoa(agent.CompanyId))
	}
}
func UpdateSaaSCompanyAddons(ctx iris.Context) {
	admin, ok := requirePlatformAdmin(ctx)
	if !ok {
		return
	}
	id, err := ctx.Params().GetInt("id")
	if err != nil || id <= 0 {
		writeJSONError(ctx, 400, "empresa inválida")
		return
	}
	if updateCompanyAddons(ctx, id, "/") {
		if err := repo.RecordAuditEvent(repo.AuditEvent{CompanyID: &id, ActorID: &admin.Id, ActorEmail: admin.Email, ActorType: "platform", Action: "update_addons", TargetType: "company", TargetID: strconv.Itoa(id), IPAddress: ctx.RemoteAddr()}); err != nil {
			ctx.Application().Logger().Errorf("addon management audit failed: company=%d error=%v", id, err)
		}
	}
}
func updateCompanyAddons(ctx iris.Context, id int, returnPath string) bool {
	var input struct {
		ExtraConnections   *int  `json:"extraConnections"`
		ExtraAgents        *int  `json:"extraAgents"`
		ExtraStorageGB     *int  `json:"extraStorageGB"`
		AudioTranscription *bool `json:"audioTranscription"`
	}
	if err := ctx.ReadJSON(&input); err != nil || input.ExtraConnections == nil || input.ExtraAgents == nil || input.ExtraStorageGB == nil || input.AudioTranscription == nil {
		writeJSONError(ctx, 400, "informe todos os adicionais e suas quantidades")
		return false
	}
	quantities := repo.AddonQuantities{ExtraConnections: *input.ExtraConnections, ExtraAgents: *input.ExtraAgents, ExtraStorageGB: *input.ExtraStorageGB, AudioTranscription: *input.AudioTranscription}
	if quantities.Validate() != nil {
		writeJSONError(ctx, 400, "quantidades de adicionais inválidas")
		return false
	}
	companyPayments, err := repo.GetPaymentsByCompany(id)
	if err != nil {
		writeAddonError(ctx, err)
		return false
	}
	if _, err = reconcileOpenAbacatePayPayments(ctx, companyPayments); err != nil {
		writeCheckoutError(ctx, err)
		return false
	}
	provider, err := repo.ResolveCompanyPaymentProvider(id)
	if err != nil {
		writeAddonError(ctx, err)
		return false
	}
	payment, err := repo.RequestCompanyAddonChange(id, quantities, provider)
	if err != nil {
		writeAddonError(ctx, err)
		return false
	}
	if payment.Status != repo.PaymentStatusPaid {
		payment, err = createPaymentCheckout(ctx, id, returnPath)
		if err != nil {
			writeCheckoutError(ctx, err)
			return false
		}
	}
	state, err := repo.GetCompanyAddons(id)
	if err != nil {
		writeAddonError(ctx, err)
		return false
	}
	ctx.JSON(iris.Map{"data": iris.Map{"addons": state, "payment": payment}})
	return true
}
func writeAddonError(ctx iris.Context, err error) {
	switch {
	case errors.Is(err, repo.ErrAddonsNotAllowed):
		writeJSONError(ctx, 403, "o plano atual não permite adicionais; altere o plano antes de contratá-los")
	case errors.Is(err, repo.ErrInvalidAddons):
		writeJSONError(ctx, 400, "quantidades ou preços de adicionais inválidos")
	case errors.Is(err, repo.ErrAddonNoChange):
		writeJSONError(ctx, 409, "estes adicionais já estão contratados")
	case errors.Is(err, repo.ErrOpenPayments):
		writeJSONError(ctx, 409, "conclua ou cancele a cobrança aberta antes de alterar os adicionais")
	case errors.Is(err, repo.ErrPlanLimitBelowUsage):
		writeJSONError(ctx, 409, "não é possível reduzir os adicionais abaixo do uso atual da empresa")
	case errors.Is(err, repo.ErrInvalidSubscriptionAdjustment):
		writeJSONError(ctx, 409, "renove a assinatura antes de contratar adicionais")
	case errors.Is(err, sql.ErrNoRows):
		writeJSONError(ctx, 404, "empresa ou assinatura atual não encontrada")
	default:
		ctx.Application().Logger().Errorf("subscription addons failed: %v", err)
		writeJSONError(ctx, 500, "não foi possível atualizar os adicionais")
	}
}

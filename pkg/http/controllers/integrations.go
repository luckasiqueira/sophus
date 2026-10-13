package controllers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"sophus/internal/repo"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

var (
	whatsAppWidgetPhonePattern = regexp.MustCompile(`^[0-9]{8,15}$`)
	whatsAppWidgetColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	nonPhoneDigits             = regexp.MustCompile(`[^0-9]+`)
)

type whatsAppWidgetView struct {
	repo.WhatsAppWidget
	ScriptURL string `json:"scriptURL"`
	EmbedCode string `json:"embedCode"`
}

func ListWhatsAppWidgets(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	widgets, err := repo.ListWhatsAppWidgets(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar as integrações")
		return
	}
	views := make([]whatsAppWidgetView, 0, len(widgets))
	for _, widget := range widgets {
		views = append(views, whatsAppWidgetResponse(ctx, widget))
	}
	ctx.JSON(iris.Map{"data": views})
}

func CreateWhatsAppWidget(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	var input repo.WhatsAppWidgetInput
	if err := ctx.ReadJSON(&input); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	if message := normalizeWhatsAppWidget(&input); message != "" {
		writeJSONError(ctx, iris.StatusBadRequest, message)
		return
	}
	widget, err := repo.CreateWhatsAppWidget(agent.CompanyId, input)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível criar o botão")
		return
	}
	ctx.StatusCode(iris.StatusCreated)
	ctx.JSON(iris.Map{"data": whatsAppWidgetResponse(ctx, widget)})
}

func UpdateWhatsAppWidget(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	var input repo.WhatsAppWidgetInput
	if err := ctx.ReadJSON(&input); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	if input.Revision <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "revisão inválida")
		return
	}
	if message := normalizeWhatsAppWidget(&input); message != "" {
		writeJSONError(ctx, iris.StatusBadRequest, message)
		return
	}
	widget, err := repo.UpdateWhatsAppWidget(id, agent.CompanyId, input)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrWhatsAppWidgetConflict):
			writeJSONError(ctx, iris.StatusConflict, "este botão foi alterado em outra sessão; recarregue a página")
		case errors.Is(err, sql.ErrNoRows):
			writeJSONError(ctx, iris.StatusNotFound, "botão não encontrado")
		default:
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível atualizar o botão")
		}
		return
	}
	ctx.JSON(iris.Map{"data": whatsAppWidgetResponse(ctx, widget)})
}

func DeleteWhatsAppWidget(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	deleted, err := repo.DeleteWhatsAppWidget(id, agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível excluir o botão")
		return
	}
	if !deleted {
		writeJSONError(ctx, iris.StatusNotFound, "botão não encontrado")
		return
	}
	ctx.StatusCode(iris.StatusNoContent)
}

func ServeWhatsAppWidget(ctx iris.Context) {
	publicID, err := uuid.Parse(ctx.Params().Get("publicId"))
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	widget, err := repo.GetActiveWhatsAppWidget(publicID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			ctx.StopWithStatus(iris.StatusNotFound)
			return
		}
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	script := whatsAppWidgetScript(widget)
	ctx.Header("Cache-Control", "public, max-age=300")
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.ContentType("application/javascript; charset=utf-8")
	_, _ = ctx.WriteString(script)
}

func whatsAppWidgetScript(widget repo.WhatsAppWidget) string {
	configuration, _ := json.Marshal(map[string]interface{}{
		"id": widget.PublicId.String(), "number": widget.PhoneNumber,
		"text": widget.CallToAction, "color": widget.Color, "iconOnly": widget.IconOnly,
	})
	return fmt.Sprintf(`(()=>{const c=%s;if(document.getElementById("sophus-wa-"+c.id))return;const h=document.createElement("div");h.id="sophus-wa-"+c.id;h.style.cssText="position:fixed;right:24px;bottom:24px;z-index:2147483000";const r=h.attachShadow({mode:"closed"});const a=document.createElement("a");a.href="https://wa.me/"+c.number;a.target="_blank";a.rel="noopener noreferrer";a.setAttribute("aria-label",c.text);a.style.cssText="display:flex;align-items:center;border-radius:999px;background:"+c.color+";color:#fff;text-decoration:none;font:700 14px/1.2 Arial,sans-serif;box-shadow:0 12px 32px rgba(0,0,0,.28);transition:transform .2s ease,box-shadow .2s ease;"+(c.iconOnly?"width:56px;height:56px;justify-content:center;padding:0":"gap:10px;min-height:56px;padding:0 18px");a.onmouseenter=()=>{a.style.transform="translateY(-2px)";a.style.boxShadow="0 16px 38px rgba(0,0,0,.34)"};a.onmouseleave=()=>{a.style.transform="";a.style.boxShadow="0 12px 32px rgba(0,0,0,.28)"};a.innerHTML='<svg width="25" height="25" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M12.04 2a9.84 9.84 0 0 0-8.44 14.9L2 22l5.23-1.54A9.98 9.98 0 1 0 12.04 2Zm5.77 14.08c-.24.68-1.4 1.3-1.94 1.36-.5.05-1.13.08-1.82-.14-.42-.13-.96-.31-1.66-.61-2.92-1.26-4.82-4.2-4.97-4.4-.14-.2-1.18-1.57-1.18-3s.75-2.13 1.02-2.42c.27-.29.59-.36.78-.36h.57c.18 0 .43-.07.67.51.24.58.82 2 .89 2.15.07.14.12.31.02.51-.09.2-.14.31-.29.48-.14.17-.3.38-.43.51-.14.14-.29.3-.12.59.17.29.76 1.25 1.63 2.02 1.12 1 2.06 1.31 2.35 1.46.29.14.46.12.63-.07.17-.2.72-.84.91-1.13.19-.29.39-.24.65-.14.27.1 1.69.8 1.98.94.29.14.48.22.55.34.07.12.07.7-.17 1.38Z"/></svg>';if(!c.iconOnly){const s=document.createElement("span");s.textContent=c.text;a.appendChild(s)}r.appendChild(a);document.body.appendChild(h)})();`, configuration)
}

func normalizeWhatsAppWidget(input *repo.WhatsAppWidgetInput) string {
	input.Name = strings.TrimSpace(input.Name)
	input.PhoneNumber = nonPhoneDigits.ReplaceAllString(input.PhoneNumber, "")
	input.CallToAction = strings.TrimSpace(input.CallToAction)
	input.Color = strings.ToUpper(strings.TrimSpace(input.Color))
	if input.Name == "" || len(input.Name) > 80 {
		return "informe um nome de até 80 caracteres para identificar o botão"
	}
	if !whatsAppWidgetPhonePattern.MatchString(input.PhoneNumber) {
		return "informe o número com DDI e DDD, contendo de 8 a 15 dígitos"
	}
	if input.CallToAction == "" || len(input.CallToAction) > 80 {
		return "o texto de chamada deve ter entre 1 e 80 caracteres"
	}
	if !whatsAppWidgetColorPattern.MatchString(input.Color) {
		return "cor inválida"
	}
	return ""
}

func whatsAppWidgetResponse(ctx iris.Context, widget repo.WhatsAppWidget) whatsAppWidgetView {
	baseURL, err := applicationPublicURL()
	if err != nil {
		baseURL = strings.TrimRight(ctx.Scheme()+"://"+ctx.Host(), "/")
	}
	scriptURL := baseURL + "/integrations/whatsapp/" + widget.PublicId.String() + "/widget.js"
	return whatsAppWidgetView{WhatsAppWidget: widget, ScriptURL: scriptURL,
		EmbedCode: `<script async src="` + scriptURL + `"></script>`}
}

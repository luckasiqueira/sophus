package controllers

import (
	"database/sql"
	"errors"
	"fmt"
	"html"
	"log"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/pkg/http/middlewares/sse"
	"sophus/web"

	"github.com/kataras/iris/v12"
)

func ContactAvatar(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	contactID, err := ctx.Params().GetInt("id")
	if err != nil || contactID <= 0 {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	contact, err := repo.GetAvatarContact(contactID)
	if err != nil {
		ctx.StopWithStatus(iris.StatusNotFound)
		return
	}
	if contact.CompanyId != agent.CompanyId {
		ctx.StopWithStatus(iris.StatusNotFound)
		return
	}
	serveContactAvatar(ctx, contact, ctx.URLParam("fallback") == "1")
}

func serveContactAvatar(ctx iris.Context, contact repo.Contact, fallback bool) {
	ctx.Header("Vary", "Cookie")
	if !fallback && validAvatarURL(contact.AvatarURL) {
		// WhatsApp URLs expire and can change after a profile-picture update.
		ctx.Header("Cache-Control", "private, max-age=60")
		ctx.Redirect(contact.AvatarURL, iris.StatusFound)
		return
	}
	initial := "?"
	if name := strings.TrimSpace(contact.Name); name != "" {
		initial = strings.ToUpper(string([]rune(name)[0]))
	} else if number := strings.TrimSpace(contact.Number); number != "" {
		initial = string([]rune(number)[0])
	}
	ctx.Header("Cache-Control", "private, max-age=30")
	ctx.ContentType("image/svg+xml")
	_, _ = ctx.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="96" height="96" viewBox="0 0 96 96"><rect width="96" height="96" rx="48" fill="#ede9fe"/><text x="48" y="57" text-anchor="middle" font-family="Arial,sans-serif" font-size="34" font-weight="700" fill="#6d28d9">%s</text></svg>`, html.EscapeString(initial)))
}

func RefreshContactAvatar(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	contactID, err := ctx.Params().GetInt("id")
	if err != nil || contactID <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "contato inválido")
		return
	}
	contact, err := repo.GetContactById(contactID)
	if err != nil || contact.CompanyId != agent.CompanyId {
		writeJSONError(ctx, iris.StatusNotFound, "contato não encontrado")
		return
	}
	updated, err := repo.InvalidateContactAvatar(ctx.Request().Context(), agent.CompanyId, contactID)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível solicitar uma nova foto")
		return
	}
	if updated {
		sse.NotifyContactAvatar(agent.CompanyId, contactID)
	}
	ctx.StatusCode(iris.StatusNoContent)
}

func validAvatarURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}

func ContactsPage(ctx iris.Context) {
	if _, ok := requireAdmin(ctx); !ok {
		return
	}
	ctx.RenderComponent(web.Contacts())
}

func ListContactTags(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	tags, err := repo.ListCompanyTags(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar as tags")
		return
	}
	ctx.JSON(iris.Map{"data": tags})
}

func CreateContactTag(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	var body struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if !validContactTagName(body.Name) {
		writeJSONError(ctx, iris.StatusBadRequest, "a tag deve ter entre 1 e 80 caracteres")
		return
	}
	if body.Color != "" && !repo.ValidContactTagColor(body.Color) {
		writeJSONError(ctx, iris.StatusBadRequest, "cor da tag inválida")
		return
	}
	tag, err := repo.CreateCompanyTag(agent.CompanyId, body.Name, body.Color)
	if err != nil {
		log.Printf("contact tag create failed: company=%d error=%v", agent.CompanyId, err)
		if isUniqueViolation(err) {
			writeJSONError(ctx, iris.StatusConflict, "já existe uma tag com este nome")
		} else {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível criar a tag")
		}
		return
	}
	ctx.StatusCode(iris.StatusCreated)
	ctx.JSON(iris.Map{"data": tag})
}

func RenameContactTag(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	parsedTagID, err := ctx.Params().GetInt("id")
	tagID := int64(parsedTagID)
	if err != nil || tagID <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "tag inválida")
		return
	}
	var body struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if !validContactTagName(body.Name) {
		writeJSONError(ctx, iris.StatusBadRequest, "a tag deve ter entre 1 e 80 caracteres")
		return
	}
	if body.Color != "" && !repo.ValidContactTagColor(body.Color) {
		writeJSONError(ctx, iris.StatusBadRequest, "cor da tag inválida")
		return
	}
	tag, err := repo.UpdateCompanyTag(agent.CompanyId, tagID, body.Name, body.Color)
	if err != nil {
		log.Printf("contact tag rename failed: company=%d tag=%d error=%v", agent.CompanyId, tagID, err)
		if errors.Is(err, repo.ErrTagInUseByFlow) {
			writeJSONError(ctx, iris.StatusConflict, "esta tag está sendo usada por um flow")
		} else if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(ctx, iris.StatusNotFound, "tag não encontrada")
		} else if isUniqueViolation(err) {
			writeJSONError(ctx, iris.StatusConflict, "já existe uma tag com este nome")
		} else {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível renomear a tag")
		}
		return
	}
	ctx.JSON(iris.Map{"data": tag})
}

func DeleteContactTag(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	parsedTagID, err := ctx.Params().GetInt("id")
	tagID := int64(parsedTagID)
	if err != nil || tagID <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "tag inválida")
		return
	}
	err = repo.DeleteCompanyTag(agent.CompanyId, tagID)
	if err != nil {
		if errors.Is(err, repo.ErrTagInUseByFlow) {
			writeJSONError(ctx, iris.StatusConflict, "esta tag está sendo usada por um flow")
		} else if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(ctx, iris.StatusNotFound, "tag não encontrada")
		} else {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível excluir a tag")
		}
		return
	}
	ctx.StatusCode(iris.StatusNoContent)
}

func SetCompanyContactTags(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	contactID, err := ctx.Params().GetInt("id")
	if err != nil || contactID <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "contato inválido")
		return
	}
	var body struct {
		TagIds []int64 `json:"tagIds"`
	}
	if err := ctx.ReadJSON(&body); err != nil || len(body.TagIds) > 100 {
		writeJSONError(ctx, iris.StatusBadRequest, "tags inválidas")
		return
	}
	seen := map[int64]bool{}
	for _, id := range body.TagIds {
		if id <= 0 || seen[id] {
			writeJSONError(ctx, iris.StatusBadRequest, "tags inválidas")
			return
		}
		seen[id] = true
	}
	if err := repo.SetContactTags(agent.CompanyId, contactID, body.TagIds); err != nil {
		log.Printf("contact tags update failed: company=%d contact=%d error=%v", agent.CompanyId, contactID, err)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(ctx, iris.StatusNotFound, "contato ou tag não encontrada")
		} else {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível atualizar as tags")
		}
		return
	}
	ctx.StatusCode(iris.StatusNoContent)
}

func UpdateCompanyContact(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	var body struct {
		Name    string `json:"name"`
		Number  string `json:"number"`
		Email   string `json:"email"`
		Company string `json:"company"`
	}
	if err := ctx.ReadJSON(&body); err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "JSON inválido")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.Number = nonPhoneDigits.ReplaceAllString(body.Number, "")
	body.Email = strings.ToLower(strings.TrimSpace(body.Email))
	body.Company = strings.TrimSpace(body.Company)
	if !utf8.ValidString(body.Name) || body.Name == "" || utf8.RuneCountInString(body.Name) > 160 || strings.ContainsRune(body.Name, 0) {
		writeJSONError(ctx, iris.StatusBadRequest, "informe um nome de até 160 caracteres")
		return
	}
	if len(body.Number) < 8 || len(body.Number) > 20 {
		writeJSONError(ctx, iris.StatusBadRequest, "informe um telefone válido com DDI e DDD")
		return
	}
	if !utf8.ValidString(body.Company) || utf8.RuneCountInString(body.Company) > 160 || strings.ContainsRune(body.Company, 0) {
		writeJSONError(ctx, iris.StatusBadRequest, "a empresa deve possuir até 160 caracteres")
		return
	}
	if body.Email != "" {
		address, err := mail.ParseAddress(body.Email)
		if err != nil || !strings.EqualFold(address.Address, body.Email) || len(body.Email) > 320 {
			writeJSONError(ctx, iris.StatusBadRequest, "informe um email válido")
			return
		}
	}
	err := repo.UpdateCompanyContact(id, agent.CompanyId, repo.ContactUpdateInput{
		Name: body.Name, Number: body.Number, Email: body.Email, Company: body.Company,
	})
	if err != nil {
		log.Printf("contact update failed: company=%d contact=%d error=%v", agent.CompanyId, id, err)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(ctx, iris.StatusNotFound, "contato não encontrado")
		} else if isUniqueViolation(err) {
			writeJSONError(ctx, iris.StatusConflict, "já existe um contato com este telefone nesta empresa")
		} else {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível atualizar o contato")
		}
		return
	}
	ctx.StatusCode(iris.StatusNoContent)
}

func validContactTagName(name string) bool {
	return utf8.ValidString(name) && utf8.RuneCountInString(name) >= 1 && utf8.RuneCountInString(name) <= 80 && !strings.ContainsRune(name, 0)
}

func DeleteCompanyContact(ctx iris.Context) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	id, ok := routeID(ctx)
	if !ok {
		return
	}
	if err := repo.DeleteCompanyContact(id, agent.CompanyId); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(ctx, iris.StatusNotFound, "contato não encontrado")
		} else {
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível excluir o contato")
		}
		return
	}
	sse.NotifyConversations(agent.CompanyId)
	ctx.StatusCode(iris.StatusNoContent)
}

func ListContactPickerConnections(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	connections, err := repo.ListCompanyConnectionOptions(agent.CompanyId, true)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível listar as conexões")
		return
	}
	ctx.JSON(iris.Map{"data": connections})
}

func ListCompanyContacts(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	query := strings.TrimSpace(ctx.URLParam("q"))
	if len(query) > 120 {
		writeJSONError(ctx, iris.StatusBadRequest, "pesquisa muito longa")
		return
	}
	var connectionID *int
	if raw := strings.TrimSpace(ctx.URLParam("connectionId")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			writeJSONError(ctx, iris.StatusBadRequest, "conexão inválida")
			return
		}
		connectionID = &value
	}
	var tagID *int64
	if raw := strings.TrimSpace(ctx.URLParam("tagId")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			writeJSONError(ctx, iris.StatusBadRequest, "tag inválida")
			return
		}
		tagID = &value
	}
	page := 1
	if raw := strings.TrimSpace(ctx.URLParam("page")); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 {
			writeJSONError(ctx, iris.StatusBadRequest, "página inválida")
			return
		}
	}
	contacts, err := repo.SearchCompanyContacts(agent.CompanyId, connectionID, tagID, query, page, 50)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível pesquisar os contatos")
		return
	}
	ctx.JSON(contacts)
}

func OpenContactConversation(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	allowed, err := repo.CompanyHasAccess(agent.CompanyId)
	if err != nil {
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível validar o acesso da empresa")
		return
	}
	if !allowed {
		writeJSONError(ctx, iris.StatusPaymentRequired, "a operação da empresa está indisponível; verifique o plano e o armazenamento")
		return
	}
	var body struct {
		ContactId    int `json:"contactId"`
		ConnectionId int `json:"connectionId"`
	}
	if err := ctx.ReadJSON(&body); err != nil || body.ContactId <= 0 || body.ConnectionId <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "contato e conexão são obrigatórios")
		return
	}
	result, err := repo.StartOrReuseConversation(agent, body.ContactId, body.ConnectionId)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrConversationUnavailable):
			writeJSONError(ctx, iris.StatusConflict, "o contato está indisponível nesta conexão ou já é atendido por outro agente")
		case errors.Is(err, sql.ErrNoRows):
			writeJSONError(ctx, iris.StatusNotFound, "contato ou conexão não encontrado")
		default:
			writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível abrir a conversa")
		}
		return
	}
	sse.NotifyConversations(agent.CompanyId)
	ctx.StatusCode(iris.StatusCreated)
	if result.Reused || result.Reopened {
		ctx.StatusCode(iris.StatusOK)
	}
	ctx.JSON(iris.Map{"data": result})
}

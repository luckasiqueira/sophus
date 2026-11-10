package flowengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"sophus/internal/llm"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares/sse"
)

// The AI agent block accepts two sub-blocks on its bottom handles: a
// knowledge base that extends the agent's own, and a list of actions the
// model can call as tools. Both are captured into the session when the flow
// hands the conversation over.

const (
	aiKnowledgeHandle      = "knowledge"
	aiToolsHandle          = "tools"
	maxAIKnowledgeBytes    = 500000
	maxAIActions           = 15
	maxAIActionParameters  = 15
	maxAIActionHeaders     = 20
	maxAIToolResultBytes   = 8000
	defaultAIActionTimeout = 15000
	maxAIActionTimeout     = 60000
)

var aiActionParameterPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

type AIAgentExtensions struct {
	KnowledgeBase string     `json:"knowledgeBase,omitempty"`
	Actions       []AIAction `json:"actions,omitempty"`
}

// AIAction is one action of the "Ações da IA" block. Type is transfer, close,
// tag, contactField or http; the other fields apply to the matching type.
type AIAction struct {
	ID          string              `json:"id"`
	Type        string              `json:"type"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	AssignType  string              `json:"assignType,omitempty"`
	AssignID    int                 `json:"assignId,omitempty"`
	Message     string              `json:"message,omitempty"`
	Tag         string              `json:"tag,omitempty"`
	Field       string              `json:"field,omitempty"`
	FieldKey    string              `json:"fieldKey,omitempty"`
	Method      string              `json:"method,omitempty"`
	URL         string              `json:"url,omitempty"`
	Headers     []AIActionHeader    `json:"headers,omitempty"`
	Parameters  []AIActionParameter `json:"parameters,omitempty"`
	Timeout     int                 `json:"timeout,omitempty"`
}

type AIActionHeader struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type AIActionParameter struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

func decodeAIActions(data map[string]interface{}) ([]AIAction, error) {
	raw, ok := data["actions"]
	if !ok || raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var actions []AIAction
	if err := json.Unmarshal(encoded, &actions); err != nil {
		return nil, errors.New("ações da IA são inválidas")
	}
	return actions, nil
}

func validateAIKnowledgeNode(data map[string]interface{}) error {
	content := strings.TrimSpace(stringVal(data, "content"))
	if content == "" || len(content) > maxAIKnowledgeBytes {
		return fmt.Errorf("base de conhecimento deve possuir entre 1 e %d caracteres", maxAIKnowledgeBytes)
	}
	return nil
}

func validateAIToolsNode(data map[string]interface{}) error {
	actions, err := decodeAIActions(data)
	if err != nil {
		return err
	}
	if len(actions) == 0 || len(actions) > maxAIActions {
		return fmt.Errorf("cadastre entre 1 e %d ações", maxAIActions)
	}
	for index, action := range actions {
		if err := validateAIAction(action); err != nil {
			return fmt.Errorf("ação %d: %w", index+1, err)
		}
	}
	return nil
}

func validateAIAction(action AIAction) error {
	name := strings.TrimSpace(action.Name)
	if name == "" || len(name) > 80 {
		return errors.New("nome deve possuir entre 1 e 80 caracteres")
	}
	if strings.TrimSpace(action.Description) == "" || len(action.Description) > 1000 {
		return errors.New("descreva quando a IA deve usar a ação (até 1000 caracteres)")
	}
	if len(action.Message) > 10000 {
		return errors.New("mensagem excede o limite de 10000 caracteres")
	}
	switch action.Type {
	case "transfer":
		if action.AssignType != "agent" && action.AssignType != "department" {
			return errors.New("escolha transferir para um agente ou setor")
		}
		if action.AssignID <= 0 {
			return errors.New("destino da transferência é obrigatório")
		}
	case "close":
	case "tag":
		if tag := strings.TrimSpace(action.Tag); tag == "" || len(tag) > 80 {
			return errors.New("tag deve possuir entre 1 e 80 caracteres")
		}
	case "contactField":
		switch action.Field {
		case "name", "email":
		case "custom":
			if !flowVariablePattern.MatchString(strings.TrimSpace(action.FieldKey)) {
				return errors.New("chave do campo personalizado inválida")
			}
		default:
			return errors.New("campo do contato inválido")
		}
	case "http":
		method := strings.ToUpper(strings.TrimSpace(action.Method))
		if method == "" {
			method = "POST"
		}
		if !allowedHTTPMethod(method) {
			return errors.New("método HTTP não permitido")
		}
		if target := strings.TrimSpace(action.URL); target == "" || len(target) > 4096 {
			return errors.New("URL é obrigatória e deve possuir até 4096 caracteres")
		}
		if action.Timeout != 0 && (action.Timeout < 100 || action.Timeout > maxAIActionTimeout) {
			return fmt.Errorf("timeout deve estar entre 100 e %d ms", maxAIActionTimeout)
		}
		if len(action.Headers) > maxAIActionHeaders {
			return fmt.Errorf("até %d cabeçalhos", maxAIActionHeaders)
		}
		for _, header := range action.Headers {
			key := strings.TrimSpace(header.Key)
			if key == "" {
				continue
			}
			if !validHTTPHeaderName(key) || blockedHTTPHeader(key) || len(header.Value) > 4096 {
				return fmt.Errorf("cabeçalho %q inválido", key)
			}
		}
		if len(action.Parameters) > maxAIActionParameters {
			return fmt.Errorf("até %d parâmetros", maxAIActionParameters)
		}
		seen := map[string]bool{}
		for _, parameter := range action.Parameters {
			parameterName := strings.TrimSpace(parameter.Name)
			if !aiActionParameterPattern.MatchString(parameterName) {
				return fmt.Errorf("parâmetro %q deve começar com letra e usar letras, números ou _", parameter.Name)
			}
			if seen[parameterName] {
				return fmt.Errorf("parâmetro %q repetido", parameterName)
			}
			seen[parameterName] = true
			if len(parameter.Description) > 500 {
				return fmt.Errorf("descrição do parâmetro %q excede 500 caracteres", parameterName)
			}
		}
	default:
		return errors.New("tipo de ação inválido")
	}
	return nil
}

// validateAIAgentEdge keeps the knowledge and action sub-blocks attached only
// to the matching handle of an AI agent block.
func validateAIAgentEdge(edge FlowEdge, source, target FlowNode) error {
	if source.Type == NodeAIKnowledge || source.Type == NodeAITools {
		return errors.New("blocos de base de conhecimento e ações não possuem saídas")
	}
	if source.Type == NodeAIAgent {
		switch {
		case edge.SourceHandle == aiKnowledgeHandle && target.Type == NodeAIKnowledge:
		case edge.SourceHandle == aiToolsHandle && target.Type == NodeAITools:
		default:
			return errors.New("o Agente IA conecta somente uma Base de Conhecimento e um bloco de Ações da IA")
		}
		return nil
	}
	if target.Type == NodeAIKnowledge || target.Type == NodeAITools {
		return errors.New("base de conhecimento e ações só podem ser conectadas a um Agente IA")
	}
	return nil
}

// collectAIAgentExtensions reads the sub-blocks connected to an AI agent
// block, resolving flow variables now: the session outlives the execution.
// Action parameters keep their {{placeholders}} for the model to fill.
func collectAIAgentExtensions(node FlowNode, flowData FlowData, ctx ExecutionContext) AIAgentExtensions {
	var extensions AIAgentExtensions
	for _, edge := range flowData.Edges {
		if edge.Source != node.ID {
			continue
		}
		target := findNode(flowData.Nodes, edge.Target)
		if target == nil {
			continue
		}
		switch {
		case edge.SourceHandle == aiKnowledgeHandle && target.Type == NodeAIKnowledge:
			extensions.KnowledgeBase = strings.TrimSpace(ReplaceVariables(stringVal(target.Data, "content"), ctx))
		case edge.SourceHandle == aiToolsHandle && target.Type == NodeAITools:
			actions, err := decodeAIActions(target.Data)
			if err != nil {
				log.Printf("ai agent actions skipped: node=%s error=%v", target.ID, err)
				continue
			}
			for _, action := range actions {
				actionContext := make(ExecutionContext, len(ctx))
				for key, value := range ctx {
					actionContext[key] = value
				}
				for _, parameter := range action.Parameters {
					delete(actionContext, strings.TrimSpace(parameter.Name))
				}
				action.Message = ReplaceVariables(action.Message, actionContext)
				action.URL = ReplaceVariables(action.URL, actionContext)
				for index := range action.Headers {
					action.Headers[index].Value = ReplaceVariables(action.Headers[index].Value, actionContext)
				}
				extensions.Actions = append(extensions.Actions, action)
			}
		}
	}
	return extensions
}

func parseAIAgentExtensions(raw json.RawMessage) AIAgentExtensions {
	var extensions AIAgentExtensions
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &extensions); err != nil {
			log.Printf("ai agent session extensions ignored: error=%v", err)
		}
	}
	return extensions
}

// aiToolName builds a provider-safe function name (^[a-zA-Z0-9_-]{1,64}$).
func aiToolName(index int, name string) string {
	var slug strings.Builder
	underscore := false
	for _, r := range norm.NFD.String(strings.ToLower(name)) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			slug.WriteRune(r)
			underscore = false
		case !underscore && slug.Len() > 0:
			slug.WriteByte('_')
			underscore = true
		}
	}
	result := fmt.Sprintf("acao_%d_%s", index+1, strings.Trim(slug.String(), "_"))
	result = strings.TrimRight(result, "_")
	if len(result) > 64 {
		result = strings.TrimRight(result[:64], "_")
	}
	return result
}

func stringProperty(description string) map[string]interface{} {
	return map[string]interface{}{"type": "string", "description": description}
}

func objectSchema(properties map[string]interface{}, required []string) map[string]interface{} {
	schema := map[string]interface{}{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// aiActionTools turns the session actions into tool definitions, indexed by
// tool name.
func aiActionTools(actions []AIAction) ([]llm.ToolDefinition, map[string]AIAction) {
	tools := make([]llm.ToolDefinition, 0, len(actions))
	byName := make(map[string]AIAction, len(actions))
	for index, action := range actions {
		name := aiToolName(index, action.Name)
		description := strings.TrimSpace(action.Name) + ": " + strings.TrimSpace(action.Description)
		var parameters map[string]interface{}
		switch action.Type {
		case "transfer":
			description += "\nTransfere o atendimento para a equipe humana e encerra sua participação. Depois de usar, escreva somente uma mensagem curta avisando o cliente."
			parameters = objectSchema(map[string]interface{}{"resumo": stringProperty("Resumo objetivo da solicitação do cliente para o atendente humano.")}, []string{"resumo"})
		case "close":
			description += "\nEncerra o atendimento. Use apenas quando o cliente não precisar de mais nada. Depois de usar, escreva somente uma despedida curta."
			parameters = objectSchema(map[string]interface{}{"resumo": stringProperty("Resumo objetivo do atendimento.")}, nil)
		case "tag":
			description += "\nAdiciona a etiqueta \"" + strings.TrimSpace(action.Tag) + "\" ao cliente."
			parameters = objectSchema(map[string]interface{}{}, nil)
		case "contactField":
			label := map[string]string{"name": "o nome do cliente", "email": "o email do cliente"}[action.Field]
			if label == "" {
				label = "o campo \"" + action.FieldKey + "\" do cliente"
			}
			description += "\nSalva " + label + " no cadastro. Use somente com um valor informado pelo cliente."
			parameters = objectSchema(map[string]interface{}{"valor": stringProperty("Valor a salvar em " + label + ".")}, []string{"valor"})
		case "http":
			properties := map[string]interface{}{}
			var required []string
			for _, parameter := range action.Parameters {
				parameterName := strings.TrimSpace(parameter.Name)
				properties[parameterName] = stringProperty(strings.TrimSpace(parameter.Description))
				if parameter.Required {
					required = append(required, parameterName)
				}
			}
			description += "\nConsulta um sistema externo e retorna a resposta dele."
			parameters = objectSchema(properties, required)
		default:
			continue
		}
		tools = append(tools, llm.ToolDefinition{Name: name, Description: description, Parameters: parameters})
		byName[name] = action
	}
	return tools, byName
}

// aiToolRunner executes the tools of one AI agent answer. Transfers and
// closings are only validated here and run after the reply is sent, so the
// customer reads the AI's last message before the conversation changes hands.
type aiToolRunner struct {
	session      repo.AIAgentSession
	conversation repo.Conversation
	connection   repo.ConnectionEVO
	actions      map[string]AIAction
	messenger    *Messenger
	pending      *pendingAIAction
}

type pendingAIAction struct {
	action  AIAction
	summary string
}

func newAIToolRunner(session repo.AIAgentSession, conversation repo.Conversation, connection repo.ConnectionEVO, actions map[string]AIAction) *aiToolRunner {
	return &aiToolRunner{session: session, conversation: conversation, connection: connection, actions: actions, messenger: NewMessenger(connection)}
}

func argumentText(arguments map[string]interface{}, name string) string {
	value, ok := arguments[name]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	encoded, _ := json.Marshal(value)
	return strings.TrimSpace(string(encoded))
}

func (runner *aiToolRunner) Execute(_ context.Context, call llm.ToolCall) string {
	action, ok := runner.actions[call.Name]
	if !ok {
		return "Erro: ação desconhecida."
	}
	companyID := runner.connection.CompanyID
	result, err := runner.run(action, call.Arguments)
	if err != nil {
		log.Printf("ai agent action failed: conversation=%d agent=%d action=%s type=%s error=%v", runner.conversation.Id, runner.session.Agent.Id, action.ID, action.Type, err)
		return "Erro ao executar a ação: " + err.Error()
	}
	log.Printf("ai agent action executed: conversation=%d company=%d agent=%d action=%s type=%s", runner.conversation.Id, companyID, runner.session.Agent.Id, action.ID, action.Type)
	return result
}

func (runner *aiToolRunner) run(action AIAction, arguments map[string]interface{}) (string, error) {
	companyID := runner.connection.CompanyID
	switch action.Type {
	case "transfer", "close":
		if runner.pending != nil {
			return "", errors.New("já existe uma transferência ou encerramento programado para esta resposta")
		}
		if action.Type == "transfer" {
			if err := repo.ValidateAIAgentTransferTarget(companyID, action.AssignType, action.AssignID); err != nil {
				return "", err
			}
		}
		runner.pending = &pendingAIAction{action: action, summary: argumentText(arguments, "resumo")}
		if action.Type == "transfer" {
			return "Transferência confirmada; será concluída após sua resposta. Escreva agora somente uma mensagem curta avisando o cliente.", nil
		}
		return "Encerramento confirmado; será concluído após sua resposta. Escreva agora somente uma despedida curta.", nil
	case "tag":
		if _, err := repo.AddAIAgentConversationTag(runner.session.Id, runner.conversation.Id, companyID, action.Tag); err != nil {
			return "", err
		}
		sse.NotifyConversations(companyID)
		return "Etiqueta adicionada.", nil
	case "contactField":
		value := argumentText(arguments, "valor")
		if value == "" || len(value) > 320 {
			return "", errors.New("informe um valor com até 320 caracteres")
		}
		var err error
		switch action.Field {
		case "email":
			address, parseErr := mail.ParseAddress(value)
			if parseErr != nil || address.Address != value {
				return "", errors.New("email inválido; peça ao cliente para confirmar")
			}
			err = repo.UpdateAIAgentContact(runner.session.Id, runner.conversation.Id, companyID, "email", value)
		case "name":
			err = repo.UpdateAIAgentContact(runner.session.Id, runner.conversation.Id, companyID, "name", value)
		default:
			err = repo.SetAIAgentContactCustomField(runner.session.Id, runner.conversation.Id, companyID, strings.TrimSpace(action.FieldKey), value)
		}
		if err != nil {
			return "", err
		}
		sse.NotifyConversations(companyID)
		return "Dado salvo no cadastro do cliente.", nil
	case "http":
		return runner.runHTTP(action, arguments)
	}
	return "", errors.New("tipo de ação inválido")
}

func (runner *aiToolRunner) actionContext(arguments map[string]interface{}) ExecutionContext {
	ctx := ExecutionContext{
		"conversationId": runner.conversation.Id,
		"contactName":    runner.conversation.Contact.Name,
		"contactNumber":  runner.conversation.Contact.Number,
		"contactEmail":   runner.conversation.Contact.Email,
	}
	for key, value := range arguments {
		ctx[key] = argumentText(arguments, key)
		if value == nil {
			ctx[key] = ""
		}
	}
	return ctx
}

func (runner *aiToolRunner) runHTTP(action AIAction, arguments map[string]interface{}) (string, error) {
	for _, parameter := range action.Parameters {
		if parameter.Required && argumentText(arguments, strings.TrimSpace(parameter.Name)) == "" {
			return "", fmt.Errorf("parâmetro obrigatório %q não informado", parameter.Name)
		}
	}
	method := strings.ToUpper(strings.TrimSpace(action.Method))
	if method == "" {
		method = "POST"
	}
	ctx := runner.actionContext(arguments)
	target := action.URL
	headerFields := make([]interface{}, 0, len(action.Headers))
	for _, header := range action.Headers {
		if strings.TrimSpace(header.Key) != "" {
			headerFields = append(headerFields, map[string]interface{}{"key": header.Key, "value": header.Value})
		}
	}
	bodyFields := make([]interface{}, 0, len(action.Parameters))
	query := url.Values{}
	for _, parameter := range action.Parameters {
		parameterName := strings.TrimSpace(parameter.Name)
		// Parameters already placed in the URL are not repeated.
		if strings.Contains(target, "{{"+parameterName+"}}") {
			continue
		}
		value := argumentText(arguments, parameterName)
		if method == "GET" || method == "DELETE" {
			if value != "" {
				query.Set(parameterName, value)
			}
			continue
		}
		bodyFields = append(bodyFields, map[string]interface{}{"key": parameterName, "value": "{{" + parameterName + "}}"})
	}
	if encoded := query.Encode(); encoded != "" {
		separator := "?"
		if strings.Contains(target, "?") {
			separator = "&"
		}
		target += separator + encoded
	}
	timeout := action.Timeout
	if timeout == 0 {
		timeout = defaultAIActionTimeout
	}
	data := map[string]interface{}{
		"method": method, "url": target, "headerMode": "fields", "headerFields": headerFields,
		"bodyMode": "none", "timeout": timeout,
	}
	if len(bodyFields) > 0 {
		data["bodyMode"], data["bodyFields"] = "json", bodyFields
	}
	response := runner.messenger.ExecuteHTTPRequest(data, ctx)
	if httpResponseHandle(response) == "failure" {
		return "", errors.New(httpFailureReason(response))
	}
	encoded, err := json.Marshal(map[string]interface{}{"status": response["status"], "data": response["data"]})
	if err != nil {
		return "", err
	}
	text := string(encoded)
	if len(text) > maxAIToolResultBytes {
		text = strings.ToValidUTF8(text[:maxAIToolResultBytes], "") + "… (resposta truncada)"
	}
	return text, nil
}

// finish runs the transfer or closing the model requested, after its reply.
func (runner *aiToolRunner) finish() error {
	if runner.pending == nil {
		return nil
	}
	action, companyID := runner.pending.action, runner.connection.CompanyID
	if message := strings.TrimSpace(action.Message); message != "" {
		if err := runner.messenger.SendText(runner.conversation, ReplaceVariables(message, runner.actionContext(nil))); err != nil {
			log.Printf("ai agent action message could not be sent: conversation=%d error=%v", runner.conversation.Id, err)
		}
	}
	var err error
	if action.Type == "transfer" {
		err = repo.TransferAIAgentConversation(runner.session.Id, runner.conversation.Id, companyID, action.AssignType, action.AssignID, runner.pending.summary)
	} else {
		err = repo.CloseAIAgentConversation(runner.session.Id, runner.conversation.Id, companyID, runner.pending.summary)
	}
	if err != nil {
		return err
	}
	sse.NotifyConversations(companyID)
	log.Printf("ai agent session finished by action: conversation=%d agent=%d type=%s", runner.conversation.Id, runner.session.Agent.Id, action.Type)
	return nil
}

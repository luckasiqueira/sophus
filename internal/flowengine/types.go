package flowengine

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	maxFlowDataBytes = 1 << 20
	maxFlowNodes     = 200
	maxFlowEdges     = 500
)

var (
	flowNodeIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
	flowVariablePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,119}$`)
)

var knownFlowNodeTypes = map[FlowNodeType]bool{
	NodeStart: true, NodeSendText: true, NodeSendImage: true, NodeSendVideo: true, NodeSendAudio: true,
	NodeSendFile: true, NodeSendMenu: true, NodeSendButtons: true, NodeCondition: true, NodeSwitch: true, NodeDelay: true,
	NodeChangeStatus: true, NodeAssign: true, NodeHTTPRequest: true, NodeWaitForResponse: true,
	NodeInput: true, NodeCheckResponse: true, NodeEnd: true,
	NodeSetVariable: true, NodeTag: true, NodeUpdateContact: true, NodeBusinessHours: true, NodeRandomSplit: true,
	NodeMessageType: true, NodeSmartAssign: true,
	NodeAddNote: true, NodeCallback: true,
	NodeSubflow:             true,
	NodeSendTemplate:        true,
	NodeAI:                  true,
	NodeScheduleMessage:     true,
	NodeAIAgent:             true,
	NodeSetContactField:     true,
	NodeConversationSummary: true,
	NodeNPSEvaluation:       true,
	NodeMediaTranscription:  true,
}

type FlowNodeType string

const (
	NodeStart               FlowNodeType = "start"
	NodeSendText            FlowNodeType = "sendText"
	NodeSendImage           FlowNodeType = "sendImage"
	NodeSendVideo           FlowNodeType = "sendVideo"
	NodeSendAudio           FlowNodeType = "sendAudio"
	NodeSendFile            FlowNodeType = "sendFile"
	NodeSendMenu            FlowNodeType = "sendMenu"
	NodeSendButtons         FlowNodeType = "sendButtons"
	NodeCondition           FlowNodeType = "condition"
	NodeSwitch              FlowNodeType = "switch"
	NodeDelay               FlowNodeType = "delay"
	NodeChangeStatus        FlowNodeType = "changeStatus"
	NodeAssign              FlowNodeType = "assign"
	NodeHTTPRequest         FlowNodeType = "httpRequest"
	NodeWaitForResponse     FlowNodeType = "waitForResponse"
	NodeInput               FlowNodeType = "input"
	NodeCheckResponse       FlowNodeType = "checkResponse"
	NodeSetVariable         FlowNodeType = "setVariable"
	NodeTag                 FlowNodeType = "tag"
	NodeUpdateContact       FlowNodeType = "updateContact"
	NodeBusinessHours       FlowNodeType = "businessHours"
	NodeRandomSplit         FlowNodeType = "randomSplit"
	NodeMessageType         FlowNodeType = "messageType"
	NodeSmartAssign         FlowNodeType = "smartAssign"
	NodeAddNote             FlowNodeType = "addNote"
	NodeCallback            FlowNodeType = "callback"
	NodeSubflow             FlowNodeType = "subflow"
	NodeSendTemplate        FlowNodeType = "sendTemplate"
	NodeAI                  FlowNodeType = "ai"
	NodeScheduleMessage     FlowNodeType = "scheduleMessage"
	NodeAIAgent             FlowNodeType = "aiAgent"
	NodeSetContactField     FlowNodeType = "setContactField"
	NodeConversationSummary FlowNodeType = "conversationSummary"
	NodeNPSEvaluation       FlowNodeType = "npsEvaluation"
	NodeMediaTranscription  FlowNodeType = "mediaTranscription"
	NodeEnd                 FlowNodeType = "end"
)

type FlowNode struct {
	ID       string                 `json:"id"`
	Type     FlowNodeType           `json:"type"`
	Position map[string]float64     `json:"position"`
	Data     map[string]interface{} `json:"data"`
}

type FlowEdge struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	Target       string `json:"target"`
	SourceHandle string `json:"sourceHandle,omitempty"`
	TargetHandle string `json:"targetHandle,omitempty"`
}

type FlowData struct {
	Nodes    []FlowNode   `json:"nodes"`
	Edges    []FlowEdge   `json:"edges"`
	Settings FlowSettings `json:"settings,omitempty"`
}

type FlowSettings struct {
	NoResponse NoResponseSettings `json:"noResponse,omitempty"`
}

type NoResponseSettings struct {
	Enabled         bool   `json:"enabled"`
	ReminderMinutes int    `json:"reminderMinutes"`
	ReminderMessage string `json:"reminderMessage"`
	TargetType      string `json:"targetType"`
	TargetID        int    `json:"targetId"`
}

type ExecutionContext map[string]interface{}

type ProcessResult struct {
	Context                   ExecutionContext
	WaitForResponse           bool
	ConditionResult           *bool
	SwitchCaseIndex           *int
	OutputHandle              string
	DirectNodeID              string
	ScheduleAppointmentHandle string
	WaitPersisted             bool
}

type BusinessHours struct {
	Enabled        bool   `json:"enabled"`
	Timezone       string `json:"timezone"`
	Days           []int  `json:"days"`
	StartTime      string `json:"startTime"`
	EndTime        string `json:"endTime"`
	LunchEnabled   bool   `json:"lunchEnabled"`
	LunchStart     string `json:"lunchStart"`
	LunchEnd       string `json:"lunchEnd"`
	OutsideAction  string `json:"outsideAction"`
	OutsideMessage string `json:"outsideMessage"`
}

func ParseFlowData(raw json.RawMessage) (FlowData, error) {
	var data FlowData
	if len(raw) == 0 {
		return FlowData{Nodes: []FlowNode{}, Edges: []FlowEdge{}}, nil
	}
	err := json.Unmarshal(raw, &data)
	return data, err
}

func ValidateFlowData(raw json.RawMessage) error {
	if len(raw) > maxFlowDataBytes {
		return fmt.Errorf("flowData excede o limite de %d bytes", maxFlowDataBytes)
	}
	data, err := ParseFlowData(raw)
	if err != nil {
		return err
	}
	if len(data.Nodes) > maxFlowNodes || len(data.Edges) > maxFlowEdges {
		return fmt.Errorf("flow excede o limite de %d nodes ou %d conexões", maxFlowNodes, maxFlowEdges)
	}
	if err := validateNoResponseSettings(data.Settings.NoResponse); err != nil {
		return fmt.Errorf("configuração sem resposta: %w", err)
	}
	nodes := make(map[string]FlowNode, len(data.Nodes))
	for _, node := range data.Nodes {
		if !knownFlowNodeTypes[node.Type] {
			return fmt.Errorf("tipo de node inválido: %s", node.Type)
		}
		if !flowNodeIDPattern.MatchString(node.ID) {
			return fmt.Errorf("node possui ID inválido")
		}
		if _, duplicated := nodes[node.ID]; duplicated {
			return fmt.Errorf("ID de node duplicado: %s", node.ID)
		}
		nodes[node.ID] = node
	}
	edgeIDs := make(map[string]bool, len(data.Edges))
	routes := make(map[string]bool, len(data.Edges))
	for _, edge := range data.Edges {
		if edge.ID == "" || len(edge.ID) > 120 || edgeIDs[edge.ID] {
			return fmt.Errorf("edge possui ID vazio, inválido ou duplicado")
		}
		edgeIDs[edge.ID] = true
		if _, ok := nodes[edge.Source]; !ok {
			return fmt.Errorf("edge %s possui origem inexistente", edge.ID)
		}
		if _, ok := nodes[edge.Target]; !ok {
			return fmt.Errorf("edge %s possui destino inexistente", edge.ID)
		}
		if nodes[edge.Source].Type == NodeEnd || nodes[edge.Source].Type == NodeAIAgent {
			return fmt.Errorf("edge %s não pode sair de um node Fim", edge.ID)
		}
		if nodes[edge.Target].Type == NodeStart {
			return fmt.Errorf("edge %s não pode entrar em um node Início", edge.ID)
		}
		if edge.Source == edge.Target {
			return fmt.Errorf("edge %s não pode conectar um node a ele mesmo", edge.ID)
		}
		if len(edge.SourceHandle) > 160 || len(edge.TargetHandle) > 160 {
			return fmt.Errorf("edge %s possui handle muito longo", edge.ID)
		}
		if !validSourceHandle(nodes[edge.Source], edge.SourceHandle) {
			return fmt.Errorf("edge %s usa uma saída inválida para o node %s", edge.ID, nodes[edge.Source].Type)
		}
		route := edge.Source + "\x00" + edge.SourceHandle
		if routes[route] {
			return fmt.Errorf("node %s possui mais de uma conexão na mesma saída", edge.Source)
		}
		routes[route] = true
	}
	type mediaFields struct {
		URL  string
		Path string
	}
	requiredMediaFields := map[FlowNodeType]mediaFields{
		NodeSendImage: {URL: "imageUrl", Path: "imagePath"},
		NodeSendVideo: {URL: "videoUrl", Path: "videoPath"},
		NodeSendAudio: {URL: "audioUrl", Path: "audioPath"},
		NodeSendFile:  {URL: "fileUrl", Path: "filePath"},
	}
	for _, node := range data.Nodes {
		fields, requiresMedia := requiredMediaFields[node.Type]
		if requiresMedia && strings.TrimSpace(stringVal(node.Data, fields.URL)) == "" &&
			(fields.Path == "" || strings.TrimSpace(stringVal(node.Data, fields.Path)) == "") {
			return fmt.Errorf("node %s requer uma mídia", node.Type)
		}
		if node.Type == NodeHTTPRequest {
			if err := validateHTTPRequestNode(node.Data); err != nil {
				return fmt.Errorf("node HTTP Request: %w", err)
			}
		}
		if node.Type == NodeCondition {
			if err := validateConditionNode(node.Data); err != nil {
				return fmt.Errorf("node Condição: %w", err)
			}
		}
		if err := validateNodeConfiguration(node); err != nil {
			return fmt.Errorf("node %s: %w", node.Type, err)
		}
	}
	return nil
}

func validateNoResponseSettings(settings NoResponseSettings) error {
	if !settings.Enabled {
		return nil
	}
	if settings.ReminderMinutes < 1 || settings.ReminderMinutes > 43200 {
		return fmt.Errorf("tempo do lembrete deve estar entre 1 e 43200 minutos")
	}
	message := strings.TrimSpace(settings.ReminderMessage)
	if message == "" || len(message) > 10000 {
		return fmt.Errorf("mensagem do lembrete deve possuir entre 1 e 10000 caracteres")
	}
	if settings.TargetType != "agent" && settings.TargetType != "department" {
		return fmt.Errorf("destino deve ser um agente ou setor")
	}
	if settings.TargetID <= 0 {
		return fmt.Errorf("destino da transferência é obrigatório")
	}
	return nil
}

func ValidateActiveFlowData(raw json.RawMessage) error {
	if err := ValidateFlowData(raw); err != nil {
		return err
	}
	data, _ := ParseFlowData(raw)
	starts := 0
	startID := ""
	for _, node := range data.Nodes {
		if node.Type == NodeStart {
			starts++
			startID = node.ID
		}
	}
	if starts != 1 {
		return fmt.Errorf("o fluxo ativo deve possuir exatamente um node Início")
	}
	reachable := map[string]bool{startID: true}
	for changed := true; changed; {
		changed = false
		for _, edge := range data.Edges {
			if reachable[edge.Source] && !reachable[edge.Target] {
				reachable[edge.Target] = true
				changed = true
			}
		}
	}
	for _, node := range data.Nodes {
		if !reachable[node.ID] {
			return fmt.Errorf("node %s não é alcançável a partir do Início", node.ID)
		}
	}
	return nil
}

func validateNodeConfiguration(node FlowNode) error {
	if node.Type == NodeAI || node.Type == NodeAIAgent || node.Type == NodeConversationSummary || node.Type == NodeMediaTranscription {
		if value, exists := node.Data["safeMode"]; exists {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("modo seguro deve ser verdadeiro ou falso")
			}
		}
	}
	switch node.Type {
	case NodeSendText:
		message := strings.TrimSpace(stringVal(node.Data, "message"))
		if message == "" || len(message) > 10000 {
			return fmt.Errorf("mensagem deve possuir entre 1 e 10000 caracteres")
		}
	case NodeSendMenu:
		if err := validateMenuNode(node.Data); err != nil {
			return err
		}
	case NodeSendButtons:
		if err := validateButtonsNode(node.Data); err != nil {
			return err
		}
	case NodeSwitch:
		cases := switchCases(node.Data)
		if len(cases) == 0 || len(cases) > 20 {
			return fmt.Errorf("deve possuir entre 1 e 20 opções")
		}
		for index, item := range cases {
			if strings.TrimSpace(stringVal(item, "condition")) == "" {
				return fmt.Errorf("opção %d exige uma condição", index+1)
			}
		}
		if len(stringVal(node.Data, "message")) > 10000 {
			return fmt.Errorf("mensagem excede o limite de 10000 caracteres")
		}
	case NodeStart:
		if raw := node.Data["businessHours"]; raw != nil {
			encoded, _ := json.Marshal(raw)
			var hours BusinessHours
			if err := json.Unmarshal(encoded, &hours); err != nil {
				return fmt.Errorf("horário de atendimento inválido")
			}
			if hours.Enabled {
				if err := validateBusinessHours(hours); err != nil {
					return err
				}
			}
		}
	case NodeBusinessHours:
		hours, err := decodeBusinessHours(node.Data)
		if err != nil {
			return err
		}
		if err := validateBusinessHours(hours); err != nil {
			return err
		}
	case NodeSetVariable:
		name := strings.TrimSpace(stringVal(node.Data, "variableName"))
		if !flowVariablePattern.MatchString(name) {
			return fmt.Errorf("nome de variável inválido")
		}
		if len(stringVal(node.Data, "value")) > 10000 {
			return fmt.Errorf("valor excede o limite de 10000 caracteres")
		}
	case NodeTag:
		operation := strings.TrimSpace(stringVal(node.Data, "operation"))
		if operation != "add" && operation != "remove" {
			return fmt.Errorf("operação de tag inválida")
		}
		tag := strings.TrimSpace(stringVal(node.Data, "tag"))
		if tag == "" || len(tag) > 80 {
			return fmt.Errorf("tag deve possuir entre 1 e 80 caracteres")
		}
	case NodeUpdateContact:
		field := strings.TrimSpace(stringVal(node.Data, "field"))
		if field != "name" && field != "email" {
			return fmt.Errorf("campo de contato inválido")
		}
		value := strings.TrimSpace(stringVal(node.Data, "value"))
		if value == "" || len(value) > 320 {
			return fmt.Errorf("valor do contato deve possuir entre 1 e 320 caracteres")
		}
	case NodeSetContactField:
		key := strings.TrimSpace(stringVal(node.Data, "fieldKey"))
		if !flowVariablePattern.MatchString(key) || len(stringVal(node.Data, "value")) > 10000 {
			return fmt.Errorf("campo personalizado inválido")
		}
		fieldType := stringVal(node.Data, "fieldType")
		if fieldType != "text" && fieldType != "number" && fieldType != "boolean" {
			return fmt.Errorf("tipo de campo personalizado inválido")
		}
	case NodeConversationSummary:
		if intVal(node.Data, "credentialId") <= 0 && intVal(node.Data, "aiAgentId") <= 0 {
			return fmt.Errorf("credencial de IA é obrigatória")
		}
		if maxMessages := intVal(node.Data, "maxMessages"); maxMessages < 2 || maxMessages > 100 {
			return fmt.Errorf("quantidade de mensagens deve estar entre 2 e 100")
		}
		if len(stringVal(node.Data, "instructions")) > 5000 {
			return fmt.Errorf("instruções de resumo excedem o limite")
		}
		if name := strings.TrimSpace(stringVal(node.Data, "saveResponseTo")); !flowVariablePattern.MatchString(name) {
			return fmt.Errorf("nome de variável inválido")
		}
	case NodeNPSEvaluation:
		message := strings.TrimSpace(stringVal(node.Data, "message"))
		if message == "" || len(message) > 10000 {
			return fmt.Errorf("mensagem de NPS inválida")
		}
		if name := strings.TrimSpace(stringVal(node.Data, "saveScoreTo")); !flowVariablePattern.MatchString(name) {
			return fmt.Errorf("nome de variável inválido")
		}
	case NodeRandomSplit:
		percentage := intVal(node.Data, "percentage")
		if percentage < 1 || percentage > 99 {
			return fmt.Errorf("percentual deve estar entre 1 e 99")
		}
	case NodeSmartAssign:
		strategy := strings.TrimSpace(stringVal(node.Data, "strategy"))
		if strategy != "leastLoaded" && strategy != "random" {
			return fmt.Errorf("estratégia de distribuição inválida")
		}
		if departmentID := intVal(node.Data, "departmentId"); departmentID < 0 {
			return fmt.Errorf("setor inválido")
		}
	case NodeAddNote:
		eventType := strings.TrimSpace(stringVal(node.Data, "eventType"))
		if eventType != "note" && eventType != "history" {
			return fmt.Errorf("tipo de registro inválido")
		}
		content := strings.TrimSpace(stringVal(node.Data, "content"))
		if content == "" || len(content) > 10000 {
			return fmt.Errorf("conteúdo deve possuir entre 1 e 10000 caracteres")
		}
	case NodeCallback:
		eventName := strings.TrimSpace(stringVal(node.Data, "eventName"))
		if eventName == "" || len(eventName) > 80 {
			return fmt.Errorf("nome do evento deve possuir entre 1 e 80 caracteres")
		}
		requestData := map[string]interface{}{
			"method": "POST", "url": stringVal(node.Data, "url"), "bodyMode": "none",
			"timeout": intVal(node.Data, "timeout"), "followRedirects": boolVal(node.Data, "followRedirects"),
		}
		if err := validateHTTPRequestNode(requestData); err != nil {
			return err
		}
	case NodeSubflow:
		if intVal(node.Data, "flowId") <= 0 {
			return fmt.Errorf("subfluxo é obrigatório")
		}
	case NodeSendTemplate:
		if intVal(node.Data, "templateId") <= 0 {
			return fmt.Errorf("modelo de mensagem é obrigatório")
		}
	case NodeAI:
		if intVal(node.Data, "credentialId") <= 0 {
			return fmt.Errorf("credencial de IA é obrigatória; selecione uma credencial no bloco Gerar com IA")
		}
		prompt := strings.TrimSpace(stringVal(node.Data, "prompt"))
		if prompt == "" || len(prompt) > 20000 || len(stringVal(node.Data, "systemPrompt")) > 10000 || len(stringVal(node.Data, "knowledgeBase")) > 500000 {
			return fmt.Errorf("prompt é obrigatório e deve respeitar os limites de tamanho")
		}
		if name := strings.TrimSpace(stringVal(node.Data, "saveResponseTo")); !flowVariablePattern.MatchString(name) {
			return fmt.Errorf("nome de variável inválido")
		}
	case NodeAIAgent:
		if intVal(node.Data, "aiAgentId") <= 0 {
			return fmt.Errorf("agente de IA é obrigatório")
		}
	case NodeMediaTranscription:
		if intVal(node.Data, "credentialId") <= 0 && intVal(node.Data, "aiAgentId") <= 0 {
			return fmt.Errorf("credencial de IA é obrigatória")
		}
		if len(stringVal(node.Data, "prompt")) > 2000 {
			return fmt.Errorf("prompt de transcrição excede o limite de 2000 caracteres")
		}
		if name := strings.TrimSpace(stringVal(node.Data, "saveResponseTo")); !flowVariablePattern.MatchString(name) {
			return fmt.Errorf("nome de variável inválido")
		}
	case NodeScheduleMessage:
		message := strings.TrimSpace(stringVal(node.Data, "message"))
		if message == "" || len(message) > 10000 {
			return fmt.Errorf("mensagem deve possuir entre 1 e 10000 caracteres")
		}
		if delayMinutes := intVal(node.Data, "delayMinutes"); delayMinutes < 1 || delayMinutes > 43200 {
			return fmt.Errorf("agendamento deve estar entre 1 minuto e 30 dias")
		}
	case NodeDelay:
		if stringVal(node.Data, "delayType") == "range" {
			minimum, maximum := intVal(node.Data, "minSeconds"), intVal(node.Data, "maxSeconds")
			if minimum < 1 || maximum < minimum || maximum > 60 {
				return fmt.Errorf("intervalo deve estar entre 1 e 60 segundos")
			}
		} else if seconds := intVal(node.Data, "seconds"); seconds < 0 || seconds > 60 {
			return fmt.Errorf("delay deve estar entre 0 e 60 segundos")
		}
	case NodeChangeStatus:
		status := stringVal(node.Data, "status")
		if status != "open" && status != "pending" && status != "closed" {
			return fmt.Errorf("status inválido")
		}
	case NodeAssign:
		assignType := stringVal(node.Data, "assignType")
		if assignType != "agent" && assignType != "department" {
			return fmt.Errorf("tipo de atribuição inválido")
		}
		if intVal(node.Data, "assignId") <= 0 {
			return fmt.Errorf("destino da atribuição é obrigatório")
		}
		if len(stringVal(node.Data, "message")) > 10000 {
			return fmt.Errorf("mensagem excede o limite de 10000 caracteres")
		}
	case NodeInput:
		if name := strings.TrimSpace(stringVal(node.Data, "variableName")); name != "" && !flowVariablePattern.MatchString(name) {
			return fmt.Errorf("nome de variável inválido")
		}
		validationType := strings.TrimSpace(stringVal(node.Data, "validationType"))
		if validationType == "" {
			validationType = "any"
		}
		if validationType != "any" && validationType != "text" && validationType != "email" && validationType != "number" && validationType != "regex" {
			return fmt.Errorf("tipo de validação inválido")
		}
		if validationType == "regex" {
			pattern := stringVal(node.Data, "validationPattern")
			if pattern == "" || len(pattern) > 500 {
				return fmt.Errorf("expressão de validação é obrigatória e deve possuir até 500 caracteres")
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("expressão de validação inválida")
			}
		}
	case NodeHTTPRequest, NodeCheckResponse:
		if name := strings.TrimSpace(stringVal(node.Data, "saveResponseTo")); name != "" && !flowVariablePattern.MatchString(name) {
			return fmt.Errorf("nome de variável inválido")
		}
	}
	if node.Type != NodeHTTPRequest && node.Type != NodeCallback {
		if timeout := intVal(node.Data, "timeout"); timeout < 0 || timeout > 86400 {
			return fmt.Errorf("timeout deve estar entre 0 e 86400 segundos")
		}
	}
	return nil
}

func safeModeEnabled(data map[string]interface{}) bool {
	value, exists := data["safeMode"]
	if !exists {
		return true
	}
	enabled, ok := value.(bool)
	return !ok || enabled
}

func validSourceHandle(node FlowNode, handle string) bool {
	switch node.Type {
	case NodeCondition, NodeCheckResponse, NodeBusinessHours:
		return handle == "" || handle == "true" || handle == "false"
	case NodeWaitForResponse:
		return handle == "" || handle == "replied" || handle == "timeout"
	case NodeInput:
		return handle == "" || handle == "replied" || handle == "valid" || handle == "invalid" || handle == "timeout"
	case NodeRandomSplit:
		return handle == "a" || handle == "b"
	case NodeMessageType:
		return handle == "text" || handle == "image" || handle == "video" || handle == "audio" || handle == "document" || handle == "other"
	case NodeSmartAssign:
		return handle == "assigned" || handle == "unavailable"
	case NodeCallback, NodeConversationSummary:
		return handle == "success" || handle == "failure"
	case NodeHTTPRequest:
		return handle == "" || handle == "success" || handle == "failure"
	case NodeNPSEvaluation:
		return handle == "promoter" || handle == "passive" || handle == "detractor" || handle == "invalid" || handle == "timeout"
	case NodeSubflow:
		return handle == "success" || handle == "failure"
	case NodeAI:
		return handle == "success" || handle == "failure"
	case NodeScheduleMessage:
		return handle == "scheduled" || handle == "failure"
	case NodeSendMenu:
		return handle == "" || handle == "fallback" || strings.HasPrefix(handle, "menu-")
	case NodeSendButtons:
		if handle == "fallback" {
			return true
		}
		for _, button := range flowButtons(node.Data) {
			if handle == "button-"+stringVal(button, "_handleId") && stringVal(button, "_handleId") != "" {
				return true
			}
		}
		return false
	case NodeSwitch:
		if handle == "fallback" {
			return true
		}
		var index int
		if _, err := fmt.Sscanf(handle, "case-%d", &index); err != nil {
			return false
		}
		return handle == fmt.Sprintf("case-%d", index) && index >= 0 && index < len(switchCases(node.Data))
	case NodeEnd, NodeAIAgent:
		return false
	default:
		return handle == ""
	}
}

func validateMenuNode(data map[string]interface{}) error {
	if strings.TrimSpace(stringVal(data, "description")) == "" {
		return fmt.Errorf("descrição do menu é obrigatória")
	}
	encoded, err := json.Marshal(data["sections"])
	if err != nil {
		return fmt.Errorf("seções do menu são inválidas")
	}
	var sections []menuSection
	if err := json.Unmarshal(encoded, &sections); err != nil || len(sections) == 0 || len(sections) > 10 {
		return fmt.Errorf("menu deve possuir entre 1 e 10 seções")
	}
	rowIDs := map[string]bool{}
	rows := 0
	for sectionIndex, section := range sections {
		if strings.TrimSpace(section.Title) == "" || len(section.Rows) == 0 {
			return fmt.Errorf("seção %d exige título e ao menos uma opção", sectionIndex+1)
		}
		for _, row := range section.Rows {
			rows++
			rowID := strings.TrimSpace(row.RowID)
			if strings.TrimSpace(row.Title) == "" || rowID == "" || rowIDs[rowID] {
				return fmt.Errorf("opções do menu exigem título e IDs únicos")
			}
			if strings.EqualFold(rowID, menuRowIDPrevious) || strings.EqualFold(rowID, menuRowIDMain) {
				return fmt.Errorf("ID de opção reservado para navegação do menu")
			}
			rowIDs[rowID] = true
		}
	}
	if rows > 50 {
		return fmt.Errorf("menu excede o limite de 50 opções")
	}
	return nil
}

func flowButtons(data map[string]interface{}) []map[string]interface{} {
	items, _ := data["buttons"].([]interface{})
	buttons := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		button, _ := item.(map[string]interface{})
		buttons = append(buttons, button)
	}
	return buttons
}

func validateButtonsNode(data map[string]interface{}) error {
	if description := strings.TrimSpace(stringVal(data, "description")); description == "" || len(description) > 10000 {
		return fmt.Errorf("mensagem dos botões deve possuir entre 1 e 10000 caracteres")
	}
	buttons := flowButtons(data)
	if len(buttons) < 1 || len(buttons) > 3 {
		return fmt.Errorf("o bloco exige entre 1 e 3 botões")
	}
	ids := map[string]bool{}
	handles := map[string]bool{}
	for i, button := range buttons {
		id := strings.TrimSpace(stringVal(button, "id"))
		label := strings.TrimSpace(stringVal(button, "text"))
		handle := stringVal(button, "_handleId")
		if id == "" || len(id) > 100 || label == "" || len(label) > 25 || !flowNodeIDPattern.MatchString(handle) {
			return fmt.Errorf("botão %d exige ID único (até 100 caracteres), texto (até 25) e saída válida", i+1)
		}
		if ids[id] || handles[handle] {
			return fmt.Errorf("IDs e saídas dos botões devem ser únicos")
		}
		ids[id], handles[handle] = true, true
	}
	return nil
}

func decodeBusinessHours(data map[string]interface{}) (BusinessHours, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return BusinessHours{}, fmt.Errorf("horário de atendimento inválido")
	}
	var hours BusinessHours
	if err := json.Unmarshal(encoded, &hours); err != nil {
		return BusinessHours{}, fmt.Errorf("horário de atendimento inválido")
	}
	return hours, nil
}

func validateBusinessHours(hours BusinessHours) error {
	if _, err := time.LoadLocation(strings.TrimSpace(hours.Timezone)); err != nil {
		return fmt.Errorf("fuso horário inválido")
	}
	start, startOK := parseTimeMinutes(hours.StartTime)
	end, endOK := parseTimeMinutes(hours.EndTime)
	if !startOK || !endOK || start == end {
		return fmt.Errorf("horário inicial/final inválido")
	}
	seenDays := map[int]bool{}
	for _, day := range hours.Days {
		if day < 0 || day > 6 || seenDays[day] {
			return fmt.Errorf("dia da semana inválido ou duplicado")
		}
		seenDays[day] = true
	}
	if hours.LunchEnabled {
		lunchStart, lunchStartOK := parseTimeMinutes(hours.LunchStart)
		lunchEnd, lunchEndOK := parseTimeMinutes(hours.LunchEnd)
		if !lunchStartOK || !lunchEndOK || lunchStart >= lunchEnd {
			return fmt.Errorf("horário de almoço inválido")
		}
	}
	return nil
}

func validateConditionNode(data map[string]interface{}) error {
	source := strings.TrimSpace(stringVal(data, "conditionSource"))
	if source == "" || source == "legacy" {
		if len(stringVal(data, "condition")) > 2000 {
			return fmt.Errorf("expressão excede o limite de 2000 caracteres")
		}
		return nil
	}
	if !structuredConditionSources[source] {
		return fmt.Errorf("origem inválida")
	}
	operator := strings.TrimSpace(stringVal(data, "conditionOperator"))
	if !structuredConditionOperators[operator] {
		return fmt.Errorf("operador inválido")
	}
	if source == "variable" {
		variable := strings.TrimSpace(stringVal(data, "conditionVariable"))
		if variable == "" || strings.HasPrefix(variable, "_") || len(variable) > 120 {
			return fmt.Errorf("variável inválida")
		}
	}
	if len(stringVal(data, "conditionValue")) > 2000 {
		return fmt.Errorf("valor excede o limite de 2000 caracteres")
	}
	return nil
}

func validateHTTPRequestNode(data map[string]interface{}) error {
	method := strings.ToUpper(strings.TrimSpace(stringVal(data, "method")))
	if method == "" {
		method = http.MethodPost
	}
	if !allowedHTTPMethod(method) {
		return fmt.Errorf("método HTTP não permitido")
	}
	requestURL := strings.TrimSpace(stringVal(data, "url"))
	if requestURL == "" {
		return fmt.Errorf("URL é obrigatória")
	}
	if len(requestURL) > 4096 {
		return fmt.Errorf("URL excede o limite de 4096 caracteres")
	}
	if timeout := intVal(data, "timeout"); timeout != 0 && (timeout < 100 || timeout > int(maxHTTPRequestTimeout/time.Millisecond)) {
		return fmt.Errorf("timeout deve estar entre 100 e 240000 ms")
	}
	if timeout := intVal(data, "connectTimeout"); timeout != 0 && (timeout < 100 || timeout > int(maxHTTPConnectTimeout/time.Millisecond)) {
		return fmt.Errorf("timeout de conexão deve estar entre 100 e 60000 ms")
	}
	if _, err := httpRequestHeaders(data, ExecutionContext{}); err != nil {
		return err
	}
	bodyMode := strings.TrimSpace(stringVal(data, "bodyMode"))
	if bodyMode == "" {
		if strings.TrimSpace(stringVal(data, "body")) != "" {
			bodyMode = "rawJSON"
		} else {
			bodyMode = "none"
		}
	}
	switch bodyMode {
	case "none", "json", "form", "multipart", "rawJSON", "raw":
	default:
		return fmt.Errorf("modo de corpo HTTP inválido: %s", bodyMode)
	}
	if len(stringVal(data, "body")) > maxHTTPRequestBytes {
		return fmt.Errorf("corpo excede o limite de %d bytes", maxHTTPRequestBytes)
	}
	if len(keyValueFields(data["bodyFields"], ExecutionContext{})) > 500 {
		return fmt.Errorf("o limite é de 500 campos no corpo")
	}
	mappings := httpResponseMappings(data)
	if len(mappings) > 100 {
		return fmt.Errorf("o limite é de 100 mapeamentos de resposta")
	}
	variables := map[string]bool{}
	if saveTo := strings.TrimSpace(stringVal(data, "saveResponseTo")); saveTo != "" {
		variables[saveTo] = true
	}
	for index, mapping := range mappings {
		path := strings.TrimSpace(mapping.Path)
		variableName := strings.TrimSpace(mapping.VariableName)
		if len(path) > 500 {
			return fmt.Errorf("caminho do mapeamento %d excede 500 caracteres", index+1)
		}
		if _, err := parseJSONPath(path); err != nil {
			return fmt.Errorf("caminho do mapeamento %d é inválido", index+1)
		}
		if !flowVariablePattern.MatchString(variableName) {
			return fmt.Errorf("variável do mapeamento %d é inválida", index+1)
		}
		if variables[variableName] {
			return fmt.Errorf("variável %s está duplicada no HTTP Request", variableName)
		}
		variables[variableName] = true
	}
	if bodyMode == "multipart" {
		for _, field := range httpMultipartFields(data["bodyFields"], ExecutionContext{}) {
			if strings.ContainsAny(field.Key, "\r\n") {
				return fmt.Errorf("nome de campo multipart inválido")
			}
			if field.ContentType != "" {
				if _, _, err := mime.ParseMediaType(field.ContentType); err != nil {
					return fmt.Errorf("Content-Type multipart inválido para %s", field.Key)
				}
			}
		}
	}
	return nil
}

func ParseContext(raw json.RawMessage) (ExecutionContext, error) {
	ctx := ExecutionContext{}
	if len(raw) == 0 {
		return ctx, nil
	}
	err := json.Unmarshal(raw, &ctx)
	return ctx, err
}

func (c ExecutionContext) ToJSON() json.RawMessage {
	b, _ := json.Marshal(c)
	return b
}

func stringVal(data map[string]interface{}, key string) string {
	if data == nil {
		return ""
	}
	v, ok := data[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func intVal(data map[string]interface{}, key string) int {
	if data == nil {
		return 0
	}
	v, ok := data[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	default:
		return 0
	}
}

func boolVal(data map[string]interface{}, key string) bool {
	if data == nil {
		return false
	}
	v, ok := data[key]
	if !ok || v == nil {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

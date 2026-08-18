package flowengine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	maxFlowDataBytes = 1 << 20
	maxFlowNodes     = 200
	maxFlowEdges     = 500
)

type FlowNodeType string

const (
	NodeStart           FlowNodeType = "start"
	NodeSendText        FlowNodeType = "sendText"
	NodeSendImage       FlowNodeType = "sendImage"
	NodeSendVideo       FlowNodeType = "sendVideo"
	NodeSendAudio       FlowNodeType = "sendAudio"
	NodeSendFile        FlowNodeType = "sendFile"
	NodeSendMenu        FlowNodeType = "sendMenu"
	NodeCondition       FlowNodeType = "condition"
	NodeSwitch          FlowNodeType = "switch"
	NodeDelay           FlowNodeType = "delay"
	NodeChangeStatus    FlowNodeType = "changeStatus"
	NodeAssign          FlowNodeType = "assign"
	NodeHTTPRequest     FlowNodeType = "httpRequest"
	NodeWaitForResponse FlowNodeType = "waitForResponse"
	NodeInput           FlowNodeType = "input"
	NodeCheckResponse   FlowNodeType = "checkResponse"
	NodeEnd             FlowNodeType = "end"
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
	Nodes []FlowNode `json:"nodes"`
	Edges []FlowEdge `json:"edges"`
}

type ExecutionContext map[string]interface{}

type ProcessResult struct {
	Context                   ExecutionContext
	WaitForResponse           bool
	ConditionResult           *bool
	SwitchCaseIndex           *int
	OutputHandle              string
	ScheduleAppointmentHandle string
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
		return fmt.Errorf("timeout deve estar entre 100 e 60000 ms")
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
	case "none", "json", "form", "rawJSON":
	default:
		return fmt.Errorf("modo de corpo HTTP inválido: %s", bodyMode)
	}
	if len(stringVal(data, "body")) > maxHTTPRequestBytes {
		return fmt.Errorf("corpo excede o limite de %d bytes", maxHTTPRequestBytes)
	}
	if len(keyValueFields(data["bodyFields"], ExecutionContext{})) > 500 {
		return fmt.Errorf("o limite é de 500 campos no corpo")
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

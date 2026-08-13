package flowengine

import (
	"encoding/json"
	"fmt"
	"strings"
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
	data, err := ParseFlowData(raw)
	if err != nil {
		return err
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

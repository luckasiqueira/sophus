package flowengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sophus/internal/media"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares/sse"
	"strconv"
	"strings"
	"time"
)

const (
	maxDepth         = 50
	maxExecutionTime = 5 * time.Minute
)

type Engine struct {
	messenger *Messenger
}

func NewEngine(connection repo.ConnectionEVO) *Engine {
	return &Engine{messenger: NewMessenger(connection)}
}

func (e *Engine) ExecuteFlow(flowID, conversationID, companyID int, initialContext ExecutionContext) error {
	startTime := time.Now()
	var execution repo.FlowExecution
	resumeExecutionID, _ := initialContext["_resumeExecutionId"].(float64)
	if int(resumeExecutionID) > 0 {
		var err error
		execution, err = repo.GetFlowExecutionById(int(resumeExecutionID))
		if err != nil {
			return fmt.Errorf("execução %d não encontrada: %w", int(resumeExecutionID), err)
		}
		if execution.FlowId != flowID || execution.ConversationId != conversationID || execution.CompanyId != companyID {
			return fmt.Errorf("execução %d não pertence ao flow, conversa e empresa informados", execution.Id)
		}
	}

	flow, err := repo.GetChatbotFlowById(flowID, companyID)
	if err != nil {
		message := fmt.Sprintf("flow %d não encontrado: %v", flowID, err)
		if int(resumeExecutionID) > 0 {
			return e.failExecution(int(resumeExecutionID), message)
		}
		return errors.New(message)
	}
	if !flow.IsActive {
		message := fmt.Sprintf("flow %d está inativo", flowID)
		if int(resumeExecutionID) > 0 {
			return e.failExecution(int(resumeExecutionID), message)
		}
		return errors.New(message)
	}

	flowData, err := ParseFlowData(flow.FlowData)
	if err != nil {
		if int(resumeExecutionID) > 0 {
			return e.failExecution(int(resumeExecutionID), err.Error())
		}
		return err
	}

	conversation, err := repo.GetConversationById(conversationID)
	if err != nil {
		if int(resumeExecutionID) > 0 {
			return e.failExecution(int(resumeExecutionID), err.Error())
		}
		return err
	}

	context := cloneContext(initialContext)
	context["_flowId"] = flowID

	var currentNodeID string

	if int(resumeExecutionID) > 0 {
		if execution.Status != "waiting" {
			log.Printf("execução %d já retomada (status: %s)", execution.Id, execution.Status)
			return nil
		}
		claimed, err := repo.ClaimFlowExecution(execution.Id)
		if err != nil {
			return err
		}
		if !claimed {
			log.Printf("execução %d já foi retomada por outro evento", execution.Id)
			return nil
		}
		savedCtx, _ := ParseContext(execution.Context)
		context = mergeContext(savedCtx, initialContext)
		if execution.CurrentNodeId != nil {
			savedNodeID := *execution.CurrentNodeId
			savedNode := findNode(flowData.Nodes, savedNodeID)
			if savedNode != nil && savedNode.Type == NodeWaitForResponse {
				next, _ := e.getNextNode(savedNodeID, string(NodeWaitForResponse), flowData.Edges, nil, nil, "", "")
				if next != "" {
					currentNodeID = next
				}
			} else {
				currentNodeID = savedNodeID
			}
		}
	} else {
		executionID, started, err := repo.StartFlowExecution(repo.FlowExecution{
			FlowId:         flowID,
			ConversationId: conversationID,
			CompanyId:      companyID,
			Status:         "running",
			Context:        context.ToJSON(),
		})
		if err != nil {
			return err
		}
		if !started {
			return nil
		}
		sse.NotifyConversations(companyID)
		execution, err = repo.GetFlowExecutionById(executionID)
		if err != nil {
			return e.failExecution(executionID, err.Error())
		}
	}

	metadata, err := repo.GetFlowConditionMetadata(conversationID, companyID)
	if err != nil {
		return e.failExecution(execution.Id, fmt.Sprintf("não foi possível carregar dados para condições: %v", err))
	}
	applyConditionMetadata(context, metadata)

	if currentNodeID == "" {
		startNode := findNodeByType(flowData.Nodes, NodeStart)
		if startNode == nil {
			return e.failExecution(execution.Id, "flow não possui node start")
		}
		currentNodeID = startNode.ID
		if int(resumeExecutionID) == 0 {
			bhResult, err := e.checkBusinessHours(startNode, conversation)
			if err != nil {
				return e.failExecution(execution.Id, err.Error())
			}
			if bhResult == "blocked" || bhResult == "message_sent" {
				return e.completeExecution(execution.Id, currentNodeID, context)
			}
		}
	}

	depth := 0
	for currentNodeID != "" && depth < maxDepth {
		if time.Since(startTime) > maxExecutionTime {
			return e.failExecution(execution.Id, "timeout: execução excedeu 5 minutos")
		}

		currentNode := findNode(flowData.Nodes, currentNodeID)
		if currentNode == nil {
			return e.failExecution(execution.Id, fmt.Sprintf("node %s não encontrado", currentNodeID))
		}

		log.Printf("processando node %s (%s)", currentNode.ID, currentNode.Type)
		result, err := e.processNode(execution.Id, *currentNode, context, conversation, companyID, flowData)
		if err != nil {
			if errors.Is(err, repo.ErrFlowExecutionInactive) {
				return nil
			}
			return e.failExecution(execution.Id, err.Error())
		}

		if len(result.Context) > 0 {
			context = mergeContext(context, result.Context)
		}

		if result.WaitForResponse {
			timeoutSeconds := intVal(currentNode.Data, "timeout")
			if err := e.saveExecutionState(execution.Id, currentNodeID, context, "waiting"); err != nil {
				return e.failExecution(execution.Id, fmt.Sprintf("não foi possível colocar a execução em espera: %v", err))
			}
			if timeoutSeconds > 0 {
				ScheduleTimeout(execution.Id, flowID, conversationID, companyID, timeoutSeconds)
			}
			return nil
		}

		if currentNode.Type == NodeEnd {
			return e.completeExecution(execution.Id, currentNodeID, context)
		}

		nextNodeID, _ := e.getNextNode(currentNodeID, string(currentNode.Type), flowData.Edges,
			result.ConditionResult, result.SwitchCaseIndex, result.OutputHandle, result.ScheduleAppointmentHandle)
		if nextNodeID == "" {
			return e.completeExecution(execution.Id, currentNodeID, context)
		}
		currentNodeID = nextNodeID
		depth++
	}

	if depth >= maxDepth {
		return e.failExecution(execution.Id, fmt.Sprintf("flow excedeu profundidade máxima de %d nodes", maxDepth))
	}
	return nil
}

func (e *Engine) processNode(executionID int, node FlowNode, ctx ExecutionContext, conversation repo.Conversation, companyID int, flowData FlowData) (ProcessResult, error) {
	if err := e.saveExecutionState(executionID, node.ID, ctx, "running"); err != nil {
		return ProcessResult{}, err
	}

	switch node.Type {
	case NodeStart:
		return ProcessResult{}, nil
	case NodeSendText:
		return ProcessResult{}, e.executeSendText(node, ctx, conversation)
	case NodeSendImage:
		return ProcessResult{}, e.executeSendImage(node, ctx, conversation)
	case NodeSendVideo:
		return ProcessResult{}, e.executeSendVideo(node, ctx, conversation)
	case NodeSendAudio:
		return ProcessResult{}, e.executeSendAudio(node, ctx, conversation)
	case NodeSendFile:
		return ProcessResult{}, e.executeSendFile(node, ctx, conversation)
	case NodeSendMenu:
		waitingID, _ := ctx["_waitingForMenuNodeId"].(string)
		if waitingID != node.ID {
			if err := e.messenger.SendMenu(conversation, node.Data, ctx); err != nil {
				return ProcessResult{}, err
			}
			return ProcessResult{
				WaitForResponse: true,
				Context: ExecutionContext{
					"_waitingForMenuNodeId": node.ID,
					"response":              nil,
					"userReplied":           nil,
				},
			}, nil
		}
		return ProcessResult{
			Context:      ExecutionContext{"_waitingForMenuNodeId": nil},
			OutputHandle: menuResponseHandle(node.Data, fmt.Sprint(ctx["response"]), ctx),
		}, nil
	case NodeCondition:
		metadata, err := repo.GetFlowConditionMetadata(conversation.Id, companyID)
		if err != nil {
			return ProcessResult{}, fmt.Errorf("não foi possível atualizar dados da condição: %w", err)
		}
		applyConditionMetadata(ctx, metadata)
		ctx["currentTime"] = conditionCurrentTime(flowData)
		result := e.EvaluateNodeCondition(node.Data, ctx)
		return ProcessResult{ConditionResult: &result}, nil
	case NodeSwitch:
		if ctx["response"] == nil || fmt.Sprint(ctx["response"]) == "" {
			return ProcessResult{WaitForResponse: true}, nil
		}
		cases := switchCases(node.Data)
		idx := e.EvaluateSwitch(cases, ctx)
		return ProcessResult{SwitchCaseIndex: &idx}, nil
	case NodeDelay:
		return ProcessResult{}, e.executeDelay(node.Data)
	case NodeChangeStatus:
		return ProcessResult{}, e.executeChangeStatus(node.Data, conversation.Id, executionID, companyID)
	case NodeAssign:
		return ProcessResult{}, e.executeAssign(node.Data, conversation.Id, companyID)
	case NodeHTTPRequest:
		resp := e.messenger.ExecuteHTTPRequest(node.Data, ctx)
		if saveTo := stringVal(node.Data, "saveResponseTo"); saveTo != "" {
			return ProcessResult{Context: ExecutionContext{saveTo: resp}}, nil
		}
		return ProcessResult{}, nil
	case NodeWaitForResponse:
		return ProcessResult{WaitForResponse: true}, nil
	case NodeInput:
		waitingID, _ := ctx["_waitingForInputNodeId"].(string)
		if waitingID != node.ID {
			if msg := stringVal(node.Data, "message"); msg != "" {
				_ = e.executeSendText(node, ctx, conversation)
			}
			return ProcessResult{
				WaitForResponse: true,
				Context:         ExecutionContext{"_waitingForInputNodeId": node.ID},
			}, nil
		}
		varName := stringVal(node.Data, "variableName")
		if varName == "" {
			varName = "userInput"
		}
		return ProcessResult{Context: ExecutionContext{
			varName:                  ctx["response"],
			"_waitingForInputNodeId": nil,
		}}, nil
	case NodeCheckResponse:
		hasResponse := ctx["userReplied"] == true
		if saveTo := stringVal(node.Data, "saveResponseTo"); saveTo != "" {
			ctx[saveTo] = hasResponse
		}
		return ProcessResult{ConditionResult: &hasResponse}, nil
	case NodeEnd:
		return ProcessResult{}, nil
	default:
		log.Printf("tipo de node desconhecido: %s", node.Type)
		return ProcessResult{}, nil
	}
}

func applyConditionMetadata(ctx ExecutionContext, metadata repo.FlowConditionMetadata) {
	contactName := metadata.ContactName
	if contactName == "" {
		contactName, _ = ctx["senderName"].(string)
	}
	ctx["contactName"] = contactName
	ctx["contactNumber"] = metadata.ContactNumber
	if metadata.ContactEmail != "" || ctx["contactEmail"] == nil {
		ctx["contactEmail"] = metadata.ContactEmail
	}
	ctx["conversationTags"] = metadata.ConversationTags
	ctx["department"] = metadata.Department
	ctx["connectionType"] = metadata.ConnectionType
}

func conditionCurrentTime(flowData FlowData) string {
	timezone := "America/Sao_Paulo"
	if startNode := findNodeByType(flowData.Nodes, NodeStart); startNode != nil {
		if raw := startNode.Data["businessHours"]; raw != nil {
			encoded, _ := json.Marshal(raw)
			var hours BusinessHours
			if json.Unmarshal(encoded, &hours) == nil && strings.TrimSpace(hours.Timezone) != "" {
				timezone = hours.Timezone
			}
		}
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		location = time.Local
	}
	return time.Now().In(location).Format("15:04")
}

func (e *Engine) getNextNode(currentNodeID, nodeType string, edges []FlowEdge, conditionResult *bool, switchCaseIndex *int, outputHandle, scheduleHandle string) (string, error) {
	outgoing := filterEdges(edges, currentNodeID)
	if len(outgoing) == 0 {
		return "", nil
	}
	if nodeType == string(NodeSwitch) && switchCaseIndex != nil {
		if *switchCaseIndex == -1 {
			for _, edge := range outgoing {
				if edge.SourceHandle == "fallback" {
					return edge.Target, nil
				}
			}
			return "", nil
		}
		handleID := fmt.Sprintf("case-%d", *switchCaseIndex)
		for _, edge := range outgoing {
			if edge.SourceHandle == handleID {
				return edge.Target, nil
			}
		}
		return "", nil
	}
	if conditionResult != nil {
		for _, edge := range outgoing {
			if *conditionResult && (edge.SourceHandle == "true" || edge.SourceHandle == "") {
				return edge.Target, nil
			}
			if !*conditionResult && edge.SourceHandle == "false" {
				return edge.Target, nil
			}
		}
		return "", nil
	}
	if outputHandle != "" {
		for _, edge := range outgoing {
			if edge.SourceHandle == outputHandle || (nodeType == string(NodeSendMenu) && outputHandle == "fallback" && edge.SourceHandle == "") {
				return edge.Target, nil
			}
		}
		return "", nil
	}
	if scheduleHandle != "" {
		for _, edge := range outgoing {
			if edge.SourceHandle == scheduleHandle {
				return edge.Target, nil
			}
		}
		if len(outgoing) > 0 {
			return outgoing[0].Target, nil
		}
	}
	return outgoing[0].Target, nil
}

func menuResponseHandle(data map[string]interface{}, response string, ctx ExecutionContext) string {
	sections, _ := data["sections"].([]interface{})
	response = strings.TrimSpace(response)
	option := 1
	for _, rawSection := range sections {
		section, _ := rawSection.(map[string]interface{})
		rows, _ := section["rows"].([]interface{})
		for _, rawRow := range rows {
			row, _ := rawRow.(map[string]interface{})
			rowID := ReplaceVariables(stringVal(row, "rowId"), ctx)
			if response != strconv.Itoa(option) && response != strings.TrimSpace(rowID) {
				option++
				continue
			}
			handleID := stringVal(row, "_handleId")
			if handleID == "" {
				handleID = stringVal(row, "rowId")
			}
			return "menu-" + handleID
		}
	}
	return "fallback"
}

func (e *Engine) executeSendText(node FlowNode, ctx ExecutionContext, conversation repo.Conversation) error {
	message := ReplaceVariables(stringVal(node.Data, "message"), ctx)
	if message == "" {
		return nil
	}
	return e.messenger.SendText(conversation, message)
}

func (e *Engine) executeSendImage(node FlowNode, ctx ExecutionContext, conversation repo.Conversation) error {
	source, err := flowMediaSource(node.Data, "imagePath", "imageUrl", e.messenger.Connection.CompanyID, ctx)
	if err != nil {
		return err
	}
	if source == "" {
		return nil
	}
	caption := ReplaceVariables(stringVal(node.Data, "caption"), ctx)
	return e.messenger.SendMedia(conversation, "image", source, caption)
}

func (e *Engine) executeSendVideo(node FlowNode, ctx ExecutionContext, conversation repo.Conversation) error {
	source, err := flowMediaSource(node.Data, "videoPath", "videoUrl", e.messenger.Connection.CompanyID, ctx)
	if err != nil {
		return err
	}
	if source == "" {
		return nil
	}
	caption := ReplaceVariables(stringVal(node.Data, "caption"), ctx)
	return e.messenger.SendMedia(conversation, "video", source, caption)
}

func (e *Engine) executeSendAudio(node FlowNode, ctx ExecutionContext, conversation repo.Conversation) error {
	source, err := flowMediaSource(node.Data, "audioPath", "audioUrl", e.messenger.Connection.CompanyID, ctx)
	if err != nil {
		return err
	}
	if source == "" {
		return nil
	}
	return e.messenger.SendAudio(conversation, source)
}

func (e *Engine) executeSendFile(node FlowNode, ctx ExecutionContext, conversation repo.Conversation) error {
	source, err := flowMediaSource(node.Data, "filePath", "fileUrl", e.messenger.Connection.CompanyID, ctx)
	if err != nil {
		return err
	}
	if source == "" {
		return nil
	}
	caption := ReplaceVariables(stringVal(node.Data, "caption"), ctx)
	return e.messenger.SendDocument(conversation, source, caption, stringVal(node.Data, "mediaName"))
}

func flowMediaSource(data map[string]interface{}, pathKey, urlKey string, companyID int, ctx ExecutionContext) (string, error) {
	if path := strings.TrimSpace(stringVal(data, pathKey)); path != "" {
		return media.SignedURL(companyID, path)
	}
	return ReplaceVariables(strings.TrimSpace(stringVal(data, urlKey)), ctx), nil
}

func (e *Engine) executeDelay(data map[string]interface{}) error {
	seconds := 0
	if stringVal(data, "delayType") == "range" {
		minS := intVal(data, "minSeconds")
		maxS := intVal(data, "maxSeconds")
		if minS > 0 && maxS > minS {
			if maxS > 60 {
				maxS = 60
			}
			seconds = minS + int(time.Now().UnixNano()%int64(maxS-minS+1))
		}
	} else {
		seconds = intVal(data, "seconds")
	}
	if seconds <= 0 {
		return nil
	}
	if seconds > 60 {
		seconds = 60
	}
	time.Sleep(time.Duration(seconds) * time.Second)
	return nil
}

func (e *Engine) executeChangeStatus(data map[string]interface{}, conversationID, executionID, companyID int) error {
	status := stringVal(data, "status")
	if status == "" {
		return nil
	}
	if err := repo.UpdateConversationStatus(conversationID, executionID, status); err != nil {
		return err
	}
	sse.NotifyConversations(companyID)
	return nil
}

func (e *Engine) executeAssign(data map[string]interface{}, conversationID, companyID int) error {
	assignType := stringVal(data, "assignType")
	if assignType == "" {
		assignType = "agent"
	}
	assignID := intVal(data, "assignId")
	if assignID <= 0 {
		return nil
	}
	if assignType == "department" {
		if err := repo.AssignConversationDepartment(conversationID, assignID, companyID); err != nil {
			return err
		}
		sse.NotifyConversations(companyID)
		return nil
	}
	if assignType != "agent" {
		return fmt.Errorf("tipo de atribuição inválido: %s", assignType)
	}
	if err := repo.AssignConversationAgent(conversationID, assignID, companyID); err != nil {
		return err
	}
	sse.NotifyConversations(companyID)
	return nil
}

func (e *Engine) checkBusinessHours(startNode *FlowNode, conversation repo.Conversation) (string, error) {
	raw, ok := startNode.Data["businessHours"]
	if !ok || raw == nil {
		return "ok", nil
	}
	b, _ := json.Marshal(raw)
	var bh BusinessHours
	if json.Unmarshal(b, &bh) != nil || !bh.Enabled {
		return "ok", nil
	}
	locName := bh.Timezone
	if locName == "" {
		locName = "America/Sao_Paulo"
	}
	loc, err := time.LoadLocation(locName)
	if err != nil {
		return "ok", nil
	}
	now := time.Now().In(loc)
	weekday := int(now.Weekday())
	allowedDays := bh.Days
	if len(allowedDays) == 0 {
		allowedDays = []int{1, 2, 3, 4, 5}
	}
	allowed := false
	for _, d := range allowedDays {
		if d == weekday {
			allowed = true
			break
		}
	}
	currentMinutes := now.Hour()*60 + now.Minute()
	startMinutes := timeToMinutes(bh.StartTime)
	endMinutes := timeToMinutes(bh.EndTime)
	outside := !allowed || currentMinutes < startMinutes || currentMinutes >= endMinutes
	if !outside && bh.LunchEnabled {
		lunchStart := timeToMinutes(bh.LunchStart)
		lunchEnd := timeToMinutes(bh.LunchEnd)
		if currentMinutes >= lunchStart && currentMinutes < lunchEnd {
			outside = true
		}
	}
	if !outside {
		return "ok", nil
	}
	if bh.OutsideAction == "message" && bh.OutsideMessage != "" {
		_ = e.messenger.SendText(conversation, bh.OutsideMessage)
		return "message_sent", nil
	}
	return "blocked", nil
}

func timeToMinutes(t string) int {
	if t == "" {
		return 0
	}
	parts := strings.Split(t, ":")
	if len(parts) < 2 {
		return 0
	}
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	return h*60 + m
}

func (e *Engine) saveExecutionState(executionID int, currentNodeID string, ctx ExecutionContext, status string) error {
	execution, err := repo.GetFlowExecutionById(executionID)
	if err != nil {
		return err
	}
	execution.Status = status
	execution.CurrentNodeId = &currentNodeID
	execution.Context = ctx.ToJSON()
	return repo.UpdateFlowExecution(execution)
}

func (e *Engine) completeExecution(executionID int, currentNodeID string, ctx ExecutionContext) error {
	execution, err := repo.GetFlowExecutionById(executionID)
	if err != nil {
		return err
	}
	now := time.Now()
	execution.Status = "completed"
	execution.CurrentNodeId = &currentNodeID
	execution.Context = ctx.ToJSON()
	execution.CompletedAt = &now
	if err := repo.FinalizeFlowExecution(execution); err != nil {
		if errors.Is(err, repo.ErrFlowExecutionInactive) {
			return nil
		}
		return err
	}
	sse.NotifyConversations(execution.CompanyId)
	return nil
}

func (e *Engine) failExecution(executionID int, message string) error {
	execution, err := repo.GetFlowExecutionById(executionID)
	if err != nil {
		return err
	}
	now := time.Now()
	execution.Status = "failed"
	execution.ErrorMessage = &message
	execution.CompletedAt = &now
	if err := repo.FinalizeFlowExecution(execution); err != nil {
		if errors.Is(err, repo.ErrFlowExecutionInactive) {
			return nil
		}
		return fmt.Errorf("%s: não foi possível persistir a falha: %w", message, err)
	}
	sse.NotifyConversations(execution.CompanyId)
	return errors.New(message)
}

func findNode(nodes []FlowNode, id string) *FlowNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
	}
	return nil
}

func findNodeByType(nodes []FlowNode, nodeType FlowNodeType) *FlowNode {
	for i := range nodes {
		if nodes[i].Type == nodeType {
			return &nodes[i]
		}
	}
	return nil
}

func filterEdges(edges []FlowEdge, source string) []FlowEdge {
	out := []FlowEdge{}
	for _, e := range edges {
		if e.Source == source {
			out = append(out, e)
		}
	}
	return out
}

func switchCases(data map[string]interface{}) []map[string]interface{} {
	raw, ok := data["cases"]
	if !ok {
		return nil
	}
	b, _ := json.Marshal(raw)
	var cases []map[string]interface{}
	_ = json.Unmarshal(b, &cases)
	return cases
}

func cloneContext(src ExecutionContext) ExecutionContext {
	dst := ExecutionContext{}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func mergeContext(base, overlay ExecutionContext) ExecutionContext {
	out := cloneContext(base)
	for k, v := range overlay {
		if v == nil {
			delete(out, k)
		} else {
			out[k] = v
		}
	}
	return out
}

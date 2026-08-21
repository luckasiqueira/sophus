package repo

import (
	"encoding/json"
	"fmt"
)

type flowDependencyData struct {
	Nodes []struct {
		Type string `json:"type"`
		Data struct {
			FlowID     int `json:"flowId"`
			TemplateID int `json:"templateId"`
		} `json:"data"`
	} `json:"nodes"`
}

func ValidateFlowDependencies(raw json.RawMessage, companyID, rootFlowID int) error {
	ancestors := map[int]bool{}
	if rootFlowID > 0 {
		ancestors[rootFlowID] = true
	}
	return validateFlowDependencies(raw, companyID, ancestors)
}

func validateFlowDependencies(raw json.RawMessage, companyID int, ancestors map[int]bool) error {
	var data flowDependencyData
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}
	for _, node := range data.Nodes {
		switch node.Type {
		case "sendTemplate":
			if _, err := GetFlowMessageTemplate(node.Data.TemplateID, companyID); err != nil {
				return fmt.Errorf("modelo de mensagem %d não existe nesta empresa", node.Data.TemplateID)
			}
		case "subflow":
			if ancestors[node.Data.FlowID] {
				return fmt.Errorf("referência cíclica ao subfluxo %d", node.Data.FlowID)
			}
			flow, err := GetChatbotFlowById(node.Data.FlowID, companyID)
			if err != nil {
				return fmt.Errorf("subfluxo %d não existe nesta empresa", node.Data.FlowID)
			}
			next := make(map[int]bool, len(ancestors)+1)
			for id := range ancestors {
				next[id] = true
			}
			next[node.Data.FlowID] = true
			if err := validateFlowDependencies(flow.FlowData, companyID, next); err != nil {
				return err
			}
		}
	}
	return nil
}

func ActiveFlowReferencesTemplate(templateID, companyID int) (bool, error) {
	var referenced bool
	err := db.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM chatbot_flows flow,
		LATERAL jsonb_array_elements(COALESCE(flow."flowData"->'nodes', '[]'::jsonb)) node
		WHERE flow."companyId" = $2 AND flow."isActive" = true
		AND node->>'type' = 'sendTemplate' AND node->'data'->>'templateId' = $1::text
	)`, templateID, companyID).Scan(&referenced)
	return referenced, err
}

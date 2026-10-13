package repo

import (
	"database/sql"
	"encoding/json"
	"strings"
)

type AgentNotificationPreferences struct {
	Toast   bool `json:"toast"`
	Sound   bool `json:"sound"`
	Browser bool `json:"browser"`
	Preview bool `json:"preview"`
	Groups  bool `json:"groups"`
}

type AgentPreferences struct {
	ScrollAfterSend          bool                         `json:"scrollAfterSend"`
	ScrollAfterSendMode      string                       `json:"scrollAfterSendMode"`
	MessageSignatureEnabled  bool                         `json:"messageSignatureEnabled"`
	MessageSignatureTemplate string                       `json:"messageSignatureTemplate"`
	Notifications            AgentNotificationPreferences `json:"notifications"`
	Onboarding               AgentOnboarding              `json:"onboarding"`
}

type AgentOnboarding struct {
	Completed bool `json:"completed"`
	Step      int  `json:"step"`
}

func DefaultAgentPreferences() AgentPreferences {
	return AgentPreferences{
		ScrollAfterSend: true, ScrollAfterSendMode: "always",
		MessageSignatureEnabled: true, MessageSignatureTemplate: "{agentName}",
		Notifications: AgentNotificationPreferences{Toast: true, Sound: true, Browser: true, Preview: true, Groups: true},
	}
}

func GetAgentPreferences(agentID, companyID int) (AgentPreferences, error) {
	var raw []byte
	err := db.QueryRow(`SELECT preferences FROM agents WHERE id=$1 AND "companyId"=$2`, agentID, companyID).Scan(&raw)
	preferences := DefaultAgentPreferences()
	if err != nil {
		return preferences, err
	}
	err = json.Unmarshal(raw, &preferences)
	return preferences, err
}

func UpdateAgentPreferences(agentID, companyID int, preferences AgentPreferences) error {
	raw, err := json.Marshal(preferences)
	if err != nil {
		return err
	}
	// Onboarding is updated by its own endpoint, so saving personalization must
	// not reset a completed guide or overwrite progress from another tab.
	result, err := db.Exec(`UPDATE agents SET preferences=$1::jsonb || jsonb_build_object('onboarding',
		COALESCE(preferences->'onboarding','{"completed":false,"step":0}'::jsonb)) WHERE id=$2 AND "companyId"=$3`, string(raw), agentID, companyID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func SaveAgentOnboarding(agentID, companyID int, state AgentOnboarding) error {
	result, err := db.Exec(`UPDATE agents SET preferences=preferences || jsonb_build_object('onboarding',jsonb_build_object(
		'completed',COALESCE((preferences->'onboarding'->>'completed')::boolean,false) OR $1,
		'step',GREATEST(COALESCE((preferences->'onboarding'->>'step')::integer,0),$2::integer)))
		WHERE id=$3 AND "companyId"=$4`, state.Completed, state.Step, agentID, companyID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (preferences AgentPreferences) SignMessage(text, agentName string) string {
	if !preferences.MessageSignatureEnabled {
		return text
	}
	signature := strings.TrimSpace(strings.ReplaceAll(preferences.MessageSignatureTemplate, "{agentName}", agentName))
	if signature == "" {
		return text
	}
	return signature + "\n\n" + strings.TrimSpace(text)
}

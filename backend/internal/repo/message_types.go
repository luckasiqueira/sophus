package repo

import (
	"zubly/backend/pkg/http/requests"
)

type Message interface {
	Save() error
}

type MessageBaseEVO struct {
	Id           string   `json:"id"` //uuid
	Delay        int      `json:"delay"`
	MentionAll   bool     `json:"mentionAll"`
	MentionedJid []string `json:"mentionedJid,omitempty"`
	Number       string   `json:"number"`
	QuotedEVO    `json:"quoted"`
}

type QuotedEVO struct {
	MessageID          string `json:"messageId"`
	MessageParticipant string `json:"messageParticipant"`
}

type TextMessageEVO struct {
	MessageBaseEVO
	Text string `json:"text"`
}

// func (m *TextMessage) Send(connectionKey string) (int, error) {
func (m *TextMessageEVO) Send(connectionKey string) (int, error) {
	r := requests.Request{
		URL:     apiBaseURL + `/send/text`,
		Payload: m,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"apikey":       connectionKey,
		},
		Response: requests.Response{},
	}
	err := r.Do()
	return r.StatusCode, err
}

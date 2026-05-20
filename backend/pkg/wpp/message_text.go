package wpp

import (
	"zubly/backend/pkg/http/requests"
)

type TextMessage struct {
	MessageBase
	Text string `json:"text"`
}

// func (m *TextMessage) Send(connectionKey string) (int, error) {
func (m *TextMessage) Send(connectionKey string) (int, error) {
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

func (m *TextMessage) Save() error {
	return nil
}

package wpp

import "zubly/backend/pkg/http/requests"

type TextMessage struct {
	MessageBase
	Text string `json:"text"`
}

func (m *TextMessage) Send(apiKey string) error {
	r := requests.Request{
		URL:     apiBaseURL + `/send/text`,
		Payload: m,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"apikey":       apiKey,
		},
		Response: requests.Response{},
	}
	return r.Do()
}

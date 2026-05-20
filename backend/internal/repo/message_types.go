package repo

type Message interface {
	Save(connection ConnectionEVO) error
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

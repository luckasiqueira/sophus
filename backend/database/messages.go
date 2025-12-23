package database

import (
	"fmt"
)

type Message struct {
	ChatId    int
	CompanyId int
	Content   string
}

func (m *Message) Received() error {
	query := `INSERT INTO public.messages (id, content) VALUES (DEFAULT, $1);`
	return insert(query, m.Content)
}

func MessageReceived() {
	m := Message{
		Content: "Hello World!",
	}
	err := m.Received()
	if err != nil {
		fmt.Println(err)
	}
}

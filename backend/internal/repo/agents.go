package repo

import (
	"fmt"
	"time"
)

type Agent struct {
	Id        int       `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Password  string    `json:"password"`
	Role      string    `json:"role"`
	IsActive  bool      `json:"isActive"`
	CompanyId int       `json:"companyId"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func GetAgentByEmail(email string) (Agent, error) {
	query := fmt.Sprintf(`SELECT * FROM agents WHERE email = $1`)
	return getAgent(query, email)
}

func GetAgentById(id int) (Agent, error) {
	query := fmt.Sprintf(`SELECT * FROM agents WHERE id = $1`, id)
	return getAgent(query, id)
}

func GetAgentByMessage(message string) (Agent, error) {
	query := fmt.Sprintf(`SELECT a.*
	FROM messages m
	INNER JOIN conversations c
		ON c.id = m."conversationId"
	INNER JOIN agents a
		ON a.id = c."agentId"
	WHERE m."messageId" = $1`)
	return getAgent(query, message)
}

func getAgent(query string, args ...interface{}) (Agent, error) {
	a := Agent{}
	stmt, err := db.Prepare(query)
	if err != nil {
		return Agent{}, err
	}
	defer stmt.Close()
	err = stmt.QueryRow(args...).Scan(&a.Id, &a.Name, &a.Email, &a.Password, &a.Role, &a.IsActive, &a.CompanyId, &a.CreatedAt, &a.UpdatedAt)
	return a, nil
}

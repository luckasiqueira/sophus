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
	a := Agent{}
	stmt, err := db.Prepare(`SELECT * FROM agents WHERE email = $1`)
	if err != nil {
		return Agent{}, err
	}
	defer stmt.Close()
	err = stmt.QueryRow(email).Scan(&a.Id, &a.Name, &a.Email, &a.Password, &a.Role, &a.IsActive, &a.CompanyId, &a.CreatedAt, &a.UpdatedAt)
	fmt.Println(err)
	return a, nil
}

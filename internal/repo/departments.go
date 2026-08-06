package repo

import (
	"database/sql"
	"time"
)

type Department struct {
	Id        int       `json:"id"`
	Name      string    `json:"name"`
	CompanyId int       `json:"companyId"`
	IsActive  bool      `json:"isActive"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type AgentAssignmentOption struct {
	Id            int    `json:"id"`
	Name          string `json:"name"`
	DepartmentIds []int  `json:"departmentIds"`
}

func GetDepartmentsByCompany(companyID int) ([]Department, error) {
	rows, err := db.Query(`SELECT id, name, "companyId", "isActive", "createdAt", "updatedAt"
		FROM departments WHERE "companyId" = $1 AND "isActive" = true ORDER BY name`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	departments := []Department{}
	for rows.Next() {
		var department Department
		if err := rows.Scan(&department.Id, &department.Name, &department.CompanyId, &department.IsActive, &department.CreatedAt, &department.UpdatedAt); err != nil {
			return nil, err
		}
		departments = append(departments, department)
	}
	return departments, rows.Err()
}

func CreateDepartment(companyID int, name string) (Department, error) {
	var department Department
	err := db.QueryRow(`INSERT INTO departments (name, "companyId") VALUES ($1, $2)
		RETURNING id, name, "companyId", "isActive", "createdAt", "updatedAt"`, name, companyID).Scan(
		&department.Id, &department.Name, &department.CompanyId, &department.IsActive, &department.CreatedAt, &department.UpdatedAt,
	)
	return department, err
}

func DeleteDepartment(id, companyID int) (bool, error) {
	result, err := db.Exec(`DELETE FROM departments WHERE id = $1 AND "companyId" = $2`, id, companyID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func GetAgentAssignmentOptions(companyID int) ([]AgentAssignmentOption, error) {
	rows, err := db.Query(`SELECT a.id, a.name, ad."departmentId"
		FROM agents a
		LEFT JOIN agent_departments ad ON ad."agentId" = a.id
		WHERE a."companyId" = $1 AND a."isActive" = true
		ORDER BY a.name, ad."departmentId"`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	options := []AgentAssignmentOption{}
	byID := map[int]int{}
	for rows.Next() {
		var id int
		var name string
		var departmentID sql.NullInt64
		if err := rows.Scan(&id, &name, &departmentID); err != nil {
			return nil, err
		}
		index, exists := byID[id]
		if !exists {
			index = len(options)
			byID[id] = index
			options = append(options, AgentAssignmentOption{Id: id, Name: name, DepartmentIds: []int{}})
		}
		if departmentID.Valid {
			options[index].DepartmentIds = append(options[index].DepartmentIds, int(departmentID.Int64))
		}
	}
	return options, rows.Err()
}

func ReplaceAgentDepartments(agentID, companyID int, departmentIDs []int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id = $1 AND "companyId" = $2)`, agentID, companyID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return sql.ErrNoRows
	}
	if _, err := tx.Exec(`DELETE FROM agent_departments WHERE "agentId" = $1`, agentID); err != nil {
		return err
	}
	for _, departmentID := range departmentIDs {
		result, err := tx.Exec(`INSERT INTO agent_departments ("agentId", "departmentId")
			SELECT $1, id FROM departments WHERE id = $2 AND "companyId" = $3 AND "isActive" = true`, agentID, departmentID, companyID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			if err != nil {
				return err
			}
			return sql.ErrNoRows
		}
	}
	return tx.Commit()
}

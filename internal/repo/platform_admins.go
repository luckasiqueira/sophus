package repo

import (
	"database/sql"
)

func CountPlatformAdmins() (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM platform_admins`).Scan(&count)
	return count, err
}

func CreateFirstPlatformAdmin(input CreatePlatformAdminInput) (PlatformAdmin, error) {
	tx, err := db.Begin()
	if err != nil {
		return PlatformAdmin{}, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`LOCK TABLE platform_admins IN EXCLUSIVE MODE`); err != nil {
		return PlatformAdmin{}, err
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM platform_admins`).Scan(&count); err != nil {
		return PlatformAdmin{}, err
	}
	if count != 0 {
		return PlatformAdmin{}, sql.ErrNoRows
	}
	if err := lockAgentEmail(tx, input.Email, 0); err != nil {
		return PlatformAdmin{}, err
	}

	admin, err := scanPlatformAdmin(tx.QueryRow(`INSERT INTO platform_admins (name, email, password)
		VALUES ($1, $2, $3)
		RETURNING id, name, email, password, "isActive", "sessionVersion", "createdAt", "updatedAt"`,
		input.Name, input.Email, input.Password))
	if err != nil {
		return PlatformAdmin{}, err
	}
	if err := tx.Commit(); err != nil {
		return PlatformAdmin{}, err
	}
	return admin, nil
}

func GetPlatformAdminByEmail(email string) (PlatformAdmin, error) {
	return scanPlatformAdmin(db.QueryRow(`SELECT id, name, email, password, "isActive", "sessionVersion", "createdAt", "updatedAt"
		FROM platform_admins WHERE lower(email) = lower($1)`, email))
}

func GetPlatformAdminById(id int) (PlatformAdmin, error) {
	return scanPlatformAdmin(db.QueryRow(`SELECT id, name, email, password, "isActive", "sessionVersion", "createdAt", "updatedAt"
		FROM platform_admins WHERE id = $1`, id))
}

func scanPlatformAdmin(scanner rowScanner) (PlatformAdmin, error) {
	var admin PlatformAdmin
	err := scanner.Scan(&admin.Id, &admin.Name, &admin.Email, &admin.Password, &admin.IsActive, &admin.SessionVersion, &admin.CreatedAt, &admin.UpdatedAt)
	return admin, err
}

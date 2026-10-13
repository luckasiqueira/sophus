package repo

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type PlatformSupportSession struct {
	ID          uuid.UUID        `json:"id"`
	CompanyID   int              `json:"companyId"`
	CompanyName string           `json:"companyName"`
	Admin       PlatformAdmin    `json:"-"`
	Agent       Agent            `json:"-"`
	Preferences AgentPreferences `json:"-"`
	ExpiresAt   time.Time        `json:"expiresAt"`
}

func supportTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func StartPlatformSupportSession(admin PlatformAdmin, companyID int, ip string) (string, PlatformSupportSession, error) {
	var session PlatformSupportSession
	if admin.Id <= 0 || !admin.IsActive || companyID <= 0 {
		return "", session, sql.ErrNoRows
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", session, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	tx, err := db.Begin()
	if err != nil {
		return "", session, err
	}
	defer tx.Rollback()
	var adminID int
	if err := tx.QueryRow(`SELECT id FROM platform_admins WHERE id=$1 AND "isActive"=true AND "sessionVersion"=$2 FOR SHARE`, admin.Id, admin.SessionVersion).Scan(&adminID); err != nil {
		return "", session, err
	}
	if err := tx.QueryRow(`SELECT name FROM companies WHERE id=$1 FOR SHARE`, companyID).Scan(&session.CompanyName); err != nil {
		return "", session, err
	}
	// Use the company's existing FK-compatible identity; support attribution and
	// preferences stay in the separate lease, never in that user's credentials.
	var agentID int
	if err := tx.QueryRow(`SELECT a.id FROM agents a JOIN companies c ON c.id=a."companyId"
		WHERE c.id=$1 ORDER BY CASE WHEN a.id=c."originalAdminId" THEN 0 WHEN a.role='admin' THEN 1 ELSE 2 END,
		a."isActive" DESC,a.id LIMIT 1 FOR SHARE OF a`, companyID).Scan(&agentID); err != nil {
		return "", session, err
	}
	session.ID, session.CompanyID, session.Admin = uuid.New(), companyID, admin
	session.Preferences = DefaultAgentPreferences()
	session.Preferences.Onboarding.Completed = true
	preferences, err := json.Marshal(session.Preferences)
	if err != nil {
		return "", session, err
	}
	err = tx.QueryRow(`INSERT INTO platform_support_sessions(id,"tokenHash","adminId","companyId","agentId","sessionVersion",preferences,"expiresAt")
		VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,now()+interval '1 hour') RETURNING "expiresAt"`, session.ID, supportTokenHash(token), admin.Id, companyID, agentID, admin.SessionVersion, string(preferences)).Scan(&session.ExpiresAt)
	if err != nil {
		return "", session, err
	}
	if _, err = tx.Exec(`INSERT INTO audit_events("companyId","actorId","actorEmail","actorType",action,"targetType","targetId","ipAddress")
		VALUES($1,$2,$3,'platform_support','support_start','support_session',$4,$5)`, companyID, admin.Id, admin.Email, session.ID.String(), ip); err != nil {
		return "", session, err
	}
	if err := tx.Commit(); err != nil {
		return "", session, err
	}
	return token, session, nil
}

const platformSupportSessionSQL = `SELECT s.id,s."companyId",c.name,s."expiresAt",s.preferences,
	a.id,a."sessionVersion",a."createdAt",a."updatedAt"
	FROM platform_support_sessions s JOIN companies c ON c.id=s."companyId"
	JOIN agents a ON a.id=s."agentId" AND a."companyId"=s."companyId"
	JOIN platform_admins owner ON owner.id=s."adminId" AND owner."isActive"=true AND owner."sessionVersion"=s."sessionVersion"
	WHERE s."tokenHash"=$1 AND s."adminId"=$2 AND s."sessionVersion"=$3 AND s."revokedAt" IS NULL AND s."expiresAt">now()`

func GetPlatformSupportSession(admin PlatformAdmin, token string) (PlatformSupportSession, error) {
	var session PlatformSupportSession
	if len(token) != 43 || !admin.IsActive {
		return session, sql.ErrNoRows
	}
	var preferences []byte
	err := db.QueryRow(platformSupportSessionSQL, supportTokenHash(token), admin.Id, admin.SessionVersion).Scan(&session.ID, &session.CompanyID, &session.CompanyName, &session.ExpiresAt, &preferences, &session.Agent.Id, &session.Agent.SessionVersion, &session.Agent.CreatedAt, &session.Agent.UpdatedAt)
	if err != nil {
		return session, err
	}
	session.Admin = admin
	session.Preferences = DefaultAgentPreferences()
	if err := json.Unmarshal(preferences, &session.Preferences); err != nil {
		return session, err
	}
	name := []rune("Suporte · " + admin.Name)
	if len(name) > 40 {
		name = name[:40]
	}
	session.Agent.Name, session.Agent.Email = string(name), admin.Email
	session.Agent.CompanyId, session.Agent.Role = session.CompanyID, RoleAdmin
	session.Agent.IsActive, session.Agent.IsSupport = true, true
	return session, nil
}

func EndPlatformSupportSession(admin PlatformAdmin, token, ip string) error {
	if len(token) != 43 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id uuid.UUID
	var companyID int
	err = tx.QueryRow(`UPDATE platform_support_sessions SET "revokedAt"=now() WHERE "tokenHash"=$1 AND "adminId"=$2 AND "revokedAt" IS NULL RETURNING id,"companyId"`, supportTokenHash(token), admin.Id).Scan(&id, &companyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO audit_events("companyId","actorId","actorEmail","actorType",action,"targetType","targetId","ipAddress") VALUES($1,$2,$3,'platform_support','support_end','support_session',$4,$5)`, companyID, admin.Id, admin.Email, id.String(), ip); err != nil {
		return err
	}
	return tx.Commit()
}

const updatePlatformSupportPreferencesSQL = `UPDATE platform_support_sessions SET preferences=$1::jsonb WHERE id=$2 AND "adminId"=$3 AND "companyId"=$4 AND "sessionVersion"=$5 AND "revokedAt" IS NULL AND "expiresAt">now()`

func UpdatePlatformSupportPreferences(session PlatformSupportSession, preferences AgentPreferences) error {
	preferences.Onboarding.Completed = true
	raw, err := json.Marshal(preferences)
	if err != nil {
		return err
	}
	result, err := db.Exec(updatePlatformSupportPreferencesSQL, string(raw), session.ID, session.Admin.Id, session.CompanyID, session.Admin.SessionVersion)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return sql.ErrNoRows
	}
	return nil
}

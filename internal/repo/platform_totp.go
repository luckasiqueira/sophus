package repo

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"sophus/internal/authenticator"
	"sophus/utils/env"
)

var ErrInvalidPlatformChallenge = errors.New("login expirado; entre novamente com email e senha")
var ErrInvalidTOTPCode = errors.New("código inválido ou já utilizado; confira o autenticador e tente novamente")

type PlatformLoginChallenge struct {
	AdminID          int
	Email            string
	SessionVersion   int
	SecretCiphertext string
	Enrollment       bool
}

func platformChallengeHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func StartPlatformLoginChallenge(admin PlatformAdmin) (string, error) {
	var encrypted string
	if err := db.QueryRow(`SELECT "totpSecretCiphertext" FROM platform_admins WHERE id = $1 AND "isActive" = true AND "sessionVersion" = $2`, admin.Id, admin.SessionVersion).Scan(&encrypted); err != nil {
		return "", err
	}
	enrollment := encrypted == ""
	if enrollment {
		secret, err := authenticator.GenerateSecret()
		if err != nil {
			return "", err
		}
		encrypted, err = authenticator.Encrypt(secret, strings.TrimSpace(env.Backend["SALT_JWT"]))
		if err != nil {
			return "", err
		}
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	if _, err := db.Exec(`DELETE FROM platform_login_challenges WHERE "expiresAt" <= now()`); err != nil {
		return "", err
	}
	_, err := db.Exec(`INSERT INTO platform_login_challenges ("tokenHash", "adminId", "sessionVersion", "secretCiphertext", enrollment, "expiresAt")
 VALUES ($1, $2, $3, $4, $5, now() + interval '5 minutes')`, platformChallengeHash(token), admin.Id, admin.SessionVersion, encrypted, enrollment)
	return token, err
}

func GetPlatformLoginChallenge(token string) (PlatformLoginChallenge, error) {
	var challenge PlatformLoginChallenge
	if len(token) != 43 {
		return challenge, ErrInvalidPlatformChallenge
	}
	err := db.QueryRow(`SELECT a.id, a.email, a."sessionVersion", challenge."secretCiphertext", challenge.enrollment
 FROM platform_login_challenges challenge JOIN platform_admins a ON a.id = challenge."adminId"
 WHERE challenge."tokenHash" = $1 AND challenge."expiresAt" > now() AND challenge.attempts < 5
 AND a."isActive" = true AND a."sessionVersion" = challenge."sessionVersion"`, platformChallengeHash(token)).Scan(
		&challenge.AdminID, &challenge.Email, &challenge.SessionVersion, &challenge.SecretCiphertext, &challenge.Enrollment)
	if errors.Is(err, sql.ErrNoRows) {
		return challenge, ErrInvalidPlatformChallenge
	}
	return challenge, err
}

func CompletePlatformLoginChallenge(token, code string) (PlatformAdmin, error) {
	if len(token) != 43 {
		return PlatformAdmin{}, ErrInvalidPlatformChallenge
	}
	tx, err := db.Begin()
	if err != nil {
		return PlatformAdmin{}, err
	}
	defer tx.Rollback()
	var adminID, version int
	var encrypted, configured string
	var enrollment bool
	var lastStep int64
	// Lock the administrator too: two challenges cannot consume the same code
	// or enroll different authenticators concurrently.
	err = tx.QueryRow(`SELECT a.id, a."sessionVersion", challenge."secretCiphertext", challenge.enrollment,
 a."totpSecretCiphertext", a."totpLastUsedStep"
 FROM platform_login_challenges challenge JOIN platform_admins a ON a.id = challenge."adminId"
 WHERE challenge."tokenHash" = $1 AND challenge."expiresAt" > now() AND challenge.attempts < 5
 AND a."isActive" = true AND a."sessionVersion" = challenge."sessionVersion"
 FOR UPDATE OF a, challenge`, platformChallengeHash(token)).Scan(&adminID, &version, &encrypted, &enrollment, &configured, &lastStep)
	if errors.Is(err, sql.ErrNoRows) {
		return PlatformAdmin{}, ErrInvalidPlatformChallenge
	}
	if err != nil {
		return PlatformAdmin{}, err
	}
	if (enrollment && configured != "") || (!enrollment && configured != encrypted) {
		return PlatformAdmin{}, ErrInvalidPlatformChallenge
	}
	secret, err := authenticator.Decrypt(encrypted, strings.TrimSpace(env.Backend["SALT_JWT"]))
	if err != nil {
		return PlatformAdmin{}, err
	}
	step, valid := authenticator.Validate(secret, code, time.Now())
	if !valid || step <= lastStep {
		if _, err := tx.Exec(`UPDATE platform_login_challenges SET attempts = attempts + 1 WHERE "tokenHash" = $1`, platformChallengeHash(token)); err != nil {
			return PlatformAdmin{}, err
		}
		if err := tx.Commit(); err != nil {
			return PlatformAdmin{}, err
		}
		return PlatformAdmin{}, ErrInvalidTOTPCode
	}
	admin, err := scanPlatformAdmin(tx.QueryRow(`UPDATE platform_admins SET "totpSecretCiphertext" = $1,
 "totpLastUsedStep" = $2, "sessionVersion" = "sessionVersion" + CASE WHEN $3 THEN 1 ELSE 0 END,
 "updatedAt" = now() WHERE id = $4 RETURNING id, name, email, password, "isActive", "sessionVersion", "createdAt", "updatedAt"`, encrypted, step, enrollment, adminID))
	if err != nil {
		return PlatformAdmin{}, err
	}
	if _, err := tx.Exec(`DELETE FROM platform_login_challenges WHERE "tokenHash" = $1`, platformChallengeHash(token)); err != nil {
		return PlatformAdmin{}, err
	}
	if err := tx.Commit(); err != nil {
		return PlatformAdmin{}, err
	}
	return admin, nil
}

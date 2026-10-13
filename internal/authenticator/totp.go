package authenticator

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

func GenerateSecret() (string, error) {
	var raw [20]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:]), nil
}

func URI(secret, email string) string {
	u := url.URL{Scheme: "otpauth", Host: "totp", Path: "/Zubly:" + email}
	q := url.Values{"secret": {secret}, "issuer": {"Zubly"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	u.RawQuery = q.Encode()
	return u.String()
}

func codeAt(secret string, step int64) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(key) < 20 {
		return "", errors.New("invalid TOTP secret")
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 15
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1000000), nil
}

// Accept the current 30-second step and one adjacent step for clock drift.
// Return the step so the database can atomically reject reused codes.
func Validate(secret, code string, now time.Time) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	current := now.Unix() / 30
	for _, step := range []int64{current, current - 1, current + 1} {
		if step < 0 {
			continue
		}
		expected, err := codeAt(secret, step)
		if err == nil && subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

func secretCipher(master string) (cipher.AEAD, error) {
	if strings.TrimSpace(master) == "" {
		return nil, errors.New("TOTP encryption key is empty")
	}
	key := sha256.Sum256([]byte(master + ":platform-totp"))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func Encrypt(secret, master string) (string, error) {
	gcm, err := secretCipher(master)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(secret), []byte("zubly-platform-totp-v1"))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func Decrypt(value, master string) (string, error) {
	gcm, err := secretCipher(master)
	if err != nil {
		return "", err
	}
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(sealed) < gcm.NonceSize()+gcm.Overhead() {
		return "", errors.New("invalid encrypted TOTP secret")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], []byte("zubly-platform-totp-v1"))
	return string(plain), err
}

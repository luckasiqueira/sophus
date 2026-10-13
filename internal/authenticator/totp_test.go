package authenticator

import (
	"encoding/base32"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestTOTPMatchesRFC6238SHA1Vectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for _, tc := range []struct {
		unix int64
		code string
	}{
		{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"},
		{1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"},
	} {
		step, valid := Validate(secret, tc.code, time.Unix(tc.unix, 0))
		if !valid || step != tc.unix/30 {
			t.Fatalf("RFC vector %d: got step=%d valid=%t", tc.unix, step, valid)
		}
	}
}

func TestTOTPClockDriftAndInvalidCodes(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	now := time.Unix(1234567890, 0)
	for _, offset := range []int64{-1, 0, 1} {
		expectedStep := now.Unix()/30 + offset
		code, _ := codeAt(secret, expectedStep)
		if step, valid := Validate(secret, code, now); !valid || step != expectedStep {
			t.Fatalf("clock drift %d rejected", offset)
		}
	}
	expired, _ := codeAt(secret, now.Unix()/30-2)
	for _, code := range []string{"", "12345", "1234567", "abcdef", "１２３４５６", expired} {
		if _, valid := Validate(secret, code, now); valid {
			t.Fatalf("invalid code %q accepted", code)
		}
	}
	if _, valid := Validate("invalid-secret", "123456", now); valid {
		t.Fatal("invalid secret accepted")
	}
}

func TestTOTPSecretEncryption(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := Encrypt(secret, "test-master-key")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ciphertext, secret) {
		t.Fatal("secret stored as plaintext")
	}
	if plain, err := Decrypt(ciphertext, "test-master-key"); err != nil || plain != secret {
		t.Fatalf("decrypt: %q %v", plain, err)
	}
	if _, err := Decrypt(ciphertext, "wrong-key"); err == nil {
		t.Fatal("wrong key accepted")
	}
	raw, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if len(raw) != 20 {
		t.Fatalf("secret entropy size %d", len(raw))
	}
	if _, err := Encrypt(secret, ""); err == nil {
		t.Fatal("empty encryption key accepted")
	}
	for _, value := range []string{"", "invalid!", ciphertext[:len(ciphertext)-4]} {
		if _, err := Decrypt(value, "test-master-key"); err == nil {
			t.Fatal("invalid ciphertext accepted")
		}
	}
	uri, err := url.Parse(URI(secret, "admin@example.com"))
	if err != nil || uri.Scheme != "otpauth" || uri.Host != "totp" || uri.Query().Get("secret") != secret || uri.Query().Get("issuer") != "Zubly" {
		t.Fatalf("invalid provisioning URI: %v", uri)
	}
}

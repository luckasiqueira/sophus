package repo

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAICredentialJSONDoesNotExposeCiphertext(t *testing.T) {
	raw, err := json.Marshal(AICredential{Id: 1, APIKeyCiphertext: "encrypted-secret", HasAPIKey: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "encrypted-secret") || strings.Contains(string(raw), "apiKeyCiphertext") {
		t.Fatalf("credential JSON exposed ciphertext: %s", raw)
	}
	if !strings.Contains(string(raw), `"hasApiKey":true`) {
		t.Fatalf("credential JSON omitted key presence indicator: %s", raw)
	}
}

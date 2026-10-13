package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetWhatsAppAvatar(t *testing.T) {
	originalURL := ApiBaseURL
	t.Cleanup(func() { ApiBaseURL = originalURL })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/user/avatar" || request.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("apikey") != "secret" {
			t.Fatalf("unexpected api key")
		}
		var body struct {
			Number  string `json:"number"`
			Preview bool   `json:"preview"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Number != "5511999999999@s.whatsapp.net" || !body.Preview {
			t.Fatalf("unexpected request body: %+v, error: %v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"URL":"https://pps.whatsapp.net/avatar.jpg"}}`)
	}))
	defer server.Close()
	ApiBaseURL = server.URL

	avatarURL, err := GetWhatsAppAvatar("5511999999999", "secret")
	if err != nil {
		t.Fatalf("GetWhatsAppAvatar returned error: %v", err)
	}
	if avatarURL != "https://pps.whatsapp.net/avatar.jpg" {
		t.Fatalf("unexpected avatar URL: %q", avatarURL)
	}
}

func TestGetWhatsAppAvatarForConnectionUsesEvolutionGOEndpoint(t *testing.T) {
	originalURL := ApiBaseURL
	t.Cleanup(func() { ApiBaseURL = originalURL })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/user/avatar" || request.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("apikey") != "secret" {
			t.Fatalf("unexpected api key")
		}
		var body struct {
			Number string `json:"number"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Number != "5522998959776@s.whatsapp.net" {
			t.Fatalf("avatar query must use canonical JID without '+': %+v, error: %v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"profilePictureUrl":"https://pps.whatsapp.net/avatar.jpg"}`)
	}))
	defer server.Close()
	ApiBaseURL = server.URL

	avatarURL, err := GetWhatsAppAvatarForConnection("+55 (22) 99895-9776", ConnectionEVO{APIToken: "secret"})
	if err != nil {
		t.Fatalf("getWhatsAppAvatar returned error: %v", err)
	}
	if avatarURL != "https://pps.whatsapp.net/avatar.jpg" {
		t.Fatalf("unexpected avatar URL: %q", avatarURL)
	}
}

func TestGetWhatsAppAvatarReportsFailedAttempts(t *testing.T) {
	originalURL := ApiBaseURL
	t.Cleanup(func() { ApiBaseURL = originalURL })
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	ApiBaseURL = server.URL

	_, err := GetWhatsAppAvatar("5511999999999", "secret")
	if err == nil || !strings.Contains(err.Error(), "Evolution GO /user/avatar") || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected Evolution GO diagnostic, got %v", err)
	}
}

func TestGetWhatsAppAvatarSchedulesRetryForTemporaryFailure(t *testing.T) {
	originalURL := ApiBaseURL
	t.Cleanup(func() { ApiBaseURL = originalURL })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	ApiBaseURL = server.URL

	_, err := GetWhatsAppAvatar("5511999999999", "secret")
	if !errors.Is(err, ErrTemporaryAvatarLookup) {
		t.Fatalf("expected temporary lookup error for HTTP 503, got %v", err)
	}
}

func TestGetWhatsAppAvatarClassifiesBackendFailure(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		want      string
		temporary bool
	}{
		{"whatsapp timeout", `{"error":"context deadline exceeded"}`, "tempo esgotado na consulta ao WhatsApp", true},
		{"disconnected", `{"error":"client disconnected"}`, "sessão WhatsApp desconectada", true},
		{"no photo", `{"error":"no profile picture found"}`, "contato sem foto de perfil", false},
		{"redacted unknown", `{"error":"user 5511999999999 private"}`, "erro HTTP 500", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			originalURL := ApiBaseURL
			t.Cleanup(func() { ApiBaseURL = originalURL })
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			ApiBaseURL = server.URL
			_, err := GetWhatsAppAvatar("5511999999999", "secret")
			if err == nil || !strings.Contains(err.Error(), tc.want) || errors.Is(err, ErrTemporaryAvatarLookup) != tc.temporary {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(err.Error(), "5511999999999") {
				t.Fatal("diagnostic leaked contact number")
			}
		})
	}
}

func TestGetWhatsAppAvatarRecognizesRemovedPhoto(t *testing.T) {
	oldURL := ApiBaseURL
	t.Cleanup(func() { ApiBaseURL = oldURL })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `{"error":"no profile picture found"}`)
	}))
	defer server.Close()
	ApiBaseURL = server.URL
	_, err := GetWhatsAppAvatar("5511999999999", "secret")
	if !errors.Is(err, ErrAvatarNotFound) || errors.Is(err, ErrTemporaryAvatarLookup) {
		t.Fatalf("removed photo error = %v", err)
	}
}

func TestGetWhatsAppAvatarUsesLIDIdentity(t *testing.T) {
	oldURL := ApiBaseURL
	t.Cleanup(func() { ApiBaseURL = oldURL })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Number string `json:"number"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input.Number != "123456789@lid" {
			t.Errorf("lookup identity = %q", input.Number)
		}
		fmt.Fprint(w, `{"data":{"URL":"https://pps.whatsapp.net/new.jpg"}}`)
	}))
	defer server.Close()
	ApiBaseURL = server.URL
	value, err := GetWhatsAppAvatar("123456789:4@lid", "secret")
	if err != nil || value == "" {
		t.Fatalf("LID lookup = %q, %v", value, err)
	}
}

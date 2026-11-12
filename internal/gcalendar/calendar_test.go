package gcalendar

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testServiceAccount(t *testing.T, tokenURL string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(map[string]string{
		"type": "service_account", "client_email": "agenda@test-project.iam.gserviceaccount.com",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "token_uri": tokenURL,
	})
	return string(encoded)
}

func TestParseServiceAccountRejectsInvalidKeys(t *testing.T) {
	for _, raw := range []string{"", "{}", `{"type":"service_account","client_email":"a@b","private_key":"x"}`, `{"type":"authorized_user"}`} {
		if _, err := ParseServiceAccount(raw); err == nil {
			t.Fatalf("%q must be rejected", raw)
		}
	}
}

func TestBusyAndCreateEvent(t *testing.T) {
	var created map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/token":
			form, _ := url.ParseQuery(string(body))
			if form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || strings.Count(form.Get("assertion"), ".") != 2 {
				http.Error(w, `{"error_description":"bad assertion"}`, 400)
				return
			}
			io.WriteString(w, `{"access_token":"token-1","expires_in":3600}`)
		case "/freeBusy":
			if r.Header.Get("Authorization") != "Bearer token-1" {
				http.Error(w, "unauthorized", 401)
				return
			}
			io.WriteString(w, `{"calendars":{"agenda@example.com":{"busy":[{"start":"2026-11-12T13:00:00Z","end":"2026-11-12T14:00:00Z"}]}}}`)
		case "/calendars/agenda@example.com/events":
			json.Unmarshal(body, &created)
			io.WriteString(w, `{"id":"evt1"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	previous, previousToken := APIBase, defaultTokenURL
	APIBase, defaultTokenURL = server.URL, server.URL+"/token"
	defer func() { APIBase, defaultTokenURL = previous, previousToken }()

	// The token_uri of the file is ignored in favor of Google's endpoint.
	account, err := ParseServiceAccount(testServiceAccount(t, "http://169.254.169.254/token"))
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	busy, err := account.Busy(context.Background(), "agenda@example.com", day, day.Add(24*time.Hour))
	if err != nil || len(busy) != 1 || busy[0].Start.Hour() != 13 {
		t.Fatalf("busy = %+v, %v", busy, err)
	}
	id, err := account.CreateEvent(context.Background(), "agenda@example.com", Event{Summary: "Reunião", Start: day.Add(15 * time.Hour), End: day.Add(16 * time.Hour), TimeZone: "UTC"})
	if err != nil || id != "evt1" || created["summary"] != "Reunião" {
		t.Fatalf("id=%q err=%v created=%v", id, err, created)
	}
	if _, err := account.Busy(context.Background(), "outra@example.com", day, day.Add(time.Hour)); err == nil {
		t.Fatal("a calendar missing from the answer must fail")
	}
}

func TestFreeSlots(t *testing.T) {
	day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	busy := []Interval{{Start: day.Add(10 * time.Hour), End: day.Add(11*time.Hour + 30*time.Minute)}}
	slots := FreeSlots(day, 9*time.Hour, 13*time.Hour, time.Hour, busy, day.Add(8*time.Hour))
	var hours []int
	for _, slot := range slots {
		hours = append(hours, slot.Hour())
	}
	if len(hours) != 2 || hours[0] != 9 || hours[1] != 12 {
		t.Fatalf("free slots = %v", hours)
	}
	if late := FreeSlots(day, 9*time.Hour, 13*time.Hour, time.Hour, nil, day.Add(11*time.Hour+time.Minute)); len(late) != 1 || late[0].Hour() != 12 {
		t.Fatalf("past slots must be skipped: %v", late)
	}
}

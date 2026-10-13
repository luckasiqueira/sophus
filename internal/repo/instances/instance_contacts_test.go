package instances

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"sophus/internal/repo"
)

func TestGetContacts(t *testing.T) {
	originalURL := repo.ApiBaseURL
	t.Cleanup(func() { repo.ApiBaseURL = originalURL })
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/user/contacts" || request.Method != http.MethodGet {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("apikey") != "instance-key" {
			t.Fatalf("unexpected API key: %q", request.Header.Get("apikey"))
		}
		_, _ = response.Write([]byte(`{"message":"success","data":[{"Jid":"5511999999999@s.whatsapp.net","Found":true,"FullName":"Maria Silva","BusinessName":"ACME"}]}`))
	}))
	defer server.Close()
	repo.ApiBaseURL = server.URL

	contacts, err := (&InstanceEVO{APIToken: "instance-key"}).GetContacts()
	if err != nil {
		t.Fatalf("get contacts: %v", err)
	}
	if len(contacts) != 1 || contacts[0].PreferredName() != "Maria Silva" || contacts[0].BusinessName != "ACME" {
		t.Fatalf("unexpected contacts: %#v", contacts)
	}
}

func TestImportableContactsExcludeGroupOnlyPushNames(t *testing.T) {
	for _, test := range []struct {
		name    string
		contact Contact
		want    bool
	}{
		{"saved full name", Contact{Found: true, FullName: "Maria", PushName: "Maria do grupo"}, true},
		{"saved first name", Contact{FirstName: "João"}, true},
		{"group participant cache", Contact{Found: true, PushName: "Participante do grupo"}, false},
		{"resolved business only", Contact{Found: true, BusinessName: "Empresa"}, false},
		{"whitespace saved name", Contact{FullName: "  ", FirstName: " ", PushName: "Pessoa"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.contact.IsAddressBookContact(); got != test.want {
				t.Fatalf("address-book eligibility=%t, want %t", got, test.want)
			}
		})
	}
}

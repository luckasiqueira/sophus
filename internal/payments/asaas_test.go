package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAsaasCreatePaymentLink(t *testing.T) {
	var payload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v3/paymentLinks" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("access_token"); got != "api-key" {
			t.Errorf("access_token = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("User-Agent is required by Asaas")
		}
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		fmt.Fprint(w, `{"id":"lnk_123","name":"Fatura #42 - Plano Pro","url":"https://sandbox.asaas.com/c/lnk_123","value":49.9,"active":true,"deleted":false,"billingType":"UNDEFINED","chargeType":"DETACHED","endDate":"2026-10-20","externalReference":"42:key"}`)
	}))
	defer server.Close()

	client := NewAsaasClient(server.Client(), "api-key", server.URL+"/v3/")
	link, err := client.CreatePaymentLink(context.Background(), CreateAsaasPaymentLinkRequest{
		Name:              "Fatura #42 - Plano Pro",
		Description:       "Acme",
		AmountCents:       4990,
		EndDate:           time.Date(2026, 10, 20, 23, 59, 0, 0, time.FixedZone("BRT", -3*60*60)),
		ExternalReference: AsaasLinkReference(42, "key"),
	})
	if err != nil {
		t.Fatalf("create payment link: %v", err)
	}
	if link.ID != "lnk_123" || link.URL != "https://sandbox.asaas.com/c/lnk_123" {
		t.Fatalf("unexpected link: %#v", link)
	}
	if payload["billingType"] != "UNDEFINED" || payload["chargeType"] != "DETACHED" || payload["value"] != json.Number("49.90") ||
		payload["endDate"] != "2026-10-20" || payload["externalReference"] != "42:key" || payload["notificationEnabled"] != false ||
		payload["name"] != "Fatura #42 - Plano Pro" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAsaasCreatePaymentLinkRejectsForeignReference(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"id":"lnk_123","url":"https://asaas.com/c/lnk_123","externalReference":"99:other"}`)
	}))
	defer server.Close()

	client := NewAsaasClient(server.Client(), "api-key", server.URL)
	_, err := client.CreatePaymentLink(context.Background(), CreateAsaasPaymentLinkRequest{
		Name: "Fatura #42", AmountCents: 100, EndDate: time.Now(), ExternalReference: "42:key",
	})
	if err == nil {
		t.Fatal("expected mismatched reference to be rejected")
	}
}

func TestAsaasDefaultBaseURL(t *testing.T) {
	if client := NewAsaasClient(http.DefaultClient, "key", " "); client.baseURL != "https://api.asaas.com/v3" {
		t.Fatalf("baseURL = %q", client.baseURL)
	}
	if client := NewAsaasClient(http.DefaultClient, "key", "https://api-sandbox.asaas.com/v3/"); client.baseURL != "https://api-sandbox.asaas.com/v3" {
		t.Fatalf("sandbox baseURL = %q", client.baseURL)
	}
}

func TestAsaasErrorsAreTyped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"errors":[{"code":"invalid_value","description":"Valor inválido"}]}`)
	}))
	defer server.Close()

	_, err := NewAsaasClient(server.Client(), "key", server.URL).GetPayment(context.Background(), "pay_1")
	var httpError *HTTPError
	if !errors.As(err, &httpError) || httpError.Provider != "Asaas" || httpError.StatusCode != http.StatusBadRequest || httpError.Message != "Valor inválido" {
		t.Fatalf("error = %#v", err)
	}
	if !IsDefinitiveHTTPFailure(err) {
		t.Fatal("400 should be definitive")
	}
}

func TestAsaasGetPayment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/payments/pay_123" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"object":"payment","id":"pay_123","status":"RECEIVED","value":49.9,"billingType":"PIX","paymentLink":"lnk_123","transactionReceiptUrl":"https://asaas.com/comprovantes/1","deleted":false}`)
	}))
	defer server.Close()

	payment, err := NewAsaasClient(server.Client(), "key", server.URL).GetPayment(context.Background(), "pay_123")
	if err != nil {
		t.Fatalf("get payment: %v", err)
	}
	if payment.Status != "RECEIVED" || payment.PaymentLink != "lnk_123" || payment.Value != json.Number("49.9") ||
		payment.TransactionReceiptURL != "https://asaas.com/comprovantes/1" {
		t.Fatalf("unexpected payment: %#v", payment)
	}
}

func TestAsaasGetPaymentRejectsMismatchedID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"id":"pay_other","status":"RECEIVED","value":1}`)
	}))
	defer server.Close()

	if _, err := NewAsaasClient(server.Client(), "key", server.URL).GetPayment(context.Background(), "pay_123"); err == nil {
		t.Fatal("expected mismatched ID to be rejected")
	}
}

func TestAsaasFindPaymentLink(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/paymentLinks" || r.URL.Query().Get("name") != "Fatura #42" {
			t.Errorf("request = %s %s", r.URL.Path, r.URL.RawQuery)
		}
		if r.URL.Query().Get("offset") == "0" {
			fmt.Fprint(w, `{"hasMore":true,"data":[{"id":"lnk_other","url":"https://asaas.com/c/1","externalReference":"420:key"},{"id":"lnk_deleted","url":"https://asaas.com/c/2","externalReference":"42:key","deleted":true}]}`)
			return
		}
		fmt.Fprint(w, `{"hasMore":false,"data":[{"id":"lnk_123","url":"https://asaas.com/c/3","externalReference":"42:key"}]}`)
	}))
	defer server.Close()

	link, found, err := NewAsaasClient(server.Client(), "key", server.URL).FindPaymentLink(context.Background(), 42, "key")
	if err != nil || !found || link.ID != "lnk_123" {
		t.Fatalf("link = %#v, found = %v, err = %v", link, found, err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestAsaasDeletePaymentLinkVerifiesIdentity(t *testing.T) {
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			fmt.Fprint(w, `{"id":"lnk_123","url":"https://asaas.com/c/3","externalReference":"42:key"}`)
		case http.MethodDelete:
			deleted = true
			fmt.Fprint(w, `{"id":"lnk_123","deleted":true}`)
		}
	}))
	defer server.Close()
	client := NewAsaasClient(server.Client(), "key", server.URL)

	if err := client.DeletePaymentLink(context.Background(), "lnk_123", "42:other"); err == nil || deleted {
		t.Fatalf("foreign link deletion err = %v, deleted = %v", err, deleted)
	}
	if err := client.DeletePaymentLink(context.Background(), "lnk_123", "42:key"); err != nil || !deleted {
		t.Fatalf("delete err = %v, deleted = %v", err, deleted)
	}
}

func TestAsaasUpdatePaymentLinkConfirmsStoredValues(t *testing.T) {
	stored := `{"id":"lnk_123","url":"https://asaas.com/c/3","externalReference":"42:key","value":10,"endDate":"2026-10-01"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			stored = fmt.Sprintf(`{"id":"lnk_123","url":"https://asaas.com/c/3","externalReference":"42:key","value":%v,"endDate":%q}`, payload["value"], payload["endDate"])
		}
		fmt.Fprint(w, stored)
	}))
	defer server.Close()
	client := NewAsaasClient(server.Client(), "key", server.URL)
	endDate := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	if err := client.UpdatePaymentLink(context.Background(), "lnk_123", "42:key", 12345, endDate); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !strings.Contains(stored, `"value":123.45`) || !strings.Contains(stored, `"endDate":"2026-11-05"`) {
		t.Fatalf("stored = %s", stored)
	}
}

func TestAsaasAmountCents(t *testing.T) {
	for _, test := range []struct {
		value   string
		want    int64
		wantErr bool
	}{
		{value: "49.9", want: 4990},
		{value: "49.90", want: 4990},
		{value: "49", want: 4900},
		{value: "0.01", want: 1},
		{value: "10.500", want: 1050},
		{value: "10.005", wantErr: true},
		{value: "-1", wantErr: true},
		{value: "1e3", wantErr: true},
		{value: "", wantErr: true},
	} {
		got, err := AsaasAmountCents(json.Number(test.value))
		if test.wantErr != (err != nil) || got != test.want {
			t.Errorf("AsaasAmountCents(%q) = %d, %v", test.value, got, err)
		}
	}
}

func TestAsaasLinkName(t *testing.T) {
	if got := AsaasLinkName(7, "Pro"); got != "Fatura #7 - Plano Pro" {
		t.Fatalf("name = %q", got)
	}
	if got := AsaasLinkName(7, strings.Repeat("ã", 200)); len(got) > asaasMaxLinkNameLength || !strings.HasPrefix(got, "Fatura #7") {
		t.Fatalf("long name = %q", got)
	}
}

func TestNormalizeAsaasBillingType(t *testing.T) {
	for _, test := range []struct {
		value, want string
		ok          bool
	}{
		{value: "", want: "UNDEFINED", ok: true},
		{value: "pix", want: "PIX", ok: true},
		{value: " BOLETO ", want: "BOLETO", ok: true},
		{value: "credit_card", want: "CREDIT_CARD", ok: true},
		{value: "UNDEFINED", want: "UNDEFINED", ok: true},
		{value: "DEBIT_CARD"},
		{value: "cartao"},
	} {
		got, ok := NormalizeAsaasBillingType(test.value)
		if got != test.want || ok != test.ok {
			t.Errorf("NormalizeAsaasBillingType(%q) = %q, %v", test.value, got, ok)
		}
	}
}

func TestAsaasCreatePaymentLinkWithFixedMethod(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&payload)
		fmt.Fprint(w, `{"id":"lnk_1","url":"https://asaas.test/c/1","value":10,"billingType":"PIX","externalReference":"1:key"}`)
	}))
	defer server.Close()
	client := NewAsaasClient(server.Client(), "key", server.URL)
	request := CreateAsaasPaymentLinkRequest{Name: "Fatura #1", AmountCents: 1000, EndDate: time.Now(), ExternalReference: "1:key", BillingType: "pix"}
	if _, err := client.CreatePaymentLink(context.Background(), request); err != nil {
		t.Fatalf("create: %v", err)
	}
	if payload["billingType"] != "PIX" {
		t.Fatalf("billingType = %v", payload["billingType"])
	}
	request.BillingType = "DEBIT_CARD"
	if _, err := client.CreatePaymentLink(context.Background(), request); err == nil {
		t.Fatal("unsupported billing type was accepted")
	}
}

func TestAsaasFindPaymentLinksReturnsEveryMethod(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"hasMore":false,"data":[{"id":"lnk_pix","url":"https://asaas.test/c/1","billingType":"PIX","externalReference":"42:key"},{"id":"lnk_boleto","url":"https://asaas.test/c/2","billingType":"BOLETO","externalReference":"42:key"},{"id":"lnk_other","url":"https://asaas.test/c/3","externalReference":"42:other"}]}`)
	}))
	defer server.Close()
	links, err := NewAsaasClient(server.Client(), "key", server.URL).FindPaymentLinks(context.Background(), 42, "key")
	if err != nil || len(links) != 2 || links[0].ID != "lnk_pix" || links[1].ID != "lnk_boleto" {
		t.Fatalf("links = %#v, err = %v", links, err)
	}
}

func TestAsaasDeletePaymentLinkToleratesMissingLink(t *testing.T) {
	deletes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes++
		}
		switch r.URL.Path {
		case "/paymentLinks/lnk_gone":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"errors":[{"code":"not_found","description":"Link não encontrado"}]}`)
		case "/paymentLinks/lnk_deleted":
			fmt.Fprint(w, `{"id":"lnk_deleted","url":"https://asaas.test/c/1","externalReference":"42:key","deleted":true}`)
		}
	}))
	defer server.Close()
	client := NewAsaasClient(server.Client(), "key", server.URL)
	for _, linkID := range []string{"lnk_gone", "lnk_deleted"} {
		if err := client.DeletePaymentLink(context.Background(), linkID, "42:key"); err != nil {
			t.Fatalf("delete %s: %v", linkID, err)
		}
	}
	if deletes != 0 {
		t.Fatalf("deletes = %d, want none for links already gone", deletes)
	}
}

package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCreatePreference(t *testing.T) {
	var received map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/checkout/preferences" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"preference-1","init_point":"https://pay.example/preference-1","sandbox_init_point":"https://sandbox.example/preference-1"}`)
	}))
	defer server.Close()

	expiresAt := time.Date(2026, time.August, 19, 12, 30, 0, 0, time.FixedZone("BRT", -3*60*60))
	client := NewMercadoPagoClient(server.Client(), "access-token", "webhook-secret", server.URL)
	response, err := client.CreatePreference(context.Background(), CreatePreferenceRequest{
		Title:             "Zubly subscription",
		AmountCents:       12345,
		PayerEmail:        "buyer@example.com",
		ExternalReference: "payment-42",
		NotificationURL:   "https://app.example.com/webhooks/mercadopago",
		BackURLs: BackURLs{
			Success: "https://app.example.com/payments/success",
			Failure: "https://app.example.com/payments/failure",
			Pending: "https://app.example.com/payments/pending",
		},
		ExpirationDate: expiresAt,
		CheckoutKey:    "checkout-secret",
	})
	if err != nil {
		t.Fatalf("create preference: %v", err)
	}
	if response.ID != "preference-1" || response.InitPoint != "https://pay.example/preference-1" {
		t.Fatalf("unexpected response: %#v", response)
	}

	items, ok := received["items"].([]interface{})
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v", received["items"])
	}
	item := items[0].(map[string]interface{})
	if item["title"] != "Zubly subscription" || item["quantity"] != json.Number("1") {
		t.Fatalf("unexpected item: %#v", item)
	}
	if item["unit_price"] != json.Number("123.45") || item["currency_id"] != "BRL" {
		t.Fatalf("unexpected price: %#v", item)
	}
	payer := received["payer"].(map[string]interface{})
	if payer["email"] != "buyer@example.com" {
		t.Fatalf("unexpected payer: %#v", payer)
	}
	if received["external_reference"] != "payment-42" || received["notification_url"] != "https://app.example.com/webhooks/mercadopago" {
		t.Fatalf("unexpected references: %#v", received)
	}
	backURLs := received["back_urls"].(map[string]interface{})
	if backURLs["success"] != "https://app.example.com/payments/success" || received["auto_return"] != "approved" {
		t.Fatalf("unexpected return configuration: %#v", received)
	}
	if received["expires"] != true || received["expiration_date_to"] != expiresAt.Format(time.RFC3339) {
		t.Fatalf("unexpected expiration: %#v", received)
	}
	metadata := received["metadata"].(map[string]interface{})
	if metadata["checkout_key"] != "checkout-secret" {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
}

func TestFindPreference(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/checkout/preferences/search":
			if r.URL.Query().Get("external_reference") != "42" {
				t.Errorf("external_reference = %q", r.URL.Query().Get("external_reference"))
			}
			fmt.Fprint(w, `{"elements":[{"id":"wrong","external_reference":"42"},{"id":"preference-42","external_reference":"42"}]}`)
		case "/checkout/preferences/wrong":
			fmt.Fprint(w, `{"id":"wrong","init_point":"https://pay.example/wrong","external_reference":"42","metadata":{"checkout_key":"other"}}`)
		case "/checkout/preferences/preference-42":
			fmt.Fprint(w, `{"id":"preference-42","init_point":"https://pay.example/42","external_reference":"42","metadata":{"checkout_key":"secret"}}`)
		default:
			t.Errorf("unexpected request path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewMercadoPagoClient(server.Client(), "access-token", "", server.URL)
	preference, found, err := client.FindPreference(context.Background(), "42", "secret")
	if err != nil {
		t.Fatalf("find preference: %v", err)
	}
	if !found || preference.ID != "preference-42" || preference.InitPoint != "https://pay.example/42" {
		t.Fatalf("unexpected preference: found=%v preference=%#v", found, preference)
	}
}

func TestGetPayment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/payments/987654" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":987654,"status":"approved","currency_id":"BRL","external_reference":"payment-42","metadata":{"checkout_key":"checkout-secret"},"date_approved":"2026-08-18T10:15:30-03:00","transaction_amount":89.90}`)
	}))
	defer server.Close()

	client := NewMercadoPagoClient(server.Client(), "access-token", "", server.URL)
	payment, err := client.GetPayment(context.Background(), "987654")
	if err != nil {
		t.Fatalf("get payment: %v", err)
	}
	if payment.ID != "987654" || payment.Status != "approved" || payment.CurrencyID != "BRL" || payment.CheckoutKey != "checkout-secret" || payment.ExternalReference != "payment-42" {
		t.Fatalf("unexpected payment: %#v", payment)
	}
	if payment.TransactionAmount.String() != "89.90" {
		t.Fatalf("transaction amount = %s", payment.TransactionAmount)
	}
	wantDate, _ := time.Parse(time.RFC3339, "2026-08-18T10:15:30-03:00")
	if payment.DateApproved == nil || !payment.DateApproved.Equal(wantDate) {
		t.Fatalf("date approved = %v", payment.DateApproved)
	}
}

func TestAPIErrorIsUsefulAndBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, strings.Repeat("invalid payment ", 1000))
	}))
	defer server.Close()

	client := NewMercadoPagoClient(server.Client(), "access-token", "", server.URL)
	_, err := client.GetPayment(context.Background(), "123")
	if err == nil {
		t.Fatal("expected API error")
	}
	if !strings.Contains(err.Error(), "400 Bad Request") || !strings.Contains(err.Error(), "invalid payment") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(err.Error()) > maxErrorBodySize+200 {
		t.Fatalf("error is not bounded: %d bytes", len(err.Error()))
	}
}

func TestVerifyWebhookSignature(t *testing.T) {
	const (
		secret    = "webhook-secret"
		dataID    = "ABC123"
		requestID = "request-456"
		timestamp = "1723996800"
	)
	manifest := "id:abc123;request-id:request-456;ts:1723996800;"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(manifest))
	header := "ts=" + timestamp + ", v1=" + hex.EncodeToString(mac.Sum(nil))

	client := NewMercadoPagoClient(http.DefaultClient, "access-token", secret, "")
	if !client.VerifyWebhookSignature(dataID, requestID, header) {
		t.Fatal("expected valid signature")
	}
	if client.VerifyWebhookSignature(dataID, "different-request", header) {
		t.Fatal("signature must not validate for a different request")
	}
	if client.VerifyWebhookSignature(dataID, requestID, header[:len(header)-1]+"0") {
		t.Fatal("changed signature must not validate")
	}
	if client.VerifyWebhookSignature(dataID, requestID, "ts="+timestamp) {
		t.Fatal("header without v1 must not validate")
	}

	clientWithoutSecret := NewMercadoPagoClient(http.DefaultClient, "access-token", "", "")
	if clientWithoutSecret.VerifyWebhookSignature(dataID, requestID, header) {
		t.Fatal("signature must not validate without a configured secret")
	}
}

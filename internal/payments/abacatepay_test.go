package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAbacatePayCreateProduct(t *testing.T) {
	var payload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/products/create" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer api-key" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		fmt.Fprint(w, `{"data":{"id":"prod_123","externalId":"plan/basic","name":"Basic plan","price":4990,"currency":"BRL","description":"Monthly access","status":"ACTIVE","cycle":null},"success":true,"error":null}`)
	}))
	defer server.Close()

	client := NewAbacatePayClient(server.Client(), "api-key", server.URL+"/v2/")
	product, err := client.CreateProduct(context.Background(), "plan/basic", "Basic plan", 4990, "Monthly access")
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	if product.ID != "prod_123" || product.ExternalID != "plan/basic" || product.Price != 4990 || product.Currency != "BRL" {
		t.Fatalf("unexpected product: %#v", product)
	}
	if len(payload) != 5 || payload["externalId"] != "plan/basic" || payload["name"] != "Basic plan" || payload["price"] != json.Number("4990") || payload["currency"] != "BRL" || payload["description"] != "Monthly access" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAbacatePayGetProductByExternalID(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/products/get" {
				t.Errorf("request = %s %s", r.Method, r.URL.Path)
			}
			if got := r.URL.Query().Get("externalId"); got != "plan/basic ?" {
				t.Errorf("externalId = %q", got)
			}
			fmt.Fprint(w, `{"data":{"id":"prod_123","externalId":"plan/basic ?","name":"Basic","price":4990,"currency":"BRL","description":"Access","status":"ACTIVE","cycle":null},"success":true,"error":null}`)
		}))
		defer server.Close()

		client := NewAbacatePayClient(server.Client(), "api-key", server.URL)
		product, found, err := client.GetProductByExternalID(context.Background(), "plan/basic ?")
		if err != nil {
			t.Fatalf("get product: %v", err)
		}
		if !found || product.ID != "prod_123" || product.ExternalID != "plan/basic ?" {
			t.Fatalf("found = %v, product = %#v", found, product)
		}
	})

	t.Run("not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "product not found", http.StatusNotFound)
		}))
		defer server.Close()

		client := NewAbacatePayClient(server.Client(), "api-key", server.URL)
		product, found, err := client.GetProductByExternalID(context.Background(), "missing")
		if err != nil {
			t.Fatalf("get missing product: %v", err)
		}
		if found || product != (AbacateProduct{}) {
			t.Fatalf("found = %v, product = %#v", found, product)
		}
	})
}

func TestAbacatePayCreateCheckout(t *testing.T) {
	var payload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/checkouts/create" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer checkout-key" {
			t.Errorf("Authorization = %q", got)
		}
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		fmt.Fprint(w, `{"data":{"id":"bill_123","externalId":"payment-42","url":"https://pay.example/bill_123","amount":8900,"paidAmount":0,"status":"PENDING","receiptUrl":null,"metadata":{"checkout_key":"secret-42"},"updatedAt":"2026-08-18T13:30:00Z"},"success":true,"error":null}`)
	}))
	defer server.Close()

	client := NewAbacatePayClient(server.Client(), "checkout-key", server.URL)
	checkout, err := client.CreateCheckout(
		context.Background(),
		"prod_123",
		"payment-42",
		"https://app.example/billing",
		"https://app.example/billing/complete",
		"secret-42",
	)
	if err != nil {
		t.Fatalf("create checkout: %v", err)
	}
	if checkout.ID != "bill_123" || checkout.URL != "https://pay.example/bill_123" || checkout.Amount != 8900 || checkout.Status != "PENDING" {
		t.Fatalf("unexpected checkout: %#v", checkout)
	}
	items, ok := payload["items"].([]interface{})
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v", payload["items"])
	}
	item := items[0].(map[string]interface{})
	if item["id"] != "prod_123" || item["quantity"] != json.Number("1") {
		t.Fatalf("item = %#v", item)
	}
	methods := payload["methods"].([]interface{})
	if len(methods) != 2 || methods[0] != "PIX" || methods[1] != "CARD" {
		t.Fatalf("methods = %#v", methods)
	}
	if payload["externalId"] != "payment-42" || payload["returnUrl"] != "https://app.example/billing" || payload["completionUrl"] != "https://app.example/billing/complete" {
		t.Fatalf("checkout fields = %#v", payload)
	}
	metadata := payload["metadata"].(map[string]interface{})
	if len(metadata) != 1 || metadata["checkout_key"] != "secret-42" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestAbacatePayFindCheckoutValidatesDetail(t *testing.T) {
	var detailRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/checkouts/list":
			if r.Method != http.MethodGet || r.URL.Query().Get("externalId") != "payment-42" || r.URL.Query().Get("limit") != "100" {
				t.Errorf("list request = %s %s", r.Method, r.URL.RawQuery)
			}
			fmt.Fprint(w, `{"data":[{"id":"bill_wrong_external"},{"id":"bill_wrong_key"},{"id":"bill_match"}],"success":true,"error":null}`)
		case "/checkouts/get":
			detailRequests++
			switch r.URL.Query().Get("id") {
			case "bill_wrong_external":
				fmt.Fprint(w, checkoutEnvelope("bill_wrong_external", "other-payment", "secret-42"))
			case "bill_wrong_key":
				fmt.Fprint(w, checkoutEnvelope("bill_wrong_key", "payment-42", "other-secret"))
			case "bill_match":
				fmt.Fprint(w, checkoutEnvelope("bill_match", "payment-42", "secret-42"))
			default:
				t.Errorf("unexpected checkout ID %q", r.URL.Query().Get("id"))
				http.NotFound(w, r)
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewAbacatePayClient(server.Client(), "api-key", server.URL)
	checkout, found, err := client.FindCheckout(context.Background(), "payment-42", "secret-42")
	if err != nil {
		t.Fatalf("find checkout: %v", err)
	}
	if !found || checkout.ID != "bill_match" {
		t.Fatalf("found = %v, checkout = %#v", found, checkout)
	}
	if detailRequests != 3 {
		t.Fatalf("detail requests = %d", detailRequests)
	}
}

func TestAbacatePayGetCheckoutStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/checkouts/get" || r.URL.Query().Get("id") != "bill_paid" {
			t.Errorf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		fmt.Fprint(w, `{"data":{"id":"bill_paid","externalId":"payment-99","url":"https://pay.example/bill_paid","amount":12000,"paidAmount":12000,"status":"PAID","receiptUrl":"https://pay.example/bill_paid/receipt","metadata":{"checkout_key":"secret-99"},"updatedAt":"2026-08-18T14:15:30-03:00"},"success":true,"error":null}`)
	}))
	defer server.Close()

	client := NewAbacatePayClient(server.Client(), "api-key", server.URL)
	checkout, err := client.GetCheckout(context.Background(), "bill_paid")
	if err != nil {
		t.Fatalf("get checkout: %v", err)
	}
	if checkout.Status != "PAID" || checkout.Amount != 12000 || checkout.PaidAmount != 12000 || checkout.ReceiptURL != "https://pay.example/bill_paid/receipt" || checkout.Metadata["checkout_key"] != "secret-99" {
		t.Fatalf("unexpected checkout: %#v", checkout)
	}
	wantUpdatedAt, _ := time.Parse(time.RFC3339, "2026-08-18T14:15:30-03:00")
	if !checkout.UpdatedAt.Equal(wantUpdatedAt) {
		t.Fatalf("updated at = %v", checkout.UpdatedAt)
	}
}

func TestAbacatePayEnvelopeValidation(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{name: "unsuccessful", response: `{"data":null,"success":false,"error":"invalid product"}`, want: "invalid product"},
		{name: "missing product ID", response: `{"data":{"externalId":"plan"},"success":true,"error":null}`, want: "missing an ID"},
		{name: "malformed", response: `{"data":`, want: "decode AbacatePay"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, test.response)
			}))
			defer server.Close()

			client := NewAbacatePayClient(server.Client(), "api-key", server.URL)
			_, err := client.CreateProduct(context.Background(), "plan", "Plan", 1000, "Access")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestAbacatePayRejectsCheckoutWithoutIDOrURL(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{name: "ID", response: `{"data":{"url":"https://pay.example/checkout"},"success":true,"error":null}`, want: "missing an ID"},
		{name: "URL", response: `{"data":{"id":"bill_123"},"success":true,"error":null}`, want: "missing a URL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, test.response)
			}))
			defer server.Close()

			client := NewAbacatePayClient(server.Client(), "api-key", server.URL)
			_, err := client.CreateCheckout(context.Background(), "prod_123", "payment-42", "https://app.example/return", "https://app.example/complete", "secret")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestAbacatePayAPIErrorIsUsefulAndBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, strings.Repeat("invalid API key ", 1000))
	}))
	defer server.Close()

	client := NewAbacatePayClient(server.Client(), "api-key", server.URL)
	_, err := client.GetCheckout(context.Background(), "bill_123")
	if err == nil {
		t.Fatal("expected API error")
	}
	if !strings.Contains(err.Error(), "401 Unauthorized") || !strings.Contains(err.Error(), "invalid API key") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(err.Error()) > abacatePayMaxErrorBodySize+200 {
		t.Fatalf("error is not bounded: %d bytes", len(err.Error()))
	}
}

func TestVerifyAbacatePaySignature(t *testing.T) {
	rawBody := []byte(`{"event":"checkout.completed","data":{"id":"bill_123"}}`)
	mac := hmac.New(sha256.New, []byte(abacatePayWebhookPublicKey))
	mac.Write(rawBody)
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if !VerifyAbacatePaySignature(rawBody, signature) {
		t.Fatal("expected valid signature")
	}
	if VerifyAbacatePaySignature([]byte(`{"event":"checkout.refunded"}`), signature) {
		t.Fatal("signature must not validate for a changed body")
	}
	changed := append([]byte(nil), mac.Sum(nil)...)
	changed[0] ^= 0xff
	if VerifyAbacatePaySignature(rawBody, base64.StdEncoding.EncodeToString(changed)) {
		t.Fatal("changed signature must not validate")
	}
	if VerifyAbacatePaySignature(rawBody, "not-base64") {
		t.Fatal("invalid base64 must not validate")
	}
	if VerifyAbacatePaySignature(rawBody, base64.StdEncoding.EncodeToString([]byte("short"))) {
		t.Fatal("wrong signature length must not validate")
	}
}

func checkoutEnvelope(id, externalID, checkoutKey string) string {
	return fmt.Sprintf(
		`{"data":{"id":%q,"externalId":%q,"url":%q,"amount":8900,"paidAmount":0,"status":"PENDING","receiptUrl":null,"metadata":{"checkout_key":%q},"updatedAt":"2026-08-18T13:30:00Z"},"success":true,"error":null}`,
		id,
		externalID,
		"https://pay.example/"+id,
		checkoutKey,
	)
}

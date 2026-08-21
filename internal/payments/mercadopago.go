package payments

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	mercadoPagoBaseURL  = "https://api.mercadopago.com"
	maxRequestBodySize  = 1 << 20
	maxResponseBodySize = 1 << 20
	maxErrorBodySize    = 4 << 10
)

type MercadoPagoClient struct {
	httpClient    *http.Client
	accessToken   string
	webhookSecret string
	baseURL       string
}

type BackURLs struct {
	Success string `json:"success,omitempty"`
	Failure string `json:"failure,omitempty"`
	Pending string `json:"pending,omitempty"`
}

type CreatePreferenceRequest struct {
	Title             string
	AmountCents       int64
	PayerEmail        string
	ExternalReference string
	NotificationURL   string
	BackURLs          BackURLs
	ExpirationDate    time.Time
	CheckoutKey       string
}

type CreatePreferenceResponse struct {
	ID        string `json:"id"`
	InitPoint string `json:"init_point"`
}

type Payment struct {
	ID                string
	Status            string
	CurrencyID        string
	CheckoutKey       string
	ExternalReference string
	DateApproved      *time.Time
	TransactionAmount json.Number
}

func NewMercadoPagoClient(httpClient *http.Client, accessToken, webhookSecret, baseURL string) *MercadoPagoClient {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = mercadoPagoBaseURL
	}
	return &MercadoPagoClient{
		httpClient:    httpClient,
		accessToken:   accessToken,
		webhookSecret: webhookSecret,
		baseURL:       strings.TrimRight(baseURL, "/"),
	}
}

func (c *MercadoPagoClient) CreatePreference(ctx context.Context, request CreatePreferenceRequest) (CreatePreferenceResponse, error) {
	if request.AmountCents <= 0 {
		return CreatePreferenceResponse{}, errors.New("preference amount must be greater than zero")
	}

	payload := struct {
		Items []struct {
			Title      string      `json:"title"`
			Quantity   int         `json:"quantity"`
			UnitPrice  json.Number `json:"unit_price"`
			CurrencyID string      `json:"currency_id"`
		} `json:"items"`
		Payer struct {
			Email string `json:"email"`
		} `json:"payer"`
		ExternalReference string    `json:"external_reference"`
		NotificationURL   string    `json:"notification_url,omitempty"`
		BackURLs          *BackURLs `json:"back_urls,omitempty"`
		Expires           bool      `json:"expires,omitempty"`
		ExpirationDateTo  string    `json:"expiration_date_to,omitempty"`
		AutoReturn        string    `json:"auto_return,omitempty"`
		Metadata          struct {
			CheckoutKey string `json:"checkout_key,omitempty"`
		} `json:"metadata,omitempty"`
	}{
		ExternalReference: request.ExternalReference,
		NotificationURL:   request.NotificationURL,
	}
	payload.Items = append(payload.Items, struct {
		Title      string      `json:"title"`
		Quantity   int         `json:"quantity"`
		UnitPrice  json.Number `json:"unit_price"`
		CurrencyID string      `json:"currency_id"`
	}{
		Title:      request.Title,
		Quantity:   1,
		UnitPrice:  json.Number(formatCents(request.AmountCents)),
		CurrencyID: "BRL",
	})
	payload.Payer.Email = request.PayerEmail
	payload.Metadata.CheckoutKey = request.CheckoutKey
	if request.BackURLs != (BackURLs{}) {
		payload.BackURLs = &request.BackURLs
	}
	if request.BackURLs.Success != "" {
		payload.AutoReturn = "approved"
	}
	if !request.ExpirationDate.IsZero() {
		payload.Expires = true
		payload.ExpirationDateTo = request.ExpirationDate.Format(time.RFC3339)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return CreatePreferenceResponse{}, fmt.Errorf("encode Mercado Pago preference: %w", err)
	}
	if len(body) > maxRequestBodySize {
		return CreatePreferenceResponse{}, errors.New("Mercado Pago request body is too large")
	}

	responseBody, err := c.do(ctx, http.MethodPost, "/checkout/preferences", bytes.NewReader(body), true)
	if err != nil {
		return CreatePreferenceResponse{}, err
	}
	var response CreatePreferenceResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return CreatePreferenceResponse{}, fmt.Errorf("decode Mercado Pago preference response: %w", err)
	}
	return response, nil
}

func (c *MercadoPagoClient) FindPreference(ctx context.Context, externalReference, checkoutKey string) (CreatePreferenceResponse, bool, error) {
	query := url.Values{}
	query.Set("external_reference", externalReference)
	body, err := c.do(ctx, http.MethodGet, "/checkout/preferences/search?"+query.Encode(), nil, false)
	if err != nil {
		return CreatePreferenceResponse{}, false, err
	}
	var response struct {
		Elements []struct {
			ID                string `json:"id"`
			ExternalReference string `json:"external_reference"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return CreatePreferenceResponse{}, false, fmt.Errorf("decode Mercado Pago preference search: %w", err)
	}
	for index, summary := range response.Elements {
		if index >= 20 {
			break
		}
		if summary.ExternalReference != externalReference || summary.ID == "" {
			continue
		}
		preference, matches, err := c.getPreference(ctx, summary.ID, externalReference, checkoutKey)
		if err != nil {
			return CreatePreferenceResponse{}, false, err
		}
		if matches {
			return preference, true, nil
		}
	}
	return CreatePreferenceResponse{}, false, nil
}

func (c *MercadoPagoClient) getPreference(ctx context.Context, preferenceID, externalReference, checkoutKey string) (CreatePreferenceResponse, bool, error) {
	body, err := c.do(ctx, http.MethodGet, "/checkout/preferences/"+url.PathEscape(preferenceID), nil, false)
	if err != nil {
		return CreatePreferenceResponse{}, false, err
	}
	var preference struct {
		ID                string `json:"id"`
		InitPoint         string `json:"init_point"`
		ExternalReference string `json:"external_reference"`
		Metadata          struct {
			CheckoutKey string `json:"checkout_key"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &preference); err != nil {
		return CreatePreferenceResponse{}, false, fmt.Errorf("decode Mercado Pago preference: %w", err)
	}
	if preference.ID != preferenceID || preference.ExternalReference != externalReference ||
		preference.Metadata.CheckoutKey != checkoutKey || preference.InitPoint == "" {
		return CreatePreferenceResponse{}, false, nil
	}
	return CreatePreferenceResponse{ID: preference.ID, InitPoint: preference.InitPoint}, true, nil
}

func (c *MercadoPagoClient) GetPayment(ctx context.Context, providerPaymentID string) (Payment, error) {
	if strings.TrimSpace(providerPaymentID) == "" {
		return Payment{}, errors.New("provider payment ID is required")
	}

	path := "/v1/payments/" + url.PathEscape(providerPaymentID)
	body, err := c.do(ctx, http.MethodGet, path, nil, false)
	if err != nil {
		return Payment{}, err
	}
	var response struct {
		ID         json.RawMessage `json:"id"`
		Status     string          `json:"status"`
		CurrencyID string          `json:"currency_id"`
		Metadata   struct {
			CheckoutKey string `json:"checkout_key"`
		} `json:"metadata"`
		ExternalReference string      `json:"external_reference"`
		DateApproved      *time.Time  `json:"date_approved"`
		TransactionAmount json.Number `json:"transaction_amount"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return Payment{}, fmt.Errorf("decode Mercado Pago payment response: %w", err)
	}
	id, err := parseProviderID(response.ID)
	if err != nil {
		return Payment{}, fmt.Errorf("decode Mercado Pago payment ID: %w", err)
	}
	return Payment{
		ID:                id,
		Status:            response.Status,
		CurrencyID:        response.CurrencyID,
		CheckoutKey:       response.Metadata.CheckoutKey,
		ExternalReference: response.ExternalReference,
		DateApproved:      response.DateApproved,
		TransactionAmount: response.TransactionAmount,
	}, nil
}

func (c *MercadoPagoClient) VerifyWebhookSignature(dataID, requestID, signatureHeader string) bool {
	if c.webhookSecret == "" {
		return false
	}

	var timestamp, signature string
	for _, part := range strings.Split(signatureHeader, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "ts":
			timestamp = strings.TrimSpace(value)
		case "v1":
			signature = strings.TrimSpace(value)
		}
	}
	if timestamp == "" || signature == "" {
		return false
	}

	provided, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	manifest := fmt.Sprintf("id:%s;request-id:%s;ts:%s;", strings.ToLower(dataID), requestID, timestamp)
	mac := hmac.New(sha256.New, []byte(c.webhookSecret))
	_, _ = io.WriteString(mac, manifest)
	return hmac.Equal(mac.Sum(nil), provided)
}

func (c *MercadoPagoClient) do(ctx context.Context, method, path string, body io.Reader, hasBody bool) ([]byte, error) {
	if c.httpClient == nil {
		return nil, errors.New("Mercado Pago HTTP client is nil")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("create Mercado Pago request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Accept", "application/json")
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send Mercado Pago request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBodySize+1))
		if readErr != nil {
			return nil, fmt.Errorf("Mercado Pago API returned %s (read error: %v)", response.Status, readErr)
		}
		truncated := len(body) > maxErrorBodySize
		if truncated {
			body = body[:maxErrorBodySize]
		}
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		if truncated {
			message += "..."
		}
		return nil, &HTTPError{
			Provider: "Mercado Pago", StatusCode: response.StatusCode, Status: response.Status, Message: message,
		}
	}

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("read Mercado Pago response: %w", err)
	}
	if len(responseBody) > maxResponseBodySize {
		return nil, errors.New("Mercado Pago response body is too large")
	}
	return responseBody, nil
}

func formatCents(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

func parseProviderID(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", errors.New("missing ID")
	}
	if raw[0] == '"' {
		var id string
		if err := json.Unmarshal(raw, &id); err != nil {
			return "", err
		}
		if id == "" {
			return "", errors.New("missing ID")
		}
		return id, nil
	}
	var id json.Number
	if err := json.Unmarshal(raw, &id); err != nil {
		return "", err
	}
	if id == "" {
		return "", errors.New("missing ID")
	}
	return id.String(), nil
}

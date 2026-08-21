package payments

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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
	abacatePayBaseURL             = "https://api.abacatepay.com/v2"
	abacatePayMaxRequestBodySize  = 1 << 20
	abacatePayMaxResponseBodySize = 1 << 20
	abacatePayMaxErrorBodySize    = 4 << 10
	abacatePayWebhookPublicKey    = "t9dXRhHHo3yDEj5pVDYz0frf7q6bMKyMRmxxCPIPp3RCplBfXRxqlC6ZpiWmOqj4L63qEaeUOtrCI8P0VMUgo6iIga2ri9ogaHFs0WIIywSMg0q7RmBfybe1E5XJcfC4IW3alNqym0tXoAKkzvfEjZxV6bE0oG2zJrNNYmUCKZyV0KZ3JS8Votf9EAWWYdiDkMkpbMdPggfh1EqHlVkMiTady6jOR3hyzGEHrIz2Ret0xHKMbiqkr9HS1JhNHDX9"
)

type AbacatePayClient struct {
	httpClient *http.Client
	apiKey     string
	baseURL    string
}

type AbacateProduct struct {
	ID          string `json:"id"`
	ExternalID  string `json:"externalId"`
	Name        string `json:"name"`
	Price       int64  `json:"price"`
	Currency    string `json:"currency"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Cycle       string `json:"cycle"`
}

type AbacateCheckout struct {
	ID         string            `json:"id"`
	ExternalID string            `json:"externalId"`
	URL        string            `json:"url"`
	Amount     int64             `json:"amount"`
	PaidAmount int64             `json:"paidAmount"`
	Status     string            `json:"status"`
	ReceiptURL string            `json:"receiptUrl"`
	Metadata   map[string]string `json:"metadata"`
	UpdatedAt  time.Time         `json:"updatedAt"`
}

type abacatePayEnvelope[T any] struct {
	Data    T               `json:"data"`
	Success bool            `json:"success"`
	Error   json.RawMessage `json:"error"`
}

func NewAbacatePayClient(httpClient *http.Client, apiKey, baseURL string) *AbacatePayClient {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = abacatePayBaseURL
	}
	return &AbacatePayClient{
		httpClient: httpClient,
		apiKey:     apiKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
	}
}

func (c *AbacatePayClient) CreateProduct(ctx context.Context, externalID, name string, priceCents int64, description string) (AbacateProduct, error) {
	if strings.TrimSpace(externalID) == "" {
		return AbacateProduct{}, errors.New("AbacatePay product external ID is required")
	}
	if strings.TrimSpace(name) == "" {
		return AbacateProduct{}, errors.New("AbacatePay product name is required")
	}
	if priceCents <= 0 {
		return AbacateProduct{}, errors.New("AbacatePay product price must be greater than zero")
	}

	payload := struct {
		ExternalID  string `json:"externalId"`
		Name        string `json:"name"`
		Price       int64  `json:"price"`
		Currency    string `json:"currency"`
		Description string `json:"description"`
	}{
		ExternalID:  externalID,
		Name:        name,
		Price:       priceCents,
		Currency:    "BRL",
		Description: description,
	}
	responseBody, err := c.postJSON(ctx, "/products/create", payload)
	if err != nil {
		return AbacateProduct{}, err
	}
	product, err := decodeAbacatePayEnvelope[AbacateProduct](responseBody, "product creation")
	if err != nil {
		return AbacateProduct{}, err
	}
	if strings.TrimSpace(product.ID) == "" {
		return AbacateProduct{}, errors.New("AbacatePay product response is missing an ID")
	}
	return product, nil
}

func (c *AbacatePayClient) GetProductByExternalID(ctx context.Context, externalID string) (AbacateProduct, bool, error) {
	if strings.TrimSpace(externalID) == "" {
		return AbacateProduct{}, false, errors.New("AbacatePay product external ID is required")
	}

	query := url.Values{}
	query.Set("externalId", externalID)
	responseBody, statusCode, err := c.do(ctx, http.MethodGet, "/products/get?"+query.Encode(), nil, false)
	if statusCode == http.StatusNotFound {
		return AbacateProduct{}, false, nil
	}
	if err != nil {
		return AbacateProduct{}, false, err
	}
	product, err := decodeAbacatePayEnvelope[AbacateProduct](responseBody, "product lookup")
	if err != nil {
		return AbacateProduct{}, false, err
	}
	if strings.TrimSpace(product.ID) == "" {
		return AbacateProduct{}, false, errors.New("AbacatePay product response is missing an ID")
	}
	return product, true, nil
}

func (c *AbacatePayClient) CreateCheckout(ctx context.Context, productID, externalID, returnURL, completionURL, checkoutKey string) (AbacateCheckout, error) {
	if strings.TrimSpace(productID) == "" {
		return AbacateCheckout{}, errors.New("AbacatePay product ID is required")
	}
	if strings.TrimSpace(externalID) == "" {
		return AbacateCheckout{}, errors.New("AbacatePay checkout external ID is required")
	}
	if strings.TrimSpace(returnURL) == "" || strings.TrimSpace(completionURL) == "" {
		return AbacateCheckout{}, errors.New("AbacatePay checkout return and completion URLs are required")
	}
	if strings.TrimSpace(checkoutKey) == "" {
		return AbacateCheckout{}, errors.New("AbacatePay checkout key is required")
	}

	payload := struct {
		Items []struct {
			ID       string `json:"id"`
			Quantity int    `json:"quantity"`
		} `json:"items"`
		Methods       []string          `json:"methods"`
		ExternalID    string            `json:"externalId"`
		ReturnURL     string            `json:"returnUrl"`
		CompletionURL string            `json:"completionUrl"`
		Metadata      map[string]string `json:"metadata"`
	}{
		Methods:       []string{"PIX", "CARD"},
		ExternalID:    externalID,
		ReturnURL:     returnURL,
		CompletionURL: completionURL,
		Metadata:      map[string]string{"checkout_key": checkoutKey},
	}
	payload.Items = append(payload.Items, struct {
		ID       string `json:"id"`
		Quantity int    `json:"quantity"`
	}{
		ID:       productID,
		Quantity: 1,
	})

	responseBody, err := c.postJSON(ctx, "/checkouts/create", payload)
	if err != nil {
		return AbacateCheckout{}, err
	}
	checkout, err := decodeAbacatePayEnvelope[AbacateCheckout](responseBody, "checkout creation")
	if err != nil {
		return AbacateCheckout{}, err
	}
	if err := validateAbacateCheckout(checkout); err != nil {
		return AbacateCheckout{}, err
	}
	return checkout, nil
}

func (c *AbacatePayClient) FindCheckout(ctx context.Context, externalID, checkoutKey string) (AbacateCheckout, bool, error) {
	if strings.TrimSpace(externalID) == "" {
		return AbacateCheckout{}, false, errors.New("AbacatePay checkout external ID is required")
	}
	if strings.TrimSpace(checkoutKey) == "" {
		return AbacateCheckout{}, false, errors.New("AbacatePay checkout key is required")
	}

	query := url.Values{}
	query.Set("externalId", externalID)
	query.Set("limit", "100")
	responseBody, _, err := c.do(ctx, http.MethodGet, "/checkouts/list?"+query.Encode(), nil, false)
	if err != nil {
		return AbacateCheckout{}, false, err
	}
	summaries, err := decodeAbacatePayEnvelope[[]struct {
		ID string `json:"id"`
	}](responseBody, "checkout list")
	if err != nil {
		return AbacateCheckout{}, false, err
	}
	for _, summary := range summaries {
		if strings.TrimSpace(summary.ID) == "" {
			continue
		}
		checkout, err := c.GetCheckout(ctx, summary.ID)
		if err != nil {
			return AbacateCheckout{}, false, err
		}
		if checkout.ExternalID == externalID && checkout.Metadata["checkout_key"] == checkoutKey {
			return checkout, true, nil
		}
	}
	return AbacateCheckout{}, false, nil
}

func (c *AbacatePayClient) GetCheckout(ctx context.Context, checkoutID string) (AbacateCheckout, error) {
	if strings.TrimSpace(checkoutID) == "" {
		return AbacateCheckout{}, errors.New("AbacatePay checkout ID is required")
	}

	query := url.Values{}
	query.Set("id", checkoutID)
	responseBody, _, err := c.do(ctx, http.MethodGet, "/checkouts/get?"+query.Encode(), nil, false)
	if err != nil {
		return AbacateCheckout{}, err
	}
	checkout, err := decodeAbacatePayEnvelope[AbacateCheckout](responseBody, "checkout lookup")
	if err != nil {
		return AbacateCheckout{}, err
	}
	if err := validateAbacateCheckout(checkout); err != nil {
		return AbacateCheckout{}, err
	}
	if checkout.ID != checkoutID {
		return AbacateCheckout{}, errors.New("AbacatePay checkout response ID does not match the requested ID")
	}
	return checkout, nil
}

func VerifyAbacatePaySignature(rawBody []byte, signature string) bool {
	provided, err := base64.StdEncoding.DecodeString(signature)
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(abacatePayWebhookPublicKey))
	_, _ = mac.Write(rawBody)
	return hmac.Equal(mac.Sum(nil), provided)
}

func validateAbacateCheckout(checkout AbacateCheckout) error {
	if strings.TrimSpace(checkout.ID) == "" {
		return errors.New("AbacatePay checkout response is missing an ID")
	}
	if strings.TrimSpace(checkout.URL) == "" {
		return errors.New("AbacatePay checkout response is missing a URL")
	}
	return nil
}

func decodeAbacatePayEnvelope[T any](body []byte, operation string) (T, error) {
	var envelope abacatePayEnvelope[T]
	if err := json.Unmarshal(body, &envelope); err != nil {
		return envelope.Data, fmt.Errorf("decode AbacatePay %s response: %w", operation, err)
	}
	if !envelope.Success {
		message := abacatePayErrorMessage(envelope.Error)
		if message == "" {
			message = "request was not successful"
		}
		return envelope.Data, fmt.Errorf("AbacatePay %s failed: %s", operation, message)
	}
	return envelope.Data, nil
}

func abacatePayErrorMessage(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ""
	}
	var message string
	if err := json.Unmarshal(raw, &message); err != nil {
		message = strings.TrimSpace(string(raw))
	}
	return boundAbacatePayMessage(message)
}

func boundAbacatePayMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= abacatePayMaxErrorBodySize {
		return message
	}
	return message[:abacatePayMaxErrorBodySize] + "..."
}

func (c *AbacatePayClient) postJSON(ctx context.Context, path string, payload interface{}) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode AbacatePay request: %w", err)
	}
	if len(body) > abacatePayMaxRequestBodySize {
		return nil, errors.New("AbacatePay request body is too large")
	}
	responseBody, _, err := c.do(ctx, http.MethodPost, path, bytes.NewReader(body), true)
	return responseBody, err
}

func (c *AbacatePayClient) do(ctx context.Context, method, path string, body io.Reader, hasBody bool) ([]byte, int, error) {
	if c.httpClient == nil {
		return nil, 0, errors.New("AbacatePay HTTP client is nil")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, 0, fmt.Errorf("create AbacatePay request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("send AbacatePay request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, abacatePayMaxErrorBodySize+1))
		if readErr != nil {
			return nil, response.StatusCode, fmt.Errorf("AbacatePay API returned %s (read error: %v)", response.Status, readErr)
		}
		message := boundAbacatePayMessage(string(responseBody))
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return nil, response.StatusCode, &HTTPError{
			Provider: "AbacatePay", StatusCode: response.StatusCode, Status: response.Status, Message: message,
		}
	}

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, abacatePayMaxResponseBodySize+1))
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("read AbacatePay response: %w", err)
	}
	if len(responseBody) > abacatePayMaxResponseBodySize {
		return nil, response.StatusCode, errors.New("AbacatePay response body is too large")
	}
	return responseBody, response.StatusCode, nil
}

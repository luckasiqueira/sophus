package payments

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	asaasBaseURL             = "https://api.asaas.com/v3"
	AsaasSandboxBaseURL      = "https://api-sandbox.asaas.com/v3"
	asaasMaxRequestBodySize  = 1 << 20
	asaasMaxResponseBodySize = 1 << 20
	asaasMaxErrorBodySize    = 4 << 10
	asaasMaxLinkNameLength   = 100
	asaasMaxLinkPages        = 5
	asaasUserAgent           = "zubly-billing"
)

// AsaasClient issues one Asaas payment link per invoice. The hosted link page
// collects the payer's CPF/CNPJ, which the platform does not store, and each
// payment made through it is confirmed against the authenticated payments API.
type AsaasClient struct {
	httpClient *http.Client
	apiKey     string
	baseURL    string
}

type AsaasPaymentLink struct {
	ID                string      `json:"id"`
	Name              string      `json:"name"`
	URL               string      `json:"url"`
	Value             json.Number `json:"value"`
	Active            bool        `json:"active"`
	Deleted           bool        `json:"deleted"`
	BillingType       string      `json:"billingType"`
	ChargeType        string      `json:"chargeType"`
	EndDate           string      `json:"endDate"`
	ExternalReference string      `json:"externalReference"`
}

type AsaasPayment struct {
	ID                    string      `json:"id"`
	Status                string      `json:"status"`
	Value                 json.Number `json:"value"`
	BillingType           string      `json:"billingType"`
	PaymentLink           string      `json:"paymentLink"`
	ExternalReference     string      `json:"externalReference"`
	InvoiceURL            string      `json:"invoiceUrl"`
	TransactionReceiptURL string      `json:"transactionReceiptUrl"`
	Deleted               bool        `json:"deleted"`
}

type CreateAsaasPaymentLinkRequest struct {
	Name              string
	Description       string
	AmountCents       int64
	EndDate           time.Time
	ExternalReference string
	// BillingType fixes the payment method shown on the link page. Empty or
	// UNDEFINED lets the payer choose between PIX, boleto and card.
	BillingType string
}

const (
	AsaasBillingTypeUndefined  = "UNDEFINED"
	AsaasBillingTypePix        = "PIX"
	AsaasBillingTypeBoleto     = "BOLETO"
	AsaasBillingTypeCreditCard = "CREDIT_CARD"
)

// NormalizeAsaasBillingType returns the link billing type for a requested
// payment method, with an empty value meaning UNDEFINED.
func NormalizeAsaasBillingType(value string) (string, bool) {
	switch value = strings.ToUpper(strings.TrimSpace(value)); value {
	case "", AsaasBillingTypeUndefined:
		return AsaasBillingTypeUndefined, true
	case AsaasBillingTypePix, AsaasBillingTypeBoleto, AsaasBillingTypeCreditCard:
		return value, true
	default:
		return "", false
	}
}

func NewAsaasClient(httpClient *http.Client, apiKey, baseURL string) *AsaasClient {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = asaasBaseURL
	}
	return &AsaasClient{
		httpClient: httpClient,
		apiKey:     apiKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
	}
}

// AsaasLinkName gives every invoice link a searchable prefix, because the
// payment links API cannot be filtered by external reference.
func AsaasLinkName(paymentID int, planName string) string {
	name := asaasLinkNamePrefix(paymentID)
	if planName = strings.TrimSpace(planName); planName != "" {
		name += " - Plano " + planName
	}
	if len(name) > asaasMaxLinkNameLength {
		name = strings.ToValidUTF8(name[:asaasMaxLinkNameLength], "")
	}
	return name
}

func asaasLinkNamePrefix(paymentID int) string {
	return "Fatura #" + strconv.Itoa(paymentID)
}

// AsaasLinkReference binds a link to the invoice and to the checkout key that
// claimed it, so a link left by an interrupted request can be recovered safely.
func AsaasLinkReference(paymentID int, checkoutKey string) string {
	return strconv.Itoa(paymentID) + ":" + checkoutKey
}

func (c *AsaasClient) CreatePaymentLink(ctx context.Context, request CreateAsaasPaymentLinkRequest) (AsaasPaymentLink, error) {
	if request.AmountCents <= 0 {
		return AsaasPaymentLink{}, errors.New("Asaas payment link amount must be greater than zero")
	}
	if strings.TrimSpace(request.Name) == "" || strings.TrimSpace(request.ExternalReference) == "" {
		return AsaasPaymentLink{}, errors.New("Asaas payment link name and external reference are required")
	}
	if request.EndDate.IsZero() {
		return AsaasPaymentLink{}, errors.New("Asaas payment link end date is required")
	}
	billingType, ok := NormalizeAsaasBillingType(request.BillingType)
	if !ok {
		return AsaasPaymentLink{}, errors.New("invalid Asaas billing type")
	}
	payload := struct {
		Name                string      `json:"name"`
		Description         string      `json:"description,omitempty"`
		BillingType         string      `json:"billingType"`
		ChargeType          string      `json:"chargeType"`
		Value               json.Number `json:"value"`
		EndDate             string      `json:"endDate"`
		DueDateLimitDays    int         `json:"dueDateLimitDays"`
		ExternalReference   string      `json:"externalReference"`
		NotificationEnabled bool        `json:"notificationEnabled"`
	}{
		Name:                request.Name,
		Description:         request.Description,
		BillingType:         billingType,
		ChargeType:          "DETACHED",
		Value:               json.Number(formatCents(request.AmountCents)),
		EndDate:             asaasDate(request.EndDate),
		DueDateLimitDays:    1,
		ExternalReference:   request.ExternalReference,
		NotificationEnabled: false,
	}
	body, err := c.doJSON(ctx, http.MethodPost, "/paymentLinks", payload)
	if err != nil {
		return AsaasPaymentLink{}, err
	}
	link, err := decodeAsaasPaymentLink(body)
	if err != nil {
		return AsaasPaymentLink{}, err
	}
	if link.ExternalReference != request.ExternalReference {
		return AsaasPaymentLink{}, errors.New("Asaas payment link reference does not match the request")
	}
	return link, nil
}

func (c *AsaasClient) GetPaymentLink(ctx context.Context, linkID string) (AsaasPaymentLink, error) {
	if strings.TrimSpace(linkID) == "" {
		return AsaasPaymentLink{}, errors.New("Asaas payment link ID is required")
	}
	body, err := c.do(ctx, http.MethodGet, "/paymentLinks/"+url.PathEscape(linkID), nil)
	if err != nil {
		return AsaasPaymentLink{}, err
	}
	link, err := decodeAsaasPaymentLink(body)
	if err != nil {
		return AsaasPaymentLink{}, err
	}
	if link.ID != linkID {
		return AsaasPaymentLink{}, errors.New("Asaas payment link response ID does not match the requested ID")
	}
	return link, nil
}

// FindPaymentLink recovers the active link created for an invoice checkout.
func (c *AsaasClient) FindPaymentLink(ctx context.Context, paymentID int, checkoutKey string) (AsaasPaymentLink, bool, error) {
	links, err := c.FindPaymentLinks(ctx, paymentID, checkoutKey)
	if err != nil || len(links) == 0 {
		return AsaasPaymentLink{}, false, err
	}
	return links[0], true, nil
}

// FindPaymentLinks lists every active link created for an invoice checkout,
// one per payment method the payer requested.
func (c *AsaasClient) FindPaymentLinks(ctx context.Context, paymentID int, checkoutKey string) ([]AsaasPaymentLink, error) {
	if paymentID <= 0 || strings.TrimSpace(checkoutKey) == "" {
		return nil, errors.New("Asaas payment link identity is required")
	}
	var found []AsaasPaymentLink
	reference := AsaasLinkReference(paymentID, checkoutKey)
	prefix := asaasLinkNamePrefix(paymentID)
	for page := 0; page < asaasMaxLinkPages; page++ {
		query := url.Values{}
		query.Set("name", prefix)
		query.Set("limit", "100")
		query.Set("offset", strconv.Itoa(page*100))
		body, err := c.do(ctx, http.MethodGet, "/paymentLinks?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var response struct {
			HasMore bool               `json:"hasMore"`
			Data    []AsaasPaymentLink `json:"data"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, fmt.Errorf("decode Asaas payment link list: %w", err)
		}
		for _, link := range response.Data {
			if link.Deleted || strings.TrimSpace(link.ID) == "" || strings.TrimSpace(link.URL) == "" {
				continue
			}
			if subtle.ConstantTimeCompare([]byte(link.ExternalReference), []byte(reference)) == 1 {
				found = append(found, link)
			}
		}
		if !response.HasMore {
			break
		}
	}
	return found, nil
}

// UpdatePaymentLink changes the price and end date of an issued invoice link
// while keeping its URL, then confirms the provider stored the new values.
func (c *AsaasClient) UpdatePaymentLink(ctx context.Context, linkID, externalReference string, amountCents int64, endDate time.Time) error {
	if amountCents <= 0 || endDate.IsZero() {
		return errors.New("invalid Asaas payment link price or end date")
	}
	if err := c.verifyPaymentLinkIdentity(ctx, linkID, externalReference); err != nil {
		return err
	}
	payload := map[string]any{
		"value":   json.Number(formatCents(amountCents)),
		"endDate": asaasDate(endDate),
	}
	if _, err := c.doJSON(ctx, http.MethodPut, "/paymentLinks/"+url.PathEscape(linkID), payload); err != nil {
		return err
	}
	link, err := c.GetPaymentLink(ctx, linkID)
	if err != nil {
		return err
	}
	cents, err := AsaasAmountCents(link.Value)
	if err != nil || cents != amountCents || link.EndDate != asaasDate(endDate) || link.ExternalReference != externalReference {
		return errors.New("Asaas payment link does not reflect the requested update")
	}
	return nil
}

// DeletePaymentLink stops a link from accepting new payments. Payments already
// made through it are kept by Asaas. A link that is already gone counts as
// deleted, so retries after a partial failure succeed.
func (c *AsaasClient) DeletePaymentLink(ctx context.Context, linkID, externalReference string) error {
	link, err := c.GetPaymentLink(ctx, linkID)
	if isAsaasNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(link.ExternalReference), []byte(externalReference)) != 1 {
		return errors.New("Asaas payment link identity does not match the invoice")
	}
	if link.Deleted {
		return nil
	}
	body, err := c.do(ctx, http.MethodDelete, "/paymentLinks/"+url.PathEscape(linkID), nil)
	if err != nil {
		return err
	}
	var response struct {
		ID      string `json:"id"`
		Deleted bool   `json:"deleted"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("decode Asaas payment link deletion: %w", err)
	}
	if response.ID != linkID || !response.Deleted {
		return errors.New("Asaas did not confirm the payment link deletion")
	}
	return nil
}

func isAsaasNotFound(err error) bool {
	var httpError *HTTPError
	return errors.As(err, &httpError) && httpError.StatusCode == http.StatusNotFound
}

func (c *AsaasClient) verifyPaymentLinkIdentity(ctx context.Context, linkID, externalReference string) error {
	link, err := c.GetPaymentLink(ctx, linkID)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(link.ExternalReference), []byte(externalReference)) != 1 {
		return errors.New("Asaas payment link identity does not match the invoice")
	}
	return nil
}

func (c *AsaasClient) GetPayment(ctx context.Context, providerPaymentID string) (AsaasPayment, error) {
	if strings.TrimSpace(providerPaymentID) == "" {
		return AsaasPayment{}, errors.New("Asaas payment ID is required")
	}
	body, err := c.do(ctx, http.MethodGet, "/payments/"+url.PathEscape(providerPaymentID), nil)
	if err != nil {
		return AsaasPayment{}, err
	}
	var payment AsaasPayment
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payment); err != nil {
		return AsaasPayment{}, fmt.Errorf("decode Asaas payment response: %w", err)
	}
	if payment.ID != providerPaymentID {
		return AsaasPayment{}, errors.New("Asaas payment response ID does not match the requested ID")
	}
	return payment, nil
}

// AsaasAmountCents converts an Asaas decimal amount without float rounding.
func AsaasAmountCents(value json.Number) (int64, error) {
	raw := strings.TrimSpace(value.String())
	whole, fraction, hasFraction := strings.Cut(raw, ".")
	if whole == "" || !asciiDigits(whole) || (hasFraction && (fraction == "" || !asciiDigits(fraction))) {
		return 0, errors.New("invalid Asaas amount")
	}
	fraction = strings.TrimRight(fraction, "0")
	if len(fraction) > 2 {
		return 0, errors.New("Asaas amount has more than two decimal places")
	}
	fraction += strings.Repeat("0", 2-len(fraction))
	cents, err := strconv.ParseInt(whole+fraction, 10, 64)
	if err != nil {
		return 0, errors.New("invalid Asaas amount")
	}
	return cents, nil
}

func asciiDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return value != ""
}

func asaasDate(value time.Time) string {
	if location, err := time.LoadLocation("America/Sao_Paulo"); err == nil {
		value = value.In(location)
	}
	return value.Format("2006-01-02")
}

func decodeAsaasPaymentLink(body []byte) (AsaasPaymentLink, error) {
	var link AsaasPaymentLink
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&link); err != nil {
		return AsaasPaymentLink{}, fmt.Errorf("decode Asaas payment link response: %w", err)
	}
	if strings.TrimSpace(link.ID) == "" || strings.TrimSpace(link.URL) == "" {
		return AsaasPaymentLink{}, errors.New("Asaas payment link response is missing an ID or URL")
	}
	return link, nil
}

func asaasErrorMessage(body []byte) string {
	var response struct {
		Errors []struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &response) == nil && len(response.Errors) > 0 {
		messages := make([]string, 0, len(response.Errors))
		for _, item := range response.Errors {
			if message := strings.TrimSpace(item.Description); message != "" {
				messages = append(messages, message)
			} else if code := strings.TrimSpace(item.Code); code != "" {
				messages = append(messages, code)
			}
		}
		if len(messages) > 0 {
			return boundAsaasMessage(strings.Join(messages, "; "))
		}
	}
	return boundAsaasMessage(string(body))
}

func boundAsaasMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= asaasMaxErrorBodySize {
		return message
	}
	return message[:asaasMaxErrorBodySize] + "..."
}

func (c *AsaasClient) doJSON(ctx context.Context, method, path string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode Asaas request: %w", err)
	}
	if len(body) > asaasMaxRequestBodySize {
		return nil, errors.New("Asaas request body is too large")
	}
	return c.do(ctx, method, path, bytes.NewReader(body))
}

func (c *AsaasClient) do(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
	if c.httpClient == nil {
		return nil, errors.New("Asaas HTTP client is nil")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("create Asaas request: %w", err)
	}
	req.Header.Set("access_token", c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", asaasUserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send Asaas request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, asaasMaxErrorBodySize+1))
		if readErr != nil {
			return nil, fmt.Errorf("Asaas API returned %s (read error: %v)", response.Status, readErr)
		}
		message := asaasErrorMessage(responseBody)
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return nil, &HTTPError{
			Provider: "Asaas", StatusCode: response.StatusCode, Status: response.Status, Message: message,
		}
	}

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, asaasMaxResponseBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("read Asaas response: %w", err)
	}
	if len(responseBody) > asaasMaxResponseBodySize {
		return nil, errors.New("Asaas response body is too large")
	}
	return responseBody, nil
}

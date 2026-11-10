// Package waha serves Sophus' Evolution GO contract on top of WAHA (GOWS
// engine). Outbound calls arrive through the requests interceptor with the
// same paths and payloads Sophus already sends to Evolution GO; inbound WAHA
// webhooks are translated back into Evolution GO events. Business logic in
// repo/controllers/flowengine therefore runs unchanged with either engine.
package waha

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"sophus/utils/env"
)

const (
	defaultBaseURL   = "http://127.0.0.1:3000"
	maxAPIResponse   = 32 << 20
	apiKeyHeader     = "X-Api-Key"
	defaultCallLimit = 30 * time.Second
)

// Client is a minimal WAHA HTTP client. Status codes are returned to callers so
// the bridge can mirror Evolution GO's error semantics (401, 404, 500...).
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func newClientFromEnv() *Client {
	base := strings.TrimRight(strings.TrimSpace(env.Backend["WAHA_API_URL"]), "/")
	if base == "" {
		base = defaultBaseURL
	}
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	return &Client{
		BaseURL: base,
		APIKey:  strings.TrimSpace(env.Backend["WAHA_API_KEY"]),
		HTTP:    &http.Client{},
	}
}

type apiResponse struct {
	Status int
	Body   []byte
}

func (r apiResponse) OK() bool {
	return r.Status >= http.StatusOK && r.Status < http.StatusMultipleChoices
}

// Do sends one request. A non-nil error means no HTTP response was received.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, payload any, accept string) (apiResponse, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return apiResponse{}, fmt.Errorf("encode WAHA request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	target := c.BaseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultCallLimit)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return apiResponse{}, err
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if accept == "" {
		accept = "application/json"
	}
	request.Header.Set("Accept", accept)
	if c.APIKey != "" {
		request.Header.Set(apiKeyHeader, c.APIKey)
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return apiResponse{}, fmt.Errorf("WAHA %s %s: %w", method, path, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil {
		return apiResponse{}, fmt.Errorf("read WAHA response: %w", err)
	}
	if len(data) > maxAPIResponse {
		return apiResponse{}, errors.New("WAHA response exceeds the size limit")
	}
	return apiResponse{Status: response.StatusCode, Body: data}, nil
}

// errorText extracts WAHA's explanation of a rejected request.
func errorText(body []byte) string {
	var payload struct {
		Message   any `json:"message"`
		Error     any `json:"error"`
		Exception struct {
			Message string `json:"message"`
		} `json:"exception"`
	}
	if json.Unmarshal(body, &payload) == nil {
		for _, value := range []any{payload.Exception.Message, payload.Message, payload.Error} {
			switch typed := value.(type) {
			case string:
				if strings.TrimSpace(typed) != "" {
					return strings.TrimSpace(typed)
				}
			case []any:
				parts := make([]string, 0, len(typed))
				for _, item := range typed {
					parts = append(parts, fmt.Sprint(item))
				}
				if len(parts) > 0 {
					return strings.Join(parts, "; ")
				}
			}
		}
	}
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > 512 {
		text = text[:512]
	}
	return text
}

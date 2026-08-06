package requests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultTimeout = 30 * time.Second

type Request struct {
	URL     string            `json:"url"`
	Payload interface{}       `json:"payload"` //map[string]any
	Headers map[string]string `json:"headers"`
	Method  string            `json:"method"`
	Timeout time.Duration     `json:"-"`
	Response
}

type Response struct {
	StatusCode int    `json:"status_code"`
	Body       []byte `json:"body"`
}

func (r *Request) Do() error {
	body, _ := json.Marshal(r.Payload)
	req, err := http.NewRequest(r.Method, r.URL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	client := http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	r.Response.StatusCode = resp.StatusCode
	r.Response.Body, err = io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if r.Response.StatusCode < http.StatusOK || r.Response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("request failed with status %d: %s", r.Response.StatusCode, string(r.Response.Body))
	}
	return nil
}

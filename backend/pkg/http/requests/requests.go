package requests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type Request struct {
	URL     string            `json:"url"`
	Payload interface{}       `json:"payload"` //map[string]any
	Headers map[string]string `json:"headers"`
	Response
}

type Response struct {
	StatusCode int `json:"status_code"`
}

func (r *Request) Do() error {
	body, _ := json.Marshal(r.Payload)
	req, err := http.NewRequest("POST", r.URL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	fmt.Println(req.Body)
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	r.Response.StatusCode = resp.StatusCode
	fmt.Println("Status:", r.Response.StatusCode)
	return nil
}

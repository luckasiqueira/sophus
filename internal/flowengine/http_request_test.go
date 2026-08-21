package flowengine

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"strings"
	"testing"
)

func TestHTTPRequestJSONFields(t *testing.T) {
	payload, body, contentType, err := httpRequestPayload(map[string]interface{}{
		"bodyMode": "json",
		"bodyFields": []interface{}{
			map[string]interface{}{"key": "name", "value": "{{name}}"},
			map[string]interface{}{"key": "active", "value": "true"},
			map[string]interface{}{"key": "count", "value": "3"},
		},
	}, ExecutionContext{"name": "Ana"}, "POST")
	if err != nil {
		t.Fatalf("build JSON payload: %v", err)
	}
	got := payload.(map[string]interface{})
	if got["name"] != "Ana" || got["active"] != true || got["count"] != float64(3) {
		t.Fatalf("unexpected JSON payload: %#v", got)
	}
	if body != nil || contentType != "application/json" {
		t.Fatalf("unexpected body/content type: %q %q", body, contentType)
	}
}

func TestHTTPRequestFormFields(t *testing.T) {
	_, body, contentType, err := httpRequestPayload(map[string]interface{}{
		"bodyMode": "form",
		"bodyFields": []interface{}{
			map[string]interface{}{"key": "name", "value": "Ana Maria"},
			map[string]interface{}{"key": "tag", "value": "vendas"},
		},
	}, ExecutionContext{}, "POST")
	if err != nil {
		t.Fatalf("build form payload: %v", err)
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		t.Fatalf("parse form payload: %v", err)
	}
	if values.Get("name") != "Ana Maria" || values.Get("tag") != "vendas" {
		t.Fatalf("unexpected form payload: %q", body)
	}
	if contentType != "application/x-www-form-urlencoded" {
		t.Fatalf("content type = %q", contentType)
	}
}

func TestHTTPRequestLegacyBodyUsesRawJSON(t *testing.T) {
	payload, body, contentType, err := httpRequestPayload(map[string]interface{}{
		"body": `{"name":"{{name}}"}`,
	}, ExecutionContext{"name": "Ana"}, "POST")
	if err != nil {
		t.Fatalf("build legacy payload: %v", err)
	}
	if payload != nil || string(body) != `{"name":"Ana"}` || contentType != "application/json" {
		t.Fatalf("unexpected legacy payload: %#v, %q, %q", payload, body, contentType)
	}
}

func TestHTTPRequestRawBodyPreservesContent(t *testing.T) {
	_, body, contentType, err := httpRequestPayload(map[string]interface{}{
		"bodyMode": "raw",
		"body":     "customer={{name}}&active=true",
	}, ExecutionContext{"name": "Ana Maria"}, "POST")
	if err != nil {
		t.Fatalf("build raw payload: %v", err)
	}
	if string(body) != "customer=Ana Maria&active=true" || contentType != "" {
		t.Fatalf("unexpected raw payload: %q, %q", body, contentType)
	}
}

func TestHTTPRequestRawJSONPreservesExactBytes(t *testing.T) {
	_, body, contentType, err := httpRequestPayload(map[string]interface{}{
		"bodyMode": "rawJSON",
		"body":     "  {\n  \"id\": 9007199254740993, \"id\": 2\n}\n",
	}, ExecutionContext{}, "POST")
	if err != nil {
		t.Fatalf("build raw JSON payload: %v", err)
	}
	want := "  {\n  \"id\": 9007199254740993, \"id\": 2\n}\n"
	if string(body) != want || contentType != "application/json" {
		t.Fatalf("raw JSON changed: %q, %q", body, contentType)
	}
}

func TestHTTPRequestMultipartFields(t *testing.T) {
	_, body, contentType, err := httpRequestPayload(map[string]interface{}{
		"bodyMode": "multipart",
		"bodyFields": []interface{}{
			map[string]interface{}{"key": "name", "value": "{{name}}"},
			map[string]interface{}{"key": "metadata", "value": `{"active":true}`, "contentType": "application/json"},
		},
	}, ExecutionContext{"name": "Ana"}, "POST")
	if err != nil {
		t.Fatalf("build multipart payload: %v", err)
	}
	mediaType, parameters, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" || parameters["boundary"] == "" {
		t.Fatalf("invalid multipart content type: %q, %v", contentType, err)
	}
	reader := multipart.NewReader(bytes.NewReader(body), parameters["boundary"])
	parts := map[string]struct {
		value       string
		contentType string
	}{}
	for {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			t.Fatalf("read multipart part: %v", nextErr)
		}
		value, _ := io.ReadAll(part)
		parts[part.FormName()] = struct {
			value       string
			contentType string
		}{value: string(value), contentType: part.Header.Get("Content-Type")}
	}
	if parts["name"].value != "Ana" || parts["metadata"].value != `{"active":true}` || parts["metadata"].contentType != "application/json" {
		t.Fatalf("unexpected multipart parts: %#v", parts)
	}
}

func TestHTTPRequestMultipartLimitsExpandedBody(t *testing.T) {
	large := strings.Repeat("x", maxHTTPRequestBytes/2+1)
	_, _, _, err := httpRequestPayload(map[string]interface{}{
		"bodyMode": "multipart",
		"bodyFields": []interface{}{
			map[string]interface{}{"key": "first", "value": "{{large}}"},
			map[string]interface{}{"key": "second", "value": "{{large}}"},
		},
	}, ExecutionContext{"large": large}, "POST")
	if err == nil || !strings.Contains(err.Error(), "excede o limite") {
		t.Fatalf("expanded multipart error = %v", err)
	}
	_, _, _, err = httpRequestPayload(map[string]interface{}{
		"bodyMode": "multipart",
		"bodyFields": []interface{}{
			map[string]interface{}{"key": "repeated", "value": "{{large}}{{large}}{{large}}"},
		},
	}, ExecutionContext{"large": large}, "POST")
	if err == nil || !strings.Contains(err.Error(), "excede o limite") {
		t.Fatalf("repeated expansion error = %v", err)
	}
}

func TestHTTPRequestDefaultsToNoHeadersOrBody(t *testing.T) {
	headers, err := httpRequestHeaders(map[string]interface{}{}, ExecutionContext{})
	if err != nil {
		t.Fatalf("build default headers: %v", err)
	}
	if len(headers) != 0 {
		t.Fatalf("default headers = %#v, want none", headers)
	}
	payload, body, contentType, err := httpRequestPayload(map[string]interface{}{}, ExecutionContext{}, "POST")
	if err != nil {
		t.Fatalf("build default body: %v", err)
	}
	if payload != nil || body != nil || contentType != "" {
		t.Fatalf("default payload = %#v, body = %q, content type = %q", payload, body, contentType)
	}
}

func TestHTTPRequestHeaderFields(t *testing.T) {
	headers, err := httpRequestHeaders(map[string]interface{}{
		"headerMode": "fields",
		"headerFields": []interface{}{
			map[string]interface{}{"key": "authorization", "value": "Bearer {{token}}"},
			map[string]interface{}{"key": "x-client-id", "value": "zubly"},
		},
	}, ExecutionContext{"token": "secret"})
	if err != nil {
		t.Fatalf("build headers: %v", err)
	}
	if headers["Authorization"] != "Bearer secret" || headers["X-Client-Id"] != "zubly" {
		t.Fatalf("unexpected headers: %#v", headers)
	}
}

func TestHTTPRequestRawJSONHeaders(t *testing.T) {
	headers, err := httpRequestHeaders(map[string]interface{}{
		"headerMode": "rawJSON",
		"headersRaw": `{"Accept":"application/json","X-Account":"{{account}}"}`,
	}, ExecutionContext{"account": "42"})
	if err != nil {
		t.Fatalf("build headers: %v", err)
	}
	if headers["Accept"] != "application/json" || headers["X-Account"] != "42" {
		t.Fatalf("unexpected headers: %#v", headers)
	}
}

func TestHTTPRequestRejectsUnsafeHeaders(t *testing.T) {
	for _, header := range []string{"Host", "X-Forwarded-For", "Proxy-Authorization", "Transfer-Encoding"} {
		_, err := httpRequestHeaders(map[string]interface{}{
			"headerMode":   "fields",
			"headerFields": []interface{}{map[string]interface{}{"key": header, "value": "value"}},
		}, ExecutionContext{})
		if err == nil || !strings.Contains(err.Error(), "não permitido") {
			t.Errorf("header %s error = %v", header, err)
		}
	}
}

func TestHTTPRequestRejectsHeaderLineBreak(t *testing.T) {
	_, err := httpRequestHeaders(map[string]interface{}{
		"headerMode":   "fields",
		"headerFields": []interface{}{map[string]interface{}{"key": "X-Test", "value": "safe\r\nInjected: true"}},
	}, ExecutionContext{})
	if err == nil || !strings.Contains(err.Error(), "valor inválido") {
		t.Fatalf("error = %v", err)
	}
}

func TestExecuteHTTPRequestRejectsPrivateDestination(t *testing.T) {
	result := (&Messenger{}).ExecuteHTTPRequest(map[string]interface{}{
		"method": "GET",
		"url":    "http://127.0.0.1/internal",
	}, ExecutionContext{})
	if result["error"] != true || !strings.Contains(result["message"].(string), "non-public IP") {
		t.Fatalf("result = %#v", result)
	}
}

package flowengine

import (
	"strings"
	"testing"
)

func TestParseCURLImportsJSONRequest(t *testing.T) {
	config, err := ParseCURL(`curl 'https://api.example.com/customers/42' \
  -X PATCH \
  -H 'Authorization: Bearer {{token}}' \
  -H 'Content-Type: application/json; charset=utf-8' \
  --data-raw '{"name":"{{name}}","active":true}'`)
	if err != nil {
		t.Fatalf("ParseCURL returned error: %v", err)
	}
	if config.Method != "PATCH" || config.URL != "https://api.example.com/customers/42" {
		t.Fatalf("unexpected request target: %#v", config)
	}
	if config.HeaderMode != "fields" || len(config.HeaderFields) != 2 {
		t.Fatalf("unexpected headers: %#v", config.HeaderFields)
	}
	if config.BodyMode != "rawJSON" || config.Body != `{"name":"{{name}}","active":true}` {
		t.Fatalf("unexpected body: mode=%q body=%q", config.BodyMode, config.Body)
	}
}

func TestParseCURLInfersDefaultsAndPreservesRawBody(t *testing.T) {
	config, err := ParseCURL(`curl https://api.example.com/events --data 'event=customer created'`)
	if err != nil {
		t.Fatalf("ParseCURL returned error: %v", err)
	}
	if config.Method != "POST" || config.BodyMode != "raw" || config.Body != "event=customer created" {
		t.Fatalf("unexpected config: %#v", config)
	}
	if value := curlHeaderValue(config.HeaderFields, "Content-Type"); value != "application/x-www-form-urlencoded" {
		t.Fatalf("content type = %q", value)
	}
}

func TestParseCURLImportsQueryAndURLencoding(t *testing.T) {
	config, err := ParseCURL(`curl -G 'https://api.example.com/search?active=true' --data-urlencode 'name=Ana Maria'`)
	if err != nil {
		t.Fatalf("ParseCURL returned error: %v", err)
	}
	if config.Method != "GET" || config.URL != "https://api.example.com/search?active=true&name=Ana+Maria" {
		t.Fatalf("unexpected query request: %#v", config)
	}
	if config.BodyMode != "none" || config.Body != "" {
		t.Fatalf("GET body was not cleared: %#v", config)
	}
}

func TestParseCURLImportsJSONFlagAndBasicAuth(t *testing.T) {
	config, err := ParseCURL(`curl --location --user 'client:secret' --json '{"ok":true}' https://api.example.com/items`)
	if err != nil {
		t.Fatalf("ParseCURL returned error: %v", err)
	}
	if config.Method != "POST" || config.BodyMode != "rawJSON" || !config.FollowRedirects {
		t.Fatalf("unexpected config: %#v", config)
	}
	if value := curlHeaderValue(config.HeaderFields, "Authorization"); value != "Basic Y2xpZW50OnNlY3JldA==" {
		t.Fatalf("authorization = %q", value)
	}
	if curlHeaderValue(config.HeaderFields, "Accept") != "application/json" || curlHeaderValue(config.HeaderFields, "Content-Type") != "application/json" {
		t.Fatalf("JSON headers were not imported: %#v", config.HeaderFields)
	}
}

func TestParseCURLKeepsEmptyDataAsPOST(t *testing.T) {
	config, err := ParseCURL(`curl https://api.example.com/events --data ''`)
	if err != nil {
		t.Fatalf("ParseCURL returned error: %v", err)
	}
	if config.Method != "POST" || config.BodyMode != "raw" || config.Body != "" {
		t.Fatalf("unexpected empty-data config: %#v", config)
	}
}

func TestParseCURLHandlesRawAtAndFragmentQuery(t *testing.T) {
	raw, err := ParseCURL(`curl https://api.example.com/events --data-raw '@literal'`)
	if err != nil || raw.Body != "@literal" {
		t.Fatalf("raw @ import = %#v, %v", raw, err)
	}
	query, err := ParseCURL(`curl -G 'https://api.example.com/search#results' --data-urlencode '=Ana Maria'`)
	if err != nil {
		t.Fatalf("query import: %v", err)
	}
	if query.URL != "https://api.example.com/search?Ana+Maria#results" {
		t.Fatalf("query URL = %q", query.URL)
	}
}

func TestParseCURLImportsWindowsCMDCommand(t *testing.T) {
	config, err := ParseCURL("curl ^\"https://api.example.com/items^\" ^\r\n  -H ^\"Content-Type: application/json^\" ^\r\n  --data-raw ^\"^{^\\^\"name^\\^\":^\\^\"Ana^\\^\"^}^\"")
	if err != nil {
		t.Fatalf("ParseCURL Windows command returned error: %v", err)
	}
	if config.URL != "https://api.example.com/items" || config.Body != `{"name":"Ana"}` || config.BodyMode != "rawJSON" {
		t.Fatalf("unexpected Windows config: %#v", config)
	}
}

func TestParseCURLImportsCombinedRedirectAndTimeoutFlags(t *testing.T) {
	config, err := ParseCURL(`curl -fsSL --connect-timeout 2.5 --max-time 8 https://api.example.com/status`)
	if err != nil {
		t.Fatalf("ParseCURL returned error: %v", err)
	}
	if !config.FollowRedirects || config.ConnectTimeout != 2500 || config.Timeout != 8000 {
		t.Fatalf("unexpected transport config: %#v", config)
	}
}

func TestParseCURLImportsPowerShellAndANSICQuotes(t *testing.T) {
	powerShell, err := ParseCURL(`curl 'https://api.example.com/items' --data-raw '{"name":"O''Brien"}' -H 'Content-Type: application/json'`)
	if err != nil {
		t.Fatalf("PowerShell command: %v", err)
	}
	if powerShell.Body != `{"name":"O'Brien"}` {
		t.Fatalf("PowerShell body = %q", powerShell.Body)
	}
	ansi, err := ParseCURL(`curl https://api.example.com/items --data-raw $'A\x42\103'`)
	if err != nil {
		t.Fatalf("ANSI-C command: %v", err)
	}
	if ansi.Body != "ABC" {
		t.Fatalf("ANSI-C body = %q", ansi.Body)
	}
}

func TestParseCURLImportsMultipartFormFields(t *testing.T) {
	config, err := ParseCURL(`curl https://api.example.com/customers \
  -F 'name=Ana' \
  --form 'metadata={"active":true};type=application/json' \
  --form-string 'handle=@literal'`)
	if err != nil {
		t.Fatalf("ParseCURL multipart returned error: %v", err)
	}
	if config.Method != "POST" || config.BodyMode != "multipart" || len(config.BodyFields) != 3 {
		t.Fatalf("unexpected multipart config: %#v", config)
	}
	if config.BodyFields[1].Key != "metadata" || config.BodyFields[1].Value != `{"active":true}` || config.BodyFields[1].ContentType != "application/json" {
		t.Fatalf("unexpected typed multipart field: %#v", config.BodyFields[1])
	}
	if config.BodyFields[2].Value != "@literal" {
		t.Fatalf("form-string value = %q", config.BodyFields[2].Value)
	}
}

func TestParseCURLMultipartRespectsQuotedTypeText(t *testing.T) {
	config, err := ParseCURL(`curl https://api.example.com/items \
  -F 'note="abc;type=text/plain"' \
  -F 'metadata={};type="application/json; charset=utf-8"'`)
	if err != nil {
		t.Fatalf("ParseCURL multipart quoting returned error: %v", err)
	}
	if config.BodyFields[0].Value != "abc;type=text/plain" || config.BodyFields[0].ContentType != "" {
		t.Fatalf("quoted text field changed: %#v", config.BodyFields[0])
	}
	if config.BodyFields[1].Value != "{}" || config.BodyFields[1].ContentType != "application/json; charset=utf-8" {
		t.Fatalf("parameterized type field changed: %#v", config.BodyFields[1])
	}
}

func TestParseCURLRejectsUnsupportedInputs(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{name: "multipart local file", command: `curl https://api.example.com -F 'file=@report.pdf'`, want: "arquivo local"},
		{name: "local file", command: `curl https://api.example.com --data-binary @payload.json`, want: "arquivos locais"},
		{name: "blocked header", command: `curl https://api.example.com -H 'Host: internal.local'`, want: "não permitido"},
		{name: "unclosed quote", command: `curl 'https://api.example.com`, want: "aspas não fechadas"},
		{name: "unknown option", command: `curl --cert certificate.pem https://api.example.com`, want: "não suportada"},
		{name: "insecure TLS", command: `curl --insecure https://api.example.com`, want: "certificados TLS válidos"},
		{name: "multipart headers modifier", command: `curl https://api.example.com -F 'note=hello;headers="X-Tag: one"'`, want: "headers"},
		{name: "multipart filename modifier", command: `curl https://api.example.com -F 'note=hello;filename=note.txt'`, want: "filename"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseCURL(test.command)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

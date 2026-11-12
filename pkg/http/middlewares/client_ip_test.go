package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kataras/iris/v12"
)

func TestForwardedClientIP(t *testing.T) {
	tests := []struct {
		name    string
		remote  string
		headers map[string]string
		want    string
	}{
		{name: "cloudflare via local proxy", remote: "127.0.0.1:5000", headers: map[string]string{"CF-Connecting-IP": "203.0.113.7", "X-Forwarded-For": "203.0.113.7, 172.68.1.1"}, want: "203.0.113.7"},
		{name: "ipv6 visitor", remote: "[::1]:5000", headers: map[string]string{"CF-Connecting-IP": "2001:db8::5"}, want: "2001:db8::5"},
		{name: "docker network proxy", remote: "172.18.0.4:5000", headers: map[string]string{"X-Real-IP": "198.51.100.9"}, want: "198.51.100.9"},
		{name: "forwarded for skips own proxies", remote: "127.0.0.1:5000", headers: map[string]string{"X-Forwarded-For": "1.2.3.4, 198.51.100.9, 10.0.0.2"}, want: "198.51.100.9"},
		{name: "direct client cannot forge", remote: "198.51.100.20:5000", headers: map[string]string{"CF-Connecting-IP": "1.1.1.1", "X-Forwarded-For": "1.1.1.1"}, want: ""},
		{name: "invalid header ignored", remote: "127.0.0.1:5000", headers: map[string]string{"CF-Connecting-IP": "not-an-ip"}, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := forwardedClientIP(test.remote, func(name string) string { return test.headers[name] })
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestRealClientIPUpdatesRemoteAddr(t *testing.T) {
	app := iris.New()
	app.UseRouter(RealClientIP)
	app.Get("/", func(ctx iris.Context) { ctx.WriteString(ctx.RemoteAddr()) })
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "127.0.0.1:41000"
	request.Header.Set("CF-Connecting-IP", "203.0.113.7")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if got := response.Body.String(); got != "203.0.113.7" {
		t.Fatalf("RemoteAddr() = %q, want visitor IP", got)
	}
}

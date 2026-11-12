package middlewares

import (
	"net"
	"strings"

	"github.com/kataras/iris/v12"
)

// RealClientIP replaces the request's remote address with the visitor's IP
// when the connection comes from a local or private proxy (cloudflared, nginx,
// a container network). Headers from any other peer are ignored, so a client
// that reaches the app directly cannot forge its address. Everything that
// reads ctx.RemoteAddr() (audit logs, login history, rate limits) then sees
// the real IP.
func RealClientIP(ctx iris.Context) {
	request := ctx.Request()
	if ip := forwardedClientIP(request.RemoteAddr, request.Header.Get); ip != "" {
		_, port, err := net.SplitHostPort(request.RemoteAddr)
		if err != nil {
			port = "0"
		}
		request.RemoteAddr = net.JoinHostPort(ip, port)
	}
	ctx.Next()
}

func forwardedClientIP(remoteAddr string, header func(string) string) string {
	host := remoteAddr
	if value, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = value
	}
	if !trustedProxy(net.ParseIP(host)) {
		return ""
	}
	// Cloudflare always sets CF-Connecting-IP to the visitor's address.
	for _, name := range []string{"CF-Connecting-IP", "True-Client-IP", "X-Real-IP"} {
		if ip := parseHeaderIP(header(name)); ip != "" {
			return ip
		}
	}
	// X-Forwarded-For: the rightmost entry that is not one of our own proxies.
	hops := strings.Split(header("X-Forwarded-For"), ",")
	for index := len(hops) - 1; index >= 0; index-- {
		ip := net.ParseIP(strings.TrimSpace(hops[index]))
		if ip == nil {
			continue
		}
		if !trustedProxy(ip) {
			return ip.String()
		}
	}
	return ""
}

func parseHeaderIP(value string) string {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return ""
	}
	return ip.String()
}

func trustedProxy(ip net.IP) bool {
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

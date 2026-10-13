package middlewares

import (
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kataras/iris/v12"
)

func SecurityHeaders(ctx iris.Context) {
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("X-Frame-Options", "DENY")
	ctx.Header("Referrer-Policy", "strict-origin-when-cross-origin")
	ctx.Header("Permissions-Policy", "camera=(), microphone=(self), geolocation=()")
	if ctx.Request().TLS != nil || strings.EqualFold(ctx.GetHeader("X-Forwarded-Proto"), "https") {
		ctx.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	}
	ctx.Next()
}

func RequireSameOrigin(ctx iris.Context) {
	if safeHTTPMethod(ctx.Method()) || sameOriginRequest(ctx) {
		ctx.Next()
		return
	}
	ctx.StopWithStatus(iris.StatusForbidden)
}

func RequireCookieSameOrigin(ctx iris.Context) {
	if IsAPIAuthentication(ctx) {
		ctx.Next()
		return
	}
	RequireSameOrigin(ctx)
}

func RequireCompanyAdmin(ctx iris.Context) {
	agent, err := AgentIdentifier(ctx)
	if err != nil {
		return
	}
	if !agent.IsAdmin() {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	ctx.Next()
}

func LimitRequestBody(maxBytes int64) iris.Handler {
	return func(ctx iris.Context) {
		if !safeHTTPMethod(ctx.Method()) && ctx.Request().Body != nil {
			ctx.Request().Body = http.MaxBytesReader(ctx.ResponseWriter(), ctx.Request().Body, maxBytes)
		}
		ctx.Next()
	}
}

func IPRateLimit(limit int, window time.Duration) iris.Handler {
	type entry struct {
		count int
		reset time.Time
	}
	var mu sync.Mutex
	entries := map[string]entry{}
	return func(ctx iris.Context) {
		key := ctx.RemoteAddr()
		if host, _, err := net.SplitHostPort(key); err == nil {
			key = host
		}
		now := time.Now()
		mu.Lock()
		current := entries[key]
		if current.reset.IsZero() || !now.Before(current.reset) {
			current = entry{reset: now.Add(window)}
		}
		current.count++
		entries[key] = current
		if len(entries) > 10000 {
			for candidate, value := range entries {
				if !now.Before(value.reset) {
					delete(entries, candidate)
				}
			}
		}
		allowed := current.count <= limit
		retryAfter := int(time.Until(current.reset).Seconds())
		mu.Unlock()
		if !allowed {
			if retryAfter < 1 {
				retryAfter = 1
			}
			ctx.Header("Retry-After", strconv.Itoa(retryAfter))
			ctx.StopWithStatus(iris.StatusTooManyRequests)
			return
		}
		ctx.Next()
	}
}

func RequireStructuredContentType(ctx iris.Context) {
	if safeHTTPMethod(ctx.Method()) || ctx.Request().ContentLength == 0 {
		ctx.Next()
		return
	}
	path := ctx.Path()
	mediaType, _, err := mime.ParseMediaType(ctx.GetHeader("Content-Type"))
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnsupportedMediaType)
		return
	}
	if path == "/management/api/ai-agents/knowledge/pdf" || path == "/flows/api/media" {
		if mediaType != "multipart/form-data" {
			ctx.StopWithStatus(iris.StatusUnsupportedMediaType)
			return
		}
		ctx.Next()
		return
	}
	if strings.HasPrefix(path, "/api/") && !IsAPIAuthentication(ctx) {
		if mediaType != "application/x-www-form-urlencoded" && mediaType != "multipart/form-data" {
			ctx.StopWithStatus(iris.StatusUnsupportedMediaType)
			return
		}
		ctx.Next()
		return
	}
	if mediaType != "application/json" {
		ctx.StopWithStatus(iris.StatusUnsupportedMediaType)
		return
	}
	ctx.Next()
}

func sameOriginRequest(ctx iris.Context) bool {
	fetchSite := strings.ToLower(strings.TrimSpace(ctx.GetHeader("Sec-Fetch-Site")))
	if fetchSite == "cross-site" || fetchSite == "same-site" {
		return false
	}
	origin := strings.TrimSpace(ctx.GetHeader("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host != "" && strings.EqualFold(parsed.Host, ctx.Host())
}

func safeHTTPMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

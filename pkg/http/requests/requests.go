package requests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

const defaultTimeout = 30 * time.Second

type Request struct {
	URL              string            `json:"url"`
	Payload          interface{}       `json:"payload"` //map[string]any
	RequestBody      []byte            `json:"-"`
	Headers          map[string]string `json:"headers"`
	Method           string            `json:"method"`
	Timeout          time.Duration     `json:"-"`
	PublicOnly       bool              `json:"-"`
	MaxRequestBytes  int64             `json:"-"`
	MaxResponseBytes int64             `json:"-"`
	FollowRedirects  bool              `json:"-"`
	ConnectTimeout   time.Duration     `json:"-"`
	Response
}

type Response struct {
	StatusCode int    `json:"status_code"`
	Body       []byte `json:"body"`
}

func (r *Request) Do() error {
	body := r.RequestBody
	if body == nil && r.Payload != nil {
		var err error
		body, err = json.Marshal(r.Payload)
		if err != nil {
			return fmt.Errorf("marshal request payload: %w", err)
		}
	}
	if r.MaxRequestBytes > 0 && int64(len(body)) > r.MaxRequestBytes {
		return fmt.Errorf("request body exceeds the %d-byte limit", r.MaxRequestBytes)
	}
	req, err := http.NewRequest(r.Method, r.URL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	if r.PublicOnly {
		if err := validatePublicURL(req.URL); err != nil {
			return err
		}
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	client := http.Client{Timeout: timeout}
	if r.PublicOnly {
		transport := publicHTTPTransport
		if r.ConnectTimeout > 0 {
			transport = publicHTTPTransport.Clone()
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				connectContext, cancel := context.WithTimeout(ctx, r.ConnectTimeout)
				defer cancel()
				return dialPublicAddress(connectContext, network, address)
			}
		}
		client.Transport = transport
		client.CheckRedirect = publicRedirectPolicy(r.FollowRedirects)
	} else if r.ConnectTimeout > 0 {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = (&net.Dialer{Timeout: r.ConnectTimeout}).DialContext
		client.Transport = transport
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	r.Response.StatusCode = resp.StatusCode
	reader := io.Reader(resp.Body)
	if r.MaxResponseBytes > 0 {
		reader = io.LimitReader(resp.Body, r.MaxResponseBytes+1)
	}
	r.Response.Body, err = io.ReadAll(reader)
	if err != nil {
		return err
	}
	if r.MaxResponseBytes > 0 && int64(len(r.Response.Body)) > r.MaxResponseBytes {
		r.Response.Body = nil
		return fmt.Errorf("response body exceeds the %d-byte limit", r.MaxResponseBytes)
	}
	if r.Response.StatusCode < http.StatusOK || r.Response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("request failed with status %d", r.Response.StatusCode)
	}
	return nil
}

func publicRedirectPolicy(follow bool) func(*http.Request, []*http.Request) error {
	return func(request *http.Request, via []*http.Request) error {
		if !follow {
			return http.ErrUseLastResponse
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return validatePublicURL(request.URL)
	}
}

var publicHTTPTransport = func() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialPublicAddress
	return transport
}()

func validatePublicURL(target *url.URL) error {
	if target.Scheme != "http" && target.Scheme != "https" {
		return fmt.Errorf("only HTTP and HTTPS URLs are allowed")
	}
	if target.User != nil {
		return fmt.Errorf("credentials in the URL are not allowed")
	}
	if target.Hostname() == "" {
		return fmt.Errorf("URL must include a host")
	}
	return nil
}

func dialPublicAddress(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid destination address: %w", err)
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve destination: %w", err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("destination host has no IP address")
	}
	for _, address := range addresses {
		if !isPublicAddress(address) {
			return nil, fmt.Errorf("destination resolves to a non-public IP address")
		}
	}

	dialer := net.Dialer{}
	var lastErr error
	for _, address := range addresses {
		connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
		if err == nil {
			return connection, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("connect to destination: %w", lastErr)
}

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func isPublicAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

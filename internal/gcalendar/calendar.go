// Package gcalendar talks to Google Calendar with a service account: the
// calendar owner shares the calendar with the account's e-mail ("Fazer
// alterações nos eventos"), and Sophus signs its own access tokens.
package gcalendar

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Endpoints are variables so tests can point them at a local server.
var (
	APIBase         = "https://www.googleapis.com/calendar/v3"
	defaultTokenURL = "https://oauth2.googleapis.com/token"
	httpClient      = &http.Client{Timeout: 20 * time.Second}
)

const scope = "https://www.googleapis.com/auth/calendar"

// ServiceAccount holds the fields Sophus uses from a Google service account
// key file (JSON).
type ServiceAccount struct {
	Type        string `json:"type"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
	key         *rsa.PrivateKey
}

// ParseServiceAccount validates a service account key file.
func ParseServiceAccount(raw string) (*ServiceAccount, error) {
	var account ServiceAccount
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &account); err != nil {
		return nil, errors.New("o JSON da conta de serviço do Google é inválido")
	}
	if account.Type != "service_account" || !strings.Contains(account.ClientEmail, "@") {
		return nil, errors.New("o arquivo não é uma chave de conta de serviço do Google")
	}
	block, _ := pem.Decode([]byte(account.PrivateKey))
	if block == nil {
		return nil, errors.New("a chave privada da conta de serviço é inválida")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		parsed, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if err != nil || !ok {
		return nil, errors.New("a chave privada da conta de serviço é inválida")
	}
	account.key = key
	// The file's token_uri is ignored: tokens only come from Google.
	account.TokenURI = defaultTokenURL
	return &account, nil
}

type cachedToken struct {
	value   string
	expires time.Time
}

var (
	tokensMu sync.Mutex
	tokens   = map[string]cachedToken{}
)

func base64URL(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }

// accessToken returns a cached OAuth token or signs a new JWT assertion.
func (account *ServiceAccount) accessToken(ctx context.Context) (string, error) {
	cacheKey := account.ClientEmail + "|" + account.TokenURI
	tokensMu.Lock()
	cached, ok := tokens[cacheKey]
	tokensMu.Unlock()
	if ok && time.Until(cached.expires) > time.Minute {
		return cached.value, nil
	}
	now := time.Now()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]interface{}{
		"iss": account.ClientEmail, "scope": scope, "aud": account.TokenURI,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	unsigned := base64URL(header) + "." + base64URL(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(nil, account.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {unsigned + "." + base64URL(signature)}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, account.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := do(request, &result); err != nil {
		return "", fmt.Errorf("o Google recusou a conta de serviço: %w", err)
	}
	if result.AccessToken == "" {
		return "", errors.New("o Google não retornou um token de acesso")
	}
	if result.ExpiresIn <= 0 {
		result.ExpiresIn = 3600
	}
	tokensMu.Lock()
	tokens[cacheKey] = cachedToken{value: result.AccessToken, expires: now.Add(time.Duration(result.ExpiresIn) * time.Second)}
	tokensMu.Unlock()
	return result.AccessToken, nil
}

func do(request *http.Request, target interface{}) error {
	response, err := httpClient.Do(request)
	if err != nil {
		return errors.New("não foi possível conectar ao Google")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &failure)
		message := strings.TrimSpace(failure.Error.Message + " " + failure.Description)
		switch response.StatusCode {
		case http.StatusNotFound:
			return errors.New("agenda não encontrada; confira o ID e se ela foi compartilhada com a conta de serviço")
		case http.StatusForbidden:
			return errors.New("sem permissão na agenda; compartilhe-a com a conta de serviço com permissão para alterar eventos")
		}
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return fmt.Errorf("Google respondeu %d: %s", response.StatusCode, message)
	}
	if target == nil {
		return nil
	}
	return json.Unmarshal(body, target)
}

func (account *ServiceAccount) call(ctx context.Context, method, path string, payload, target interface{}) error {
	token, err := account.accessToken(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, APIBase+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	return do(request, target)
}

// Interval is a busy period of the calendar.
type Interval struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Busy returns the calendar's busy periods between from and to.
func (account *ServiceAccount) Busy(ctx context.Context, calendarID string, from, to time.Time) ([]Interval, error) {
	var result struct {
		Calendars map[string]struct {
			Busy   []Interval `json:"busy"`
			Errors []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"calendars"`
	}
	err := account.call(ctx, http.MethodPost, "/freeBusy", map[string]interface{}{
		"timeMin": from.Format(time.RFC3339), "timeMax": to.Format(time.RFC3339), "items": []map[string]string{{"id": calendarID}},
	}, &result)
	if err != nil {
		return nil, err
	}
	calendar, ok := result.Calendars[calendarID]
	if !ok {
		return nil, errors.New("o Google não retornou a agenda consultada")
	}
	if len(calendar.Errors) > 0 {
		if calendar.Errors[0].Reason == "notFound" {
			return nil, errors.New("agenda não encontrada; confira o ID e se ela foi compartilhada com a conta de serviço")
		}
		return nil, fmt.Errorf("o Google não consultou a agenda: %s", calendar.Errors[0].Reason)
	}
	return calendar.Busy, nil
}

// Event is an event to create.
type Event struct {
	Summary     string
	Description string
	Start       time.Time
	End         time.Time
	TimeZone    string
}

// CreateEvent adds the event to the calendar and returns its ID.
func (account *ServiceAccount) CreateEvent(ctx context.Context, calendarID string, event Event) (string, error) {
	when := func(at time.Time) map[string]string {
		return map[string]string{"dateTime": at.Format(time.RFC3339), "timeZone": event.TimeZone}
	}
	var result struct {
		ID string `json:"id"`
	}
	err := account.call(ctx, http.MethodPost, "/calendars/"+url.PathEscape(calendarID)+"/events", map[string]interface{}{
		"summary": event.Summary, "description": event.Description, "start": when(event.Start), "end": when(event.End),
	}, &result)
	return result.ID, err
}

// FreeSlots lists the starts of free slots of the given length within the
// working window of one day, skipping busy periods and the past.
func FreeSlots(day time.Time, workStart, workEnd time.Duration, slot time.Duration, busy []Interval, now time.Time) []time.Time {
	midnight := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	var slots []time.Time
	for start := midnight.Add(workStart); !start.Add(slot).After(midnight.Add(workEnd)); start = start.Add(slot) {
		end := start.Add(slot)
		if start.Before(now) {
			continue
		}
		free := true
		for _, period := range busy {
			if start.Before(period.End) && end.After(period.Start) {
				free = false
				break
			}
		}
		if free {
			slots = append(slots, start)
		}
	}
	return slots
}

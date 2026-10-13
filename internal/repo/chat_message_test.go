package repo

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type chatMessageDriver struct {
	queries  int
	inserted []driver.NamedValue
}

func (d *chatMessageDriver) Open(string) (driver.Conn, error) { return &chatMessageConn{d}, nil }

type chatMessageConn struct{ d *chatMessageDriver }

func (c *chatMessageConn) Close() error { return nil }
func (c *chatMessageConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected conversation transaction")
}
func (c *chatMessageConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared query")
}
func (c *chatMessageConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.d.queries++
	if !strings.Contains(query, "SELECT EXISTS(SELECT 1 FROM messages") {
		return nil, errors.New("unexpected contact/conversation lookup")
	}
	return &chatMessageRows{}, nil
}
func (c *chatMessageConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if strings.HasPrefix(query, "INSERT INTO public.messages") {
		c.d.inserted = append([]driver.NamedValue(nil), args...)
		return driver.RowsAffected(1), nil
	}
	// Read sync is independently covered by its own tests. Do not launch it
	// against this intentionally limited persistence fixture.
	return nil, errors.New("read synchronization unavailable in fixture")
}

type chatMessageRows struct{ read bool }

func (*chatMessageRows) Columns() []string { return []string{"exists"} }
func (*chatMessageRows) Close() error      { return nil }
func (r *chatMessageRows) Next(values []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	values[0] = false
	return nil
}

func TestSaveChatTextReusesConversationWithoutRemoteLookup(t *testing.T) {
	testDriver := &chatMessageDriver{}
	sql.Register("chat-message-persistence", testDriver)
	testDB, err := sql.Open("chat-message-persistence", "")
	if err != nil {
		t.Fatal(err)
	}
	oldDB, oldURL := db, ApiBaseURL
	db = testDB
	t.Cleanup(func() { db = oldDB; ApiBaseURL = oldURL; testDB.Close() })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("saving a chat message called Evolution: %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	ApiBaseURL = server.URL
	for _, group := range []bool{false, true} {
		testDriver.queries = 0
		testDriver.inserted = nil
		conversation := Conversation{Id: 8, ConnectionID: 11, Status: ConversationStatusOpen, Contact: Contact{Id: 5, CompanyId: 3, Name: "Ana", Number: "5511999999999", IsGroup: group}}
		message := TextMessageEVO{Text: "Texto assinado", MessageBaseEVO: MessageBaseEVO{QuotedEVO: QuotedEVO{MessageID: "QUOTED"}}, JSON: []byte(`{"data":{"key":{"id":"SENT"},"Info":{"Timestamp":"2026-10-08T12:00:00Z"}}}`)}
		id, err := SaveChatTextMessage(message, ConnectionEVO{Id: 11, CompanyID: 3}, conversation)
		if err != nil || id != "SENT" {
			t.Fatalf("save: id=%s err=%v", id, err)
		}
		if testDriver.queries != 1 {
			t.Fatalf("unexpected lookups: %d", testDriver.queries)
		}
		args := testDriver.inserted
		if len(args) != 18 || args[1].Value != message.Text || args[2].Value != int64(8) || args[3].Value != "QUOTED" || args[8].Value != true || args[9].Value != group {
			t.Fatalf("wrong conversation/text/quote/direction in insert: %+v", args)
		}
	}
	if _, err := SaveChatTextMessage(TextMessageEVO{}, ConnectionEVO{Id: 12, CompanyID: 3}, Conversation{Id: 8, ConnectionID: 11, Contact: Contact{Id: 5, CompanyId: 3}}); err == nil {
		t.Fatal("wrong connection context accepted")
	}
}

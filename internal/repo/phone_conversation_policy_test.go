package repo

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

type phonePolicyDriver struct {
	contact                                         bool
	status                                          string
	contactsCreated, conversationsCreated, reopened int
}

var phonePolicyDriverSequence atomic.Uint64

func (d *phonePolicyDriver) Open(string) (driver.Conn, error) { return &phonePolicyConn{d}, nil }

type phonePolicyConn struct{ state *phonePolicyDriver }

func (c *phonePolicyConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}
func (c *phonePolicyConn) Close() error              { return nil }
func (c *phonePolicyConn) Begin() (driver.Tx, error) { return phonePolicyTx{}, nil }

type phonePolicyTx struct{}

func (phonePolicyTx) Commit() error   { return nil }
func (phonePolicyTx) Rollback() error { return nil }

type phonePolicyRows struct {
	columns []string
	values  []driver.Value
	read    bool
}

func (r *phonePolicyRows) Columns() []string { return r.columns }
func (r *phonePolicyRows) Close() error      { return nil }
func (r *phonePolicyRows) Next(values []driver.Value) error {
	if r.read || r.values == nil {
		return io.EOF
	}
	r.read = true
	copy(values, r.values)
	return nil
}

func (c *phonePolicyConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	s := c.state
	switch {
	case strings.Contains(query, "SELECT id FROM contacts"):
		if args[0].Value != int64(3) {
			return nil, errors.New("contact lookup escaped company scope")
		}
		rows := &phonePolicyRows{columns: []string{"id"}}
		if s.contact {
			rows.values = []driver.Value{int64(7)}
		}
		return rows, nil
	case strings.Contains(query, "INSERT INTO contacts"):
		s.contact = true
		s.contactsCreated++
		return &phonePolicyRows{columns: []string{"id"}, values: []driver.Value{int64(7)}}, nil
	case strings.Contains(query, "SELECT id, status FROM conversations"):
		if args[0].Value != int64(7) || args[1].Value != int64(9) {
			return nil, errors.New("conversation lookup escaped contact/connection scope")
		}
		rows := &phonePolicyRows{columns: []string{"id", "status"}}
		if s.status != "" {
			rows.values = []driver.Value{int64(12), s.status}
		}
		return rows, nil
	case strings.Contains(query, "INSERT INTO conversations"):
		s.status = "open"
		s.conversationsCreated++
		return &phonePolicyRows{columns: []string{"id"}, values: []driver.Value{int64(12)}}, nil
	default:
		return nil, fmt.Errorf("unexpected policy query: %s", query)
	}
}

func (c *phonePolicyConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if strings.Contains(query, "UPDATE conversations SET status") {
		c.state.reopened++
		c.state.status = "open"
	}
	return driver.RowsAffected(1), nil
}

func TestPhoneConversationPolicyCreationAndExistingHistory(t *testing.T) {
	tests := []struct {
		name                                  string
		contact                               bool
		status                                string
		allow, blocked                        bool
		newContact, newConversation, reopened int
	}{
		{"disabled unknown contact", false, "", false, true, 0, 0, 0},
		{"disabled contact without appointment", true, "", false, true, 0, 0, 0},
		{"disabled existing open appointment", true, "open", false, false, 0, 0, 0},
		{"disabled existing closed appointment", true, "closed", false, false, 0, 0, 0},
		{"enabled new contact and appointment", false, "", true, false, 1, 1, 0},
		{"enabled existing closed appointment", true, "closed", true, false, 0, 1, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := &phonePolicyDriver{contact: test.contact, status: test.status}
			name := fmt.Sprintf("phone-conversation-policy-%d", phonePolicyDriverSequence.Add(1))
			sql.Register(name, state)
			testDB, err := sql.Open(name, "")
			if err != nil {
				t.Fatal(err)
			}
			previous := db
			db = testDB
			t.Cleanup(func() { db = previous; _ = testDB.Close() })
			id, err := checkExistentConversation(9, Contact{CompanyId: 3, Number: "5511888888888", JID: "5511888888888@s.whatsapp.net"}, test.allow, !test.allow)
			if test.blocked {
				if !errors.Is(err, ErrPhoneConversationCreationDisabled) {
					t.Fatalf("creation should be skipped: id=%d err=%v", id, err)
				}
			} else if err != nil || id != 12 {
				t.Fatalf("existing/new appointment unavailable: id=%d err=%v", id, err)
			}
			if state.contactsCreated != test.newContact || state.conversationsCreated != test.newConversation || state.reopened != test.reopened {
				t.Fatalf("incorrect policy writes: %+v", state)
			}
		})
	}
}

func TestPhoneCreationPreferencePreservesStoredValue(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		var settings CompanyOperationSettings
		if err := json.Unmarshal([]byte(fmt.Sprintf(`{"outboundMessagesEnabled":%t}`, enabled)), &settings); err != nil {
			t.Fatal(err)
		}
		if settings.PhoneConversationCreationEnabled != enabled {
			t.Fatal("existing company preference was lost")
		}
		data, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), fmt.Sprintf(`"outboundMessagesEnabled":%t`, enabled)) {
			t.Fatal("settings UI/API round trip lost preference")
		}
	}
}

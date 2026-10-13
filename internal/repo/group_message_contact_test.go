package repo

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
)

type groupContactDriver struct{ name string }

func (d groupContactDriver) Open(string) (driver.Conn, error) { return groupContactConn{d.name}, nil }

type groupContactConn struct{ name string }

func (groupContactConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (groupContactConn) Close() error              { return nil }
func (groupContactConn) Begin() (driver.Tx, error) { return nil, errors.New("unexpected transaction") }
func (c groupContactConn) QueryContext(_ context.Context, _ string, args []driver.NamedValue) (driver.Rows, error) {
	if args[0].Value != int64(3) {
		return nil, errors.New("group lookup escaped company scope")
	}
	return &groupContactRows{name: c.name}, nil
}

type groupContactRows struct {
	name string
	read bool
}

func (*groupContactRows) Columns() []string { return []string{"id", "name"} }
func (*groupContactRows) Close() error      { return nil }
func (r *groupContactRows) Next(values []driver.Value) error {
	if r.read || r.name == "" {
		return io.EOF
	}
	r.read = true
	values[0] = int64(7)
	values[1] = r.name
	return nil
}

var groupContactTestSequence atomic.Uint64

func TestGroupMessageDoesNotDependOnRemoteGroupInfo(t *testing.T) {
	for _, test := range []struct{ name, stored, payload, want string }{
		{"webhook name", "", `{"JID":"120363123456@g.us","Name":"Equipe"}`, "Equipe"},
		{"nested webhook name", "", `{"GroupName":{"Name":"Equipe"}}`, "Equipe"},
		{"stored name", "Grupo existente", `{}`, "Grupo existente"},
		{"unknown group fallback", "", `{}`, groupContactPlaceholder},
		{"mismatched metadata", "Grupo correto", `{"JID":"120363999999@g.us","Name":"Outro grupo"}`, "Grupo correto"},
	} {
		t.Run(test.name, func(t *testing.T) {
			name := fmt.Sprintf("group-contact-%d", groupContactTestSequence.Add(1))
			sql.Register(name, groupContactDriver{name: test.stored})
			testDB, err := sql.Open(name, "")
			if err != nil {
				t.Fatal(err)
			}
			previous := db
			db = testDB
			t.Cleanup(func() { db = previous; _ = testDB.Close() })
			message := EventMessageEVO{}
			message.Data.Info.Chat = "120363123456@g.us"
			message.Data.GroupData = json.RawMessage(test.payload)
			contact, err := groupContactFromWebhook(message, 3)
			if err != nil {
				t.Fatal(err)
			}
			if contact.Name != test.want || !contact.IsGroup || contact.CompanyId != 3 || contact.JID != message.Data.Info.Chat {
				t.Fatalf("incorrect group resolution: %+v", contact)
			}
		})
	}
}

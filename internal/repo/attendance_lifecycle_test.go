package repo

import (
	"context"
	"database/sql"
	"errors"
	"github.com/joho/godotenv"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestClosedAttendanceCreatesSeparateTranscriptPostgres(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_POSTGRES") == "" {
		t.Skip("set ZUBLY_TEST_POSTGRES for temporary-table attendance lifecycle tests")
	}
	config, err := godotenv.Read("../../.env")
	if err != nil {
		t.Fatal("workspace database configuration required")
	}
	dsn := &url.URL{Scheme: "postgres", Host: config["POSTGRES_HOST"] + ":" + config["POSTGRES_PORT"], Path: "/" + config["POSTGRES_DB"], User: url.UserPassword(config["POSTGRES_USER"], config["POSTGRES_PASSWORD"])}
	dsn.RawQuery = url.Values{"sslmode": []string{"disable"}, "options": []string{"-csearch_path=pg_temp"}}.Encode()
	testDB, err := sql.Open("postgres", dsn.String())
	if err != nil {
		t.Fatal("cannot open isolated connection")
	}
	defer testDB.Close()
	testDB.SetMaxOpenConns(1)
	testDB.SetMaxIdleConns(1)
	previous := db
	db = testDB
	t.Cleanup(func() { db = previous })
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	fixture := `CREATE TEMP TABLE contacts(id integer PRIMARY KEY,"companyId" integer,name text,number text,jid text,lid text,"isGroup" boolean,"isBlocked" boolean,"avatarRefreshRequestedAt" timestamptz);
CREATE TEMP TABLE connections(id integer PRIMARY KEY,"companyId" integer,status text);
CREATE TEMP TABLE conversations(id serial PRIMARY KEY,status text,"contactId" integer,"connectionId" integer,"agentId" integer,"departmentId" integer,url uuid,"createdAt" timestamptz,"updatedAt" timestamptz,protocol bigint NOT NULL DEFAULT 1);
CREATE TEMP TABLE messages(id integer PRIMARY KEY,"conversationId" integer,"createdAt" timestamptz,"mediaType" text);
CREATE TEMP TABLE flow_executions(id serial PRIMARY KEY,"conversationId" integer,status text,"errorMessage" text,"completedAt" timestamptz,"updatedAt" timestamptz);
INSERT INTO contacts VALUES(7,3,'Cliente','5511888888888','5511888888888@s.whatsapp.net','',false,false,NULL);
INSERT INTO connections VALUES(9,3,'connected');
INSERT INTO conversations(id,status,"contactId","connectionId","agentId","departmentId",url,"createdAt","updatedAt") VALUES(12,'closed',7,9,99,5,'00000000-0000-0000-0000-000000000012',now()-interval '10 days',now()-interval '1 day');
SELECT setval(pg_get_serial_sequence('conversations','id'),12);
INSERT INTO messages VALUES(1,12,now()-interval '10 days','text');`
	if _, err = testDB.ExecContext(ctx, fixture); err != nil {
		t.Fatal(err)
	}
	result, err := StartOrReuseConversation(Agent{Id: 8, CompanyId: 3, Role: RoleAdmin}, 7, 9)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.Reopened {
		t.Fatal("closed attendance was reused")
	}
	var currentID int
	if err = testDB.QueryRow(`SELECT id FROM conversations WHERE url=$1`, result.URL).Scan(&currentID); err != nil {
		t.Fatal(err)
	}
	if currentID == 12 {
		t.Fatal("new attendance retained the old ID")
	}
	var assignedOwner int
	var assignedDepartment sql.NullInt64
	if err = testDB.QueryRow(`SELECT "agentId","departmentId" FROM conversations WHERE id=$1`, currentID).Scan(&assignedOwner, &assignedDepartment); err != nil {
		t.Fatal(err)
	}
	if assignedOwner != 8 || assignedDepartment.Valid {
		t.Fatal("new attendance was not assigned directly to its initiator")
	}
	var status string
	var owner, department int
	if err = testDB.QueryRow(`SELECT status,"agentId","departmentId" FROM conversations WHERE id=12`).Scan(&status, &owner, &department); err != nil {
		t.Fatal(err)
	}
	if status != "closed" || owner != 99 || department != 5 {
		t.Fatal("opening a new attendance changed archived metadata")
	}
	if _, err = testDB.Exec(`INSERT INTO messages VALUES(2,$1,now(),'text')`, currentID); err != nil {
		t.Fatal(err)
	}
	assertIDs := func(query string, want []int) {
		t.Helper()
		rows, err := testDB.Query(query, currentID, 3, 0, 51)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var actual []int
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			actual = append(actual, id)
		}
		if len(actual) != len(want) {
			t.Fatalf("transcript IDs=%v, want %v", actual, want)
		}
		for i := range want {
			if actual[i] != want[i] {
				t.Fatalf("transcript IDs=%v, want %v", actual, want)
			}
		}
	}
	assertIDs(conversationMessagePageIDsSQL+`SELECT id FROM page_ids WHERE $2::integer=3 ORDER BY id`, []int{2})
	assertIDs(contactHistoryPageIDsSQL+`SELECT id FROM page_ids ORDER BY id`, []int{1, 2})
	var reusedID int
	reusedID, err = checkExistentConversation(9, Contact{CompanyId: 3, Name: "Cliente", Number: "5511888888888", JID: "5511888888888@s.whatsapp.net"}, true)
	if err != nil || reusedID != currentID {
		t.Fatalf("active attendance duplicated: %v", err)
	}
	if _, err = testDB.Exec(`UPDATE conversations SET status='closed' WHERE id=$1`, currentID); err != nil {
		t.Fatal(err)
	}
	nextID, err := checkExistentConversation(9, Contact{CompanyId: 3, Name: "Cliente", Number: "5511888888888", JID: "5511888888888@s.whatsapp.net"}, true)
	if err != nil || nextID == currentID {
		t.Fatalf("incoming message reused a closed attendance: %v", err)
	}
	for _, scenario := range []struct {
		name   string
		status string
		owner  any
		admin  bool
		denied bool
	}{
		{name: "unassigned", status: "open"},
		{name: "legacy zero owner", status: "open", owner: 0},
		{name: "queued", status: "pending"},
		{name: "own queued attendance", status: "pending", owner: 8},
		{name: "own active attendance", status: "open", owner: 8},
		{name: "automation", status: "running"},
		{name: "administrator takes attendance", status: "open", owner: 99, admin: true},
		{name: "other agent protected", status: "open", owner: 99, denied: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if _, err := testDB.Exec(`UPDATE conversations SET status=$1,"agentId"=$2,"departmentId"=5 WHERE id=$3`, scenario.status, scenario.owner, nextID); err != nil {
				t.Fatal(err)
			}
			if _, err := testDB.Exec(`DELETE FROM flow_executions`); err != nil {
				t.Fatal(err)
			}
			if _, err := testDB.Exec(`INSERT INTO flow_executions("conversationId",status) VALUES($1,'waiting')`, nextID); err != nil {
				t.Fatal(err)
			}
			initiator := Agent{Id: 8, CompanyId: 3}
			if scenario.admin {
				initiator.Role = RoleAdmin
			}
			started, err := StartOrReuseConversation(initiator, 7, 9)
			if scenario.denied {
				if !errors.Is(err, ErrConversationUnavailable) {
					t.Fatalf("expected protected ownership, got %v", err)
				}
				return
			}
			if err != nil || !started.Reused {
				t.Fatalf("could not reuse attendance: %v", err)
			}
			var actualID, owner int
			var status, flowStatus string
			var department sql.NullInt64
			if err := testDB.QueryRow(`SELECT id,status,"agentId","departmentId" FROM conversations WHERE url=$1`, started.URL).Scan(&actualID, &status, &owner, &department); err != nil {
				t.Fatal(err)
			}
			if actualID != nextID || status != "open" || owner != initiator.Id || department.Valid {
				t.Fatalf("attendance not assigned to initiator: id=%d status=%s owner=%d department=%v", actualID, status, owner, department)
			}
			if err := testDB.QueryRow(`SELECT status FROM flow_executions WHERE "conversationId"=$1`, nextID).Scan(&flowStatus); err != nil || flowStatus != "failed" {
				t.Fatalf("automation still active: status=%s error=%v", flowStatus, err)
			}
		})
	}
	t.Log("PASS: distinct attendance IDs, preserved closed metadata, current/contact transcript scopes, active reuse and a new attendance after closure")
}

package repo

import (
	"database/sql"
	"os"
	"testing"
	"time"
)

// ZUBLY_TEST_POSTGRES_URL points at a disposable database (e.g. a local PGlite
// socket). Every table here is TEMP, so nothing persists on the server.
func companyAuditTestDB(t *testing.T) {
	t.Helper()
	dsn := os.Getenv("ZUBLY_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set ZUBLY_TEST_POSTGRES_URL for company audit query tests")
	}
	testDB, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	testDB.SetMaxOpenConns(1)
	testDB.SetMaxIdleConns(1)
	previous := db
	db = testDB
	t.Cleanup(func() { db = previous; testDB.Close() })
	for _, statement := range []string{
		// pg_temp qualifies the drop so a real table can never be touched.
		`DROP TABLE IF EXISTS pg_temp.agents, pg_temp.contacts, pg_temp.connections, pg_temp.audit_events`,
		`CREATE TEMP TABLE agents(id integer PRIMARY KEY,"companyId" integer,name text,email text)`,
		`CREATE TEMP TABLE contacts(id integer PRIMARY KEY,"companyId" integer,name text)`,
		`CREATE TEMP TABLE connections(id integer PRIMARY KEY,"companyId" integer,name text)`,
		`CREATE TEMP TABLE audit_events(id bigserial PRIMARY KEY,"companyId" integer,"actorId" integer,"actorEmail" text NOT NULL DEFAULT '',
			"actorType" text NOT NULL,action text NOT NULL,"targetType" text NOT NULL DEFAULT '',"targetId" text NOT NULL DEFAULT '',
			"targetName" text NOT NULL DEFAULT '',"ipAddress" text NOT NULL DEFAULT '',"createdAt" timestamptz NOT NULL DEFAULT now())`,
		`INSERT INTO agents VALUES(7,1,'Ana','ana@empresa.com'),(8,1,'Bia','bia@empresa.com'),(9,2,'Caio','caio@outra.com')`,
		`INSERT INTO connections VALUES(30,1,'Comercial')`,
		`INSERT INTO audit_events("companyId","actorId","actorEmail","actorType",action,"targetType","targetId","targetName","ipAddress","createdAt") VALUES
			(1,7,'ana@empresa.com','agent','login','session','','','10.0.0.1','2026-10-01T10:00:00Z'),
			(1,7,'ana@empresa.com','agent','login_failed','session','','','203.0.113.9','2026-10-02T10:00:00Z'),
			(1,7,'ana@empresa.com','agent','update','agent','8','Bia antiga','10.0.0.1','2026-10-03T10:00:00Z'),
			(1,7,'ana@empresa.com','agent','delete','contact','50','Cliente removido','10.0.0.1','2026-10-04T10:00:00Z'),
			(1,7,'ana@empresa.com','agent','reconnect','instance','30','','10.0.0.1','2026-10-05T10:00:00Z'),
			(1,1,'staff@plataforma.com','platform_support','support_start','support_session','abc','','198.51.100.1','2026-10-06T10:00:00Z'),
			(2,9,'caio@outra.com','agent','login','session','','','10.0.0.2','2026-10-06T11:00:00Z')`,
	} {
		if _, err := testDB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCompanyAuditEventsAreScopedNamedAndMasked(t *testing.T) {
	companyAuditTestDB(t)
	page, err := ListCompanyAuditEvents(1, CompanyAuditFilter{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 6 || len(page.Data) != 6 {
		t.Fatalf("expected only company 1 events, got total=%d rows=%d", page.Total, len(page.Data))
	}
	byAction := map[string]CompanyAuditEvent{}
	for _, event := range page.Data {
		byAction[event.Action] = event
	}
	if got := byAction["update"].TargetName; got != "Bia" {
		t.Fatalf("live target name should win over the stored one, got %q", got)
	}
	if got := byAction["delete"].TargetName; got != "Cliente removido" {
		t.Fatalf("deleted target should keep its stored name, got %q", got)
	}
	if got := byAction["reconnect"].TargetName; got != "Comercial" {
		t.Fatalf("instance target name not resolved, got %q", got)
	}
	if got := byAction["login"].ActorName; got != "Ana" {
		t.Fatalf("actor name not resolved, got %q", got)
	}
	support := byAction["support_start"]
	if support.ActorEmail != "" || support.ActorName != "Suporte da plataforma" {
		t.Fatalf("platform staff identity leaked: %+v", support)
	}
	if page.Data[0].Action != "support_start" {
		t.Fatalf("events must be newest first, got %s", page.Data[0].Action)
	}
}

func TestCompanyAuditFilters(t *testing.T) {
	companyAuditTestDB(t)
	from := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		filter CompanyAuditFilter
		want   int
	}{
		{"failed logins", CompanyAuditFilter{Category: "login_failed"}, 1},
		{"logins", CompanyAuditFilter{Category: "login"}, 2},
		{"contacts", CompanyAuditFilter{Category: "contact"}, 1},
		{"support", CompanyAuditFilter{Category: "support"}, 1},
		{"actor by name", CompanyAuditFilter{Actor: "ana"}, 5},
		{"platform email is not searchable", CompanyAuditFilter{Actor: "plataforma"}, 0},
		{"period", CompanyAuditFilter{From: &from, To: &to}, 2},
	}
	for _, tc := range cases {
		page, err := ListCompanyAuditEvents(1, tc.filter, 1)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if page.Total != tc.want || len(page.Data) != tc.want {
			t.Fatalf("%s: want %d, got total=%d rows=%d", tc.name, tc.want, page.Total, len(page.Data))
		}
	}
	if ValidCompanyAuditCategory("drop table") {
		t.Fatal("unknown categories must be rejected")
	}
}

func TestLoginFailuresSinceLastLoginIgnoresOwnRecentTypos(t *testing.T) {
	companyAuditTestDB(t)
	if _, err := db.Exec(`INSERT INTO audit_events("companyId","actorId","actorEmail","actorType",action,"ipAddress","createdAt") VALUES
		(1,7,'ana@empresa.com','agent','login_failed','192.0.2.50',now() - interval '1 minute')`); err != nil {
		t.Fatal(err)
	}
	summary, err := LoginFailuresSinceLastLogin(7, "192.0.2.50")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Count != 1 || summary.LastIP != "203.0.113.9" {
		t.Fatalf("expected only the foreign attempt after the last login, got %+v", summary)
	}
	summary, err = LoginFailuresSinceLastLogin(7, "10.9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Count != 2 || summary.LastIP != "192.0.2.50" {
		t.Fatalf("recent attempt from another IP must count, got %+v", summary)
	}
	if _, err := db.Exec(`INSERT INTO audit_events("companyId","actorId","actorEmail","actorType",action) VALUES(1,7,'ana@empresa.com','agent','login')`); err != nil {
		t.Fatal(err)
	}
	if summary, err = LoginFailuresSinceLastLogin(7, "10.9.9.9"); err != nil || summary.Count != 0 {
		t.Fatalf("a new login must reset the count, got %+v err=%v", summary, err)
	}
}

func TestAuditTargetNameStaysInsideCompany(t *testing.T) {
	companyAuditTestDB(t)
	if got := AuditTargetName(1, "agent", "8"); got != "Bia" {
		t.Fatalf("got %q", got)
	}
	if got := AuditTargetName(1, "agent", "9"); got != "" {
		t.Fatalf("another company's agent leaked: %q", got)
	}
	if got := AuditTargetName(1, "session", "8"); got != "" {
		t.Fatalf("unknown target type resolved: %q", got)
	}
}

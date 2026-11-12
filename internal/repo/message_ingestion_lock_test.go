package repo

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/joho/godotenv"
)

func TestLockMessageIngestionSerializesSameMessage(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_POSTGRES") == "" {
		t.Skip("set ZUBLY_TEST_POSTGRES to run advisory-lock tests against the workspace database")
	}
	config, err := godotenv.Read("../../.env")
	if err != nil {
		t.Fatal("workspace database configuration required")
	}
	dsn := &url.URL{Scheme: "postgres", Host: config["POSTGRES_HOST"] + ":" + config["POSTGRES_PORT"], Path: "/" + config["POSTGRES_DB"], User: url.UserPassword(config["POSTGRES_USER"], config["POSTGRES_PASSWORD"])}
	dsn.RawQuery = url.Values{"sslmode": {"disable"}}.Encode()
	testDB, err := sql.Open("postgres", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	defer testDB.Close()
	previous := db
	db = testDB
	t.Cleanup(func() { db = previous })

	ctx := context.Background()
	first, err := LockMessageIngestion(ctx, 1, "TEST-MESSAGE")
	if err != nil {
		t.Fatal(err)
	}
	other, err := LockMessageIngestion(ctx, 1, "OTHER-MESSAGE")
	if err != nil {
		t.Fatal("a different message must not wait: ", err)
	}
	other()
	acquired := make(chan func(), 1)
	go func() {
		second, err := LockMessageIngestion(ctx, 1, "TEST-MESSAGE")
		if err != nil {
			t.Error(err)
			close(acquired)
			return
		}
		acquired <- second
	}()
	select {
	case <-acquired:
		t.Fatal("a concurrent delivery of the same message must wait")
	case <-time.After(300 * time.Millisecond):
	}
	first()
	first() // release is idempotent
	select {
	case second := <-acquired:
		if second != nil {
			second()
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second delivery never acquired the lock after release")
	}
	noop, err := LockMessageIngestion(ctx, 1, "  ")
	if err != nil {
		t.Fatal(err)
	}
	noop()
}

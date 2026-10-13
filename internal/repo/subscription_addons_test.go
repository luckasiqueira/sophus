package repo

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"
)

func TestAddonPricingAndProration(t *testing.T) {
	prices := AddonPrices{ExtraConnectionPriceCents: 2500, ExtraAgentPriceCents: 1000, ExtraStorageGBPriceCents: 299, AudioTranscriptionPriceCents: 1490}
	amount, err := addonMonthlyPrice(AddonQuantities{ExtraConnections: 2, ExtraAgents: 3, ExtraStorageGB: 10, AudioTranscription: true}, prices)
	if err != nil || amount != 12480 {
		t.Fatalf("monthly amount=%d error=%v", amount, err)
	}
	annual, err := addonCyclePrice(amount, BillingCycleAnnual)
	if err != nil || annual != 149760 {
		t.Fatalf("annual amount=%d error=%v", annual, err)
	}
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if got := addonProratedCharge(1000, now.Add(15*24*time.Hour), now, 30); got != 500 {
		t.Fatalf("prorated amount=%d", got)
	}
	if got := addonProratedCharge(1, now.Add(time.Second), now, 30); got != 1 {
		t.Fatal("partial cent rounded down")
	}
	if got := addonProratedCharge(1000, now.Add(40*24*time.Hour), now, 30); got != 1000 {
		t.Fatal("remaining period exceeded full-cycle price")
	}
	if _, err := addonMonthlyPrice(AddonQuantities{ExtraAgents: -1}, prices); !errors.Is(err, ErrInvalidAddons) {
		t.Fatal("negative quantity accepted")
	}
	if _, err := addonMonthlyPrice(AddonQuantities{ExtraAgents: 10000}, AddonPrices{ExtraAgentPriceCents: 9007199254740991}); !errors.Is(err, ErrInvalidAddons) {
		t.Fatal("overflowing quantity accepted")
	}
	retained := retainedAddonPrices(AddonQuantities{ExtraAgents: 1}, AddonQuantities{ExtraAgents: 2}, prices, AddonPrices{ExtraAgentPriceCents: 2000})
	if retained.ExtraAgentPriceCents != 1000 {
		t.Fatal("contracted price changed silently")
	}
}

func TestSubscriptionAddonsPostgresLifecycle(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_POSTGRES") == "" {
		t.Skip("set ZUBLY_TEST_POSTGRES for isolated-schema financial lifecycle tests")
	}
	config, err := godotenv.Read("../../.env")
	if err != nil {
		t.Fatal("configured PostgreSQL test requires the workspace environment file")
	}
	dsn := &url.URL{Scheme: "postgres", Host: config["POSTGRES_HOST"] + ":" + config["POSTGRES_PORT"], Path: "/" + config["POSTGRES_DB"], User: url.UserPassword(config["POSTGRES_USER"], config["POSTGRES_PASSWORD"])}
	parameters := url.Values{"sslmode": []string{"disable"}, "options": []string{"-csearch_path=pg_temp"}}
	dsn.RawQuery = parameters.Encode()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	testDB, err := sql.Open("postgres", dsn.String())
	if err != nil {
		t.Fatal("cannot open scoped PostgreSQL test connection")
	}
	defer testDB.Close()
	testDB.SetMaxOpenConns(1)
	testDB.SetMaxIdleConns(1)
	previous := db
	db = testDB
	t.Cleanup(func() { db = previous })
	fixture := `
CREATE TABLE companies(id integer PRIMARY KEY,"isActive" boolean DEFAULT true);
CREATE TABLE plans(id integer PRIMARY KEY,name text,"allowAddons" boolean,"agentLimit" integer,"connectionLimit" integer,"storageLimitGB" integer);
CREATE TABLE subscriptions(id integer PRIMARY KEY,"companyId" integer,"planId" integer,status text,"priceCents" bigint,"durationDays" integer,"billingCycle" text,"startsAt" timestamptz,"currentPeriodStart" timestamptz,"currentPeriodEnd" timestamptz,"basePeriodEnd" timestamptz,"periodRevision" integer DEFAULT 0,"createdAt" timestamptz DEFAULT now(),"updatedAt" timestamptz DEFAULT now());
CREATE TABLE platform_settings(id integer PRIMARY KEY,"extraConnectionPriceCents" bigint,"extraAgentPriceCents" bigint,"extraStorageGBPriceCents" bigint,"audioTranscriptionPriceCents" bigint);
CREATE TABLE agents(id serial PRIMARY KEY,"companyId" integer);CREATE TABLE connections(id serial PRIMARY KEY,"companyId" integer);
CREATE TABLE company_storage_usage("companyId" integer PRIMARY KEY,"usedBytes" bigint DEFAULT 0,"reservedBytes" bigint DEFAULT 0);
CREATE TABLE payments(id serial PRIMARY KEY,"companyId" integer,"subscriptionId" integer,status text,"amountCents" bigint,"dueDate" timestamptz,provider text,"providerPaymentId" text,"providerPreferenceId" text,"checkoutURL" text,"receiptURL" text,"checkoutKey" text,"paidAt" timestamptz,"createdAt" timestamptz DEFAULT now(),"updatedAt" timestamptz DEFAULT now(),"ignoredAt" timestamptz,"manualConfirmedAt" timestamptz,"creditAppliedCents" bigint DEFAULT 0,"entitlementRevision" integer DEFAULT 0,"checkoutCreating" boolean DEFAULT false);
CREATE TABLE subscription_plan_changes("paymentId" integer,status text,"updatedAt" timestamptz);
CREATE TABLE company_credit_balances("companyId" integer PRIMARY KEY,"balanceCents" bigint DEFAULT 0,"updatedAt" timestamptz DEFAULT now());
CREATE TABLE company_credit_entries(id bigserial PRIMARY KEY,"companyId" integer,"sourcePaymentId" integer,"paymentId" integer,"providerTransactionId" text,kind text,"amountCents" bigint,"createdAt" timestamptz DEFAULT now());
INSERT INTO companies VALUES(1,true),(2,true);INSERT INTO plans VALUES(1,'Com adicionais',true,2,1,1),(2,'Sem adicionais',false,2,1,1);
INSERT INTO subscriptions(id,"companyId","planId",status,"priceCents","durationDays","billingCycle","startsAt","currentPeriodStart","currentPeriodEnd","basePeriodEnd") VALUES(1,1,1,'active',10000,30,'monthly',now()-interval '15 days',now()-interval '15 days',now()+interval '15 days',now()+interval '15 days'),(2,2,2,'active',10000,30,'monthly',now(),now(),now()+interval '30 days',now()+interval '30 days');
INSERT INTO platform_settings VALUES(1,2000,1000,500,1490);INSERT INTO agents("companyId") VALUES(1),(1);INSERT INTO connections("companyId") VALUES(1);INSERT INTO company_storage_usage VALUES(1,900000000,0);
`
	if _, err = testDB.ExecContext(ctx, strings.ReplaceAll(fixture, "CREATE TABLE", "CREATE TEMP TABLE")); err != nil {
		t.Fatal(err)
	}
	migration, err := migrations.ReadFile("migrations/78__create_subscription_addons.sql")
	if err != nil {
		t.Fatal(err)
	}
	migrationSQL := strings.Split(string(migration), "-- +goose Down")[0]
	migrationSQL = strings.ReplaceAll(migrationSQL, "CREATE TABLE", "CREATE TEMP TABLE")
	migrationSQL = strings.ReplaceAll(migrationSQL, "CREATE VIEW", "CREATE TEMP VIEW")
	migrationSQL = strings.ReplaceAll(migrationSQL, "FUNCTION cancel_pending_addon_change", "FUNCTION pg_temp.cancel_pending_addon_change")
	if _, err = testDB.ExecContext(ctx, migrationSQL); err != nil {
		t.Fatal(err)
	}
	if _, err = RequestCompanyAddonChange(2, AddonQuantities{ExtraAgents: 1}, PaymentProviderMercadoPago); !errors.Is(err, ErrAddonsNotAllowed) {
		t.Fatalf("plan restriction bypassed: %v", err)
	}
	before, err := GetCompanyAddons(1)
	if err != nil {
		t.Fatal(err)
	}
	requested := AddonQuantities{ExtraAgents: 1, ExtraConnections: 1, ExtraStorageGB: 2, AudioTranscription: true}
	payment, err := RequestCompanyAddonChange(1, requested, PaymentProviderMercadoPago)
	if err != nil {
		t.Fatal(err)
	}
	if payment.AmountCents < 2744 || payment.AmountCents > 2745 {
		t.Fatalf("unexpected prorated invoice: %d", payment.AmountCents)
	}
	pending, err := GetCompanyAddons(1)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Quantities != (AddonQuantities{}) || pending.PendingPaymentID == nil {
		t.Fatal("limits activated before payment")
	}
	retried, err := RequestCompanyAddonChange(1, requested, PaymentProviderMercadoPago)
	if err != nil || retried.Id != payment.Id {
		t.Fatalf("retry duplicated add-on invoice: %v", err)
	}
	if _, err = RequestCompanyAddonChange(1, AddonQuantities{ExtraAgents: 2}, PaymentProviderMercadoPago); !errors.Is(err, ErrOpenPayments) {
		t.Fatal("different pending change bypassed invoice lock")
	}
	apply := func(status string) {
		t.Helper()
		tx, err := testDB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		if _, _, err = applyProviderPayment(tx, payment.Id, payment.Provider, "provider-addon", status, &now, nil); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	apply(PaymentStatusPaid)
	apply(PaymentStatusPaid)
	active, err := GetCompanyAddons(1)
	if err != nil {
		t.Fatal(err)
	}
	if active.Quantities != requested || active.Limits.AgentLimit != 3 || active.Limits.ConnectionLimit != 2 || active.Limits.StorageLimitGB != 3 || !active.Limits.AudioTranscription || active.TotalPriceCents != 15490 {
		t.Fatalf("paid add-on contract not applied: %+v", active)
	}
	if !active.CurrentPeriodEnd.Equal(before.CurrentPeriodEnd) {
		t.Fatal("add-on invoice extended the subscription period")
	}
	if _, err = testDB.Exec(`INSERT INTO agents("companyId") VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = RequestCompanyAddonChange(1, AddonQuantities{}, PaymentProviderMercadoPago); !errors.Is(err, ErrPlanLimitBelowUsage) {
		t.Fatalf("reduction below current usage accepted: %v", err)
	}
	apply(PaymentStatusRefunded)
	restored, err := GetCompanyAddons(1)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Quantities != (AddonQuantities{}) || restored.TotalPriceCents != 10000 {
		t.Fatal("refund did not restore previous contract")
	}
	creditPayment, err := RequestCompanyAddonChange(1, AddonQuantities{ExtraAgents: 1}, PaymentProviderMercadoPago)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = testDB.Exec(`INSERT INTO company_credit_balances ("companyId","balanceCents") VALUES(1,$1)`, creditPayment.AmountCents); err != nil {
		t.Fatal(err)
	}
	settled, err := ApplyCompanyCreditsToPayment(1, creditPayment.Id)
	if err != nil || settled.Status != PaymentStatusPaid {
		t.Fatalf("credit settlement failed: %v", err)
	}
	creditedState, err := GetCompanyAddons(1)
	if err != nil || creditedState.Quantities.ExtraAgents != 1 || !creditedState.CurrentPeriodEnd.Equal(before.CurrentPeriodEnd) {
		t.Fatal("credits did not apply add-ons without extending the period")
	}
	t.Log("PASS: plan eligibility, prorated checkout, pending limits, payment idempotency, unchanged subscription dates, effective quotas, usage-safe reductions and refunds")
}

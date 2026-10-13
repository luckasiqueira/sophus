package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"time"
)

var (
	ErrAddonsNotAllowed = errors.New("o plano atual não permite adicionais")
	ErrInvalidAddons    = errors.New("quantidades ou valores de adicionais inválidos")
	ErrAddonNoChange    = errors.New("os adicionais selecionados já estão contratados")
)

type AddonQuantities struct {
	ExtraConnections   int  `json:"extraConnections"`
	ExtraAgents        int  `json:"extraAgents"`
	ExtraStorageGB     int  `json:"extraStorageGB"`
	AudioTranscription bool `json:"audioTranscription"`
}
type EffectiveLimits struct {
	AgentLimit         int  `json:"agentLimit"`
	ConnectionLimit    int  `json:"connectionLimit"`
	StorageLimitGB     int  `json:"storageLimitGB"`
	AudioTranscription bool `json:"audioTranscription"`
}
type CompanyAddons struct {
	CompanyID         int             `json:"companyId"`
	SubscriptionID    int             `json:"subscriptionId"`
	PlanID            int             `json:"planId"`
	PlanName          string          `json:"planName"`
	Allowed           bool            `json:"allowed"`
	BillingCycle      string          `json:"billingCycle"`
	CurrentPeriodEnd  time.Time       `json:"currentPeriodEnd"`
	Quantities        AddonQuantities `json:"quantities"`
	Prices            AddonPrices     `json:"prices"`
	ContractPrices    AddonPrices     `json:"contractPrices"`
	BasePriceCents    int64           `json:"basePriceCents"`
	MonthlyAddonCents int64           `json:"monthlyAddonCents"`
	TotalPriceCents   int64           `json:"totalPriceCents"`
	Limits            EffectiveLimits `json:"limits"`
	Revision          int             `json:"revision"`
	PendingPaymentID  *int            `json:"pendingPaymentId"`
	AgentCount        int             `json:"agentCount"`
	ConnectionCount   int             `json:"connectionCount"`
	StorageBytes      int64           `json:"storageBytes"`
}

func (q AddonQuantities) Validate() error {
	if q.ExtraConnections < 0 || q.ExtraConnections > 10000 || q.ExtraAgents < 0 || q.ExtraAgents > 10000 || q.ExtraStorageGB < 0 || q.ExtraStorageGB > 100000 {
		return ErrInvalidAddons
	}
	return nil
}
func addonMonthlyPrice(q AddonQuantities, p AddonPrices) (int64, error) {
	if q.Validate() != nil || p.Validate() != nil {
		return 0, ErrInvalidAddons
	}
	total := int64(0)
	quantities := []int64{int64(q.ExtraConnections), int64(q.ExtraAgents), int64(q.ExtraStorageGB), 0}
	if q.AudioTranscription {
		quantities[3] = 1
	}
	for i, price := range []int64{p.ExtraConnectionPriceCents, p.ExtraAgentPriceCents, p.ExtraStorageGBPriceCents, p.AudioTranscriptionPriceCents} {
		if price > 0 && quantities[i] > (9007199254740991-total)/price {
			return 0, ErrInvalidAddons
		}
		total += price * quantities[i]
	}
	return total, nil
}
func addonCyclePrice(monthly int64, cycle string) (int64, error) {
	factor := int64(1)
	if cycle == BillingCycleAnnual {
		factor = 12
	} else if cycle != BillingCycleMonthly {
		return 0, ErrInvalidBillingCycle
	}
	if monthly > 9007199254740991/factor {
		return 0, ErrInvalidAddons
	}
	return monthly * factor, nil
}
func addonProratedCharge(delta int64, end, now time.Time, days int) int64 {
	if delta <= 0 || days <= 0 || !end.After(now) {
		return 0
	}
	remaining := int64(end.Sub(now))
	full := int64(time.Duration(days) * 24 * time.Hour)
	if remaining > full {
		remaining = full
	}
	numerator := new(big.Int).Mul(big.NewInt(delta), big.NewInt(remaining))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, big.NewInt(full), remainder)
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient.Int64()
}

func CompanyAudioTranscriptionEnabled(companyID int) (bool, error) {
	var enabled bool
	err := db.QueryRow(`SELECT COALESCE(bool_or(limits."audioTranscription"),false) FROM subscription_effective_limits limits JOIN subscriptions s ON s.id=limits."subscriptionId" WHERE s."companyId"=$1 AND s.status IN ('trialing','active','past_due')`, companyID).Scan(&enabled)
	return enabled, err
}
func retainedAddonPrices(current, target AddonQuantities, contract, catalog AddonPrices) AddonPrices {
	if current.ExtraConnections > 0 && target.ExtraConnections > 0 {
		catalog.ExtraConnectionPriceCents = contract.ExtraConnectionPriceCents
	}
	if current.ExtraAgents > 0 && target.ExtraAgents > 0 {
		catalog.ExtraAgentPriceCents = contract.ExtraAgentPriceCents
	}
	if current.ExtraStorageGB > 0 && target.ExtraStorageGB > 0 {
		catalog.ExtraStorageGBPriceCents = contract.ExtraStorageGBPriceCents
	}
	if current.AudioTranscription && target.AudioTranscription {
		catalog.AudioTranscriptionPriceCents = contract.AudioTranscriptionPriceCents
	}
	return catalog
}

const companyAddonStateSQL = `SELECT s.id,s."planId",p.name,p."allowAddons",s."billingCycle",s."currentPeriodEnd",s."priceCents",
	COALESCE(a."basePriceCents",s."priceCents"),COALESCE(a.quantities,'{}'),COALESCE(a.prices,'{}'),COALESCE(a.revision,0),
	l."agentLimit",l."connectionLimit",l."storageLimitGB",l."audioTranscription",
	(SELECT COUNT(*) FROM agents WHERE "companyId"=$1),(SELECT COUNT(*) FROM connections WHERE "companyId"=$1),
	COALESCE((SELECT "usedBytes"+"reservedBytes" FROM company_storage_usage WHERE "companyId"=$1),0),
	(SELECT "paymentId" FROM subscription_addon_changes WHERE "companyId"=$1 AND status='pending')
	FROM subscriptions s JOIN plans p ON p.id=s."planId" JOIN subscription_effective_limits l ON l."subscriptionId"=s.id
	LEFT JOIN subscription_addons a ON a."subscriptionId"=s.id
	WHERE s."companyId"=$1 AND s.status IN ('trialing','active','past_due') ORDER BY s."createdAt" DESC LIMIT 1`

func scanCompanyAddons(scanner rowScanner, companyID int) (CompanyAddons, error) {
	state := CompanyAddons{CompanyID: companyID}
	var q, p []byte
	var pending sql.NullInt64
	err := scanner.Scan(&state.SubscriptionID, &state.PlanID, &state.PlanName, &state.Allowed, &state.BillingCycle, &state.CurrentPeriodEnd, &state.TotalPriceCents, &state.BasePriceCents, &q, &p, &state.Revision, &state.Limits.AgentLimit, &state.Limits.ConnectionLimit, &state.Limits.StorageLimitGB, &state.Limits.AudioTranscription, &state.AgentCount, &state.ConnectionCount, &state.StorageBytes, &pending)
	if err != nil {
		return state, err
	}
	if err = json.Unmarshal(q, &state.Quantities); err != nil {
		return state, err
	}
	if err = json.Unmarshal(p, &state.ContractPrices); err != nil {
		return state, err
	}
	state.MonthlyAddonCents, err = addonMonthlyPrice(state.Quantities, state.ContractPrices)
	if pending.Valid {
		id := int(pending.Int64)
		state.PendingPaymentID = &id
	}
	return state, err
}
func GetCompanyAddons(companyID int) (CompanyAddons, error) {
	state, err := scanCompanyAddons(db.QueryRow(companyAddonStateSQL, companyID), companyID)
	if err != nil {
		return state, err
	}
	state.Prices, err = GetAddonPrices()
	return state, err
}

func RequestCompanyAddonChange(companyID int, target AddonQuantities, provider string) (Payment, error) {
	defer invalidateAccessCache()
	if target.Validate() != nil {
		return Payment{}, ErrInvalidAddons
	}
	if !IsValidPaymentProvider(provider) {
		return Payment{}, ErrInvalidPaymentProvider
	}
	tx, err := db.Begin()
	if err != nil {
		return Payment{}, err
	}
	defer tx.Rollback()
	var company int
	if err = tx.QueryRow(`SELECT id FROM companies WHERE id=$1 FOR UPDATE`, companyID).Scan(&company); err != nil {
		return Payment{}, err
	}
	state, err := scanCompanyAddons(tx.QueryRow(companyAddonStateSQL, companyID), companyID)
	if err != nil {
		return Payment{}, err
	}
	if !state.Allowed {
		return Payment{}, ErrAddonsNotAllowed
	}
	if !state.CurrentPeriodEnd.After(time.Now().UTC()) {
		return Payment{}, ErrInvalidSubscriptionAdjustment
	}
	if state.Quantities == target {
		return Payment{}, ErrAddonNoChange
	}
	if state.PendingPaymentID != nil {
		var pendingJSON []byte
		if err = tx.QueryRow(`SELECT "targetQuantities" FROM subscription_addon_changes WHERE "paymentId"=$1 AND "companyId"=$2 AND status='pending' FOR UPDATE`, *state.PendingPaymentID, companyID).Scan(&pendingJSON); err != nil {
			return Payment{}, err
		}
		var pending AddonQuantities
		if err = json.Unmarshal(pendingJSON, &pending); err != nil {
			return Payment{}, err
		}
		if pending != target {
			return Payment{}, ErrOpenPayments
		}
		payment, err := scanPayment(tx.QueryRow(`SELECT id,"companyId","subscriptionId",status,"amountCents","dueDate",provider,"providerPaymentId","providerPreferenceId","checkoutURL","receiptURL","checkoutKey","paidAt","createdAt","updatedAt" FROM payments WHERE id=$1 AND "companyId"=$2 FOR UPDATE`, *state.PendingPaymentID, companyID))
		if err != nil {
			return Payment{}, err
		}
		return payment, tx.Commit()
	}
	var open bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM payments WHERE "companyId"=$1 AND status IN ('pending','processing'))`, companyID).Scan(&open); err != nil {
		return Payment{}, err
	}
	if open {
		return Payment{}, ErrOpenPayments
	}
	prices, err := scanAddonPrices(tx.QueryRow(`SELECT "extraConnectionPriceCents","extraAgentPriceCents","extraStorageGBPriceCents","audioTranscriptionPriceCents" FROM platform_settings WHERE id=1 FOR SHARE`))
	if err != nil {
		return Payment{}, err
	}
	prices = retainedAddonPrices(state.Quantities, target, state.ContractPrices, prices)
	monthly, err := addonMonthlyPrice(target, prices)
	if err != nil {
		return Payment{}, err
	}
	extra, err := addonCyclePrice(monthly, state.BillingCycle)
	if err != nil || extra > 9007199254740991-state.BasePriceCents {
		return Payment{}, ErrInvalidAddons
	}
	total := state.BasePriceCents + extra
	if state.AgentCount > state.Limits.AgentLimit-state.Quantities.ExtraAgents+target.ExtraAgents || state.ConnectionCount > state.Limits.ConnectionLimit-state.Quantities.ExtraConnections+target.ExtraConnections || state.StorageBytes > int64(state.Limits.StorageLimitGB-state.Quantities.ExtraStorageGB+target.ExtraStorageGB)*bytesPerGB {
		return Payment{}, ErrPlanLimitBelowUsage
	}
	q, _ := json.Marshal(target)
	p, _ := json.Marshal(prices)
	oldQ, _ := json.Marshal(state.Quantities)
	oldP, _ := json.Marshal(state.ContractPrices)
	if _, err = tx.Exec(`INSERT INTO subscription_addons ("subscriptionId","companyId","basePriceCents",quantities,prices) VALUES($1,$2,$3,$4::jsonb,$5::jsonb) ON CONFLICT DO NOTHING`, state.SubscriptionID, companyID, state.BasePriceCents, string(oldQ), string(oldP)); err != nil {
		return Payment{}, err
	}
	days := 30
	if state.BillingCycle == BillingCycleAnnual {
		days = 365
	}
	now := time.Now().UTC()
	charge := addonProratedCharge(total-state.TotalPriceCents, state.CurrentPeriodEnd, now, days)
	if charge == 0 {
		if err = writeSubscriptionAddons(tx, state.SubscriptionID, companyID, target, prices, state.BasePriceCents, total, state.Revision+1); err != nil {
			return Payment{}, err
		}
		return Payment{CompanyId: companyID, SubscriptionId: state.SubscriptionID, Status: PaymentStatusPaid, AmountCents: 0}, tx.Commit()
	}
	payment, err := scanPayment(tx.QueryRow(`INSERT INTO payments ("companyId","subscriptionId",status,"amountCents","dueDate",provider,"entitlementRevision")
		VALUES($1,$2,'pending',$3,$4,$5,(SELECT "periodRevision" FROM subscriptions WHERE id=$2)) RETURNING id,"companyId","subscriptionId",status,"amountCents","dueDate",provider,"providerPaymentId","providerPreferenceId","checkoutURL","receiptURL","checkoutKey","paidAt","createdAt","updatedAt"`, companyID, state.SubscriptionID, charge, now.Add(24*time.Hour), provider))
	if err != nil {
		return Payment{}, err
	}
	_, err = tx.Exec(`INSERT INTO subscription_addon_changes ("paymentId","companyId","subscriptionId","planId","basePriceCents","sourceRevision","sourceQuantities","targetQuantities","sourcePrices","targetPrices","sourcePriceCents","targetPriceCents","serviceEndsAt") VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9::jsonb,$10::jsonb,$11,$12,$13)`, payment.Id, companyID, state.SubscriptionID, state.PlanID, state.BasePriceCents, state.Revision, string(oldQ), string(q), string(oldP), string(p), state.TotalPriceCents, total, state.CurrentPeriodEnd)
	if err != nil {
		return Payment{}, err
	}
	return payment, tx.Commit()
}

func writeSubscriptionAddons(tx *sql.Tx, subscriptionID, companyID int, q AddonQuantities, p AddonPrices, base, total int64, revision int) error {
	quantities, _ := json.Marshal(q)
	prices, _ := json.Marshal(p)
	if _, err := tx.Exec(`INSERT INTO subscription_addons ("subscriptionId","companyId","basePriceCents",quantities,prices,revision) VALUES($1,$2,$3,$4::jsonb,$5::jsonb,$6)
		ON CONFLICT ("subscriptionId") DO UPDATE SET "basePriceCents"=$3,quantities=$4::jsonb,prices=$5::jsonb,revision=$6,"updatedAt"=now()`, subscriptionID, companyID, base, string(quantities), string(prices), revision); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE subscriptions SET "priceCents"=$1,"updatedAt"=now() WHERE id=$2 AND "companyId"=$3`, total, subscriptionID, companyID)
	return err
}

func carrySubscriptionAddons(tx *sql.Tx, source, target, company, plan int, cycle string, total int64) error {
	var allowed bool
	if err := tx.QueryRow(`SELECT "allowAddons" FROM plans WHERE id=$1`, plan).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return nil
	}
	var qJSON, pJSON []byte
	err := tx.QueryRow(`SELECT quantities,prices FROM subscription_addons WHERE "subscriptionId"=$1`, source).Scan(&qJSON, &pJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var q AddonQuantities
	var p AddonPrices
	if err = json.Unmarshal(qJSON, &q); err != nil {
		return err
	}
	if err = json.Unmarshal(pJSON, &p); err != nil {
		return err
	}
	monthly, err := addonMonthlyPrice(q, p)
	if err != nil {
		return err
	}
	extra, err := addonCyclePrice(monthly, cycle)
	if err != nil || extra > total {
		return ErrInvalidAddons
	}
	return writeSubscriptionAddons(tx, target, company, q, p, total-extra, total, 0)
}

// Add-on invoices never extend the subscription period. Payment application is
// serialized with the company and records the revision for idempotent refunds.
func applyAddonPayment(tx *sql.Tx, payment Payment, paid, refunded bool) (bool, error) {
	var company, subscription, plan, revision int
	var base, sourceTotal, targetTotal int64
	var oldQ, newQ, oldP, newP []byte
	var status string
	var expires time.Time
	err := tx.QueryRow(`SELECT "companyId","subscriptionId","planId","sourceRevision","basePriceCents","sourcePriceCents","targetPriceCents","sourceQuantities","targetQuantities","sourcePrices","targetPrices",status,"serviceEndsAt" FROM subscription_addon_changes WHERE "paymentId"=$1 FOR UPDATE`, payment.Id).Scan(&company, &subscription, &plan, &revision, &base, &sourceTotal, &targetTotal, &oldQ, &newQ, &oldP, &newP, &status, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if paid && status == "pending" {
		var currentPlan, currentRevision int
		var allowed bool
		var subStatus string
		if err = tx.QueryRow(`SELECT s."planId",s.status,p."allowAddons",a.revision FROM subscriptions s JOIN plans p ON p.id=s."planId" JOIN subscription_addons a ON a."subscriptionId"=s.id WHERE s.id=$1 FOR UPDATE OF s,a`, subscription).Scan(&currentPlan, &subStatus, &allowed, &currentRevision); err != nil {
			return true, err
		}
		paidAt := time.Now().UTC()
		if payment.PaidAt != nil {
			paidAt = *payment.PaidAt
		}
		if !allowed || currentPlan != plan || currentRevision != revision || subStatus == "canceled" || paidAt.After(expires) {
			if _, err = lockCompanyCredits(tx, company); err != nil {
				return true, err
			}
			var applied int64
			if err = tx.QueryRow(`SELECT "creditAppliedCents" FROM payments WHERE id=$1`, payment.Id).Scan(&applied); err != nil {
				return true, err
			}
			if _, err = addCreditEntry(tx, company, &payment.Id, nil, "credit", "", payment.AmountCents+applied); err != nil {
				return true, err
			}
			_, err = tx.Exec(`UPDATE subscription_addon_changes SET status='credited',"updatedAt"=now() WHERE "paymentId"=$1`, payment.Id)
			return true, err
		}
		var q AddonQuantities
		var p AddonPrices
		if err = json.Unmarshal(newQ, &q); err != nil {
			return true, err
		}
		if err = json.Unmarshal(newP, &p); err != nil {
			return true, err
		}
		if err = writeSubscriptionAddons(tx, subscription, company, q, p, base, targetTotal, revision+1); err != nil {
			return true, err
		}
		_, err = tx.Exec(`UPDATE subscription_addon_changes SET status='applied',"updatedAt"=now() WHERE "paymentId"=$1`, payment.Id)
	}
	if refunded && status == "applied" {
		var currentRevision int
		if err = tx.QueryRow(`SELECT revision FROM subscription_addons WHERE "subscriptionId"=$1 FOR UPDATE`, subscription).Scan(&currentRevision); err != nil {
			return true, err
		}
		if currentRevision == revision+1 {
			var q AddonQuantities
			var p AddonPrices
			_ = json.Unmarshal(oldQ, &q)
			_ = json.Unmarshal(oldP, &p)
			if err = writeSubscriptionAddons(tx, subscription, company, q, p, base, sourceTotal, currentRevision+1); err != nil {
				return true, err
			}
		} else {
			var currentJSON, pricesJSON []byte
			var currentBase int64
			var cycle string
			if err = tx.QueryRow(`SELECT a.quantities,a.prices,a."basePriceCents",s."billingCycle" FROM subscription_addons a JOIN subscriptions s ON s.id=a."subscriptionId" WHERE a."subscriptionId"=$1`, subscription).Scan(&currentJSON, &pricesJSON, &currentBase, &cycle); err != nil {
				return true, err
			}
			var current, old, target AddonQuantities
			var prices AddonPrices
			if err = json.Unmarshal(currentJSON, &current); err != nil {
				return true, err
			}
			if err = json.Unmarshal(oldQ, &old); err != nil {
				return true, err
			}
			if err = json.Unmarshal(newQ, &target); err != nil {
				return true, err
			}
			if err = json.Unmarshal(pricesJSON, &prices); err != nil {
				return true, err
			}
			current.ExtraConnections = max(0, current.ExtraConnections-max(0, target.ExtraConnections-old.ExtraConnections))
			current.ExtraAgents = max(0, current.ExtraAgents-max(0, target.ExtraAgents-old.ExtraAgents))
			current.ExtraStorageGB = max(0, current.ExtraStorageGB-max(0, target.ExtraStorageGB-old.ExtraStorageGB))
			if !old.AudioTranscription && target.AudioTranscription {
				current.AudioTranscription = false
			}
			monthly, priceErr := addonMonthlyPrice(current, prices)
			if priceErr != nil {
				return true, priceErr
			}
			extra, priceErr := addonCyclePrice(monthly, cycle)
			if priceErr != nil {
				return true, priceErr
			}
			if err = writeSubscriptionAddons(tx, subscription, company, current, prices, currentBase, currentBase+extra, currentRevision+1); err != nil {
				return true, err
			}
		}
		_, err = tx.Exec(`UPDATE subscription_addon_changes SET status='refunded',"updatedAt"=now() WHERE "paymentId"=$1`, payment.Id)
	}
	if refunded && status == "credited" {
		var amount int64
		if err = tx.QueryRow(`SELECT COALESCE(SUM("amountCents"),0) FROM company_credit_entries WHERE "sourcePaymentId"=$1 AND kind='credit'`, payment.Id).Scan(&amount); err != nil {
			return true, err
		}
		if amount > 0 {
			if _, err = lockCompanyCredits(tx, company); err != nil {
				return true, err
			}
			if _, err = addCreditEntry(tx, company, &payment.Id, nil, "reversal", "", -amount); err != nil {
				return true, err
			}
		}
	}
	return true, err
}

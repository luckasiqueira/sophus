package repo

import (
	"database/sql"
	"encoding/json"
	"sophus/internal/objectcache"
	"strconv"
	"time"
)

func GetPlatformSettings() (PlatformSettings, error) {
	return scanPlatformSettings(db.QueryRow(`SELECT id, "paymentProvider", "allowCompanyPaymentOverride",
		"defaultTimezone", "subscriptionGraceDays", "createdAt", "updatedAt"
		FROM platform_settings WHERE id = 1`))
}

func UpdatePlatformSettings(input UpdatePlatformSettingsInput) (PlatformSettings, error) {
	defer invalidateSettingsCache()
	if !IsValidPaymentProvider(input.PaymentProvider) {
		return PlatformSettings{}, ErrInvalidPaymentProvider
	}
	if input.SubscriptionGraceDays < 0 || input.SubscriptionGraceDays > 90 {
		return PlatformSettings{}, ErrInvalidSubscriptionGraceDays
	}
	return scanPlatformSettings(db.QueryRow(`UPDATE platform_settings SET
		"paymentProvider" = $1,
		"allowCompanyPaymentOverride" = $2,
		"defaultTimezone" = COALESCE(NULLIF($3, ''), "defaultTimezone"),
		"subscriptionGraceDays" = $4,
		"updatedAt" = now()
		WHERE id = 1
		RETURNING id, "paymentProvider", "allowCompanyPaymentOverride", "defaultTimezone",
			"subscriptionGraceDays", "createdAt", "updatedAt"`, input.PaymentProvider,
		input.AllowCompanyPaymentOverride, input.DefaultTimezone, input.SubscriptionGraceDays))
}

func UpdatePlatformAdminProfile(id int, input UpdatePlatformAdminProfileInput) (PlatformAdmin, error) {
	tx, err := db.Begin()
	if err != nil {
		return PlatformAdmin{}, err
	}
	defer tx.Rollback()
	if err := lockPlatformAdminEmail(tx, input.Email, id); err != nil {
		return PlatformAdmin{}, err
	}
	admin, err := scanPlatformAdmin(tx.QueryRow(`UPDATE platform_admins SET
		name = $1,
		email = $2,
		"sessionVersion" = "sessionVersion" + CASE
			WHEN lower(email) <> lower($2) THEN 1 ELSE 0 END,
		"updatedAt" = now()
		WHERE id = $3
		RETURNING id, name, email, password, "isActive", "sessionVersion", "createdAt", "updatedAt"`,
		input.Name, input.Email, id))
	if err != nil {
		return PlatformAdmin{}, err
	}
	if err := tx.Commit(); err != nil {
		return PlatformAdmin{}, err
	}
	return admin, nil
}

func lockPlatformAdminEmail(tx *sql.Tx, email string, excludeAdminID int) error {
	var lockAcquired interface{}
	if err := tx.QueryRow(`SELECT pg_advisory_xact_lock(hashtextextended(lower($1), 0))`, email).Scan(&lockAcquired); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM agents WHERE lower(email) = lower($1)
		UNION ALL
		SELECT 1 FROM platform_admins WHERE lower(email) = lower($1) AND id <> $2
	)`, email, excludeAdminID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrAgentEmailInUse
	}
	return nil
}

func GetCompanySettings(companyID int) (CompanySettings, error) {
	return scanCompanySettings(db.QueryRow(`SELECT c.id, c.name, c.email, c."isActive", c.timezone,
		c."paymentProviderOverride", c."mediaRetentionDays", c."operationSettings", ps."paymentProvider", ps."allowCompanyPaymentOverride",
		ps."defaultTimezone", ps."subscriptionGraceDays", c."createdAt", c."updatedAt"
		FROM companies c
		CROSS JOIN platform_settings ps
		WHERE c.id = $1 AND ps.id = 1`, companyID))
}

func UpdateCompanySettings(companyID int, input UpdateCompanySettingsInput) (CompanySettings, error) {
	defer invalidateSettingsCache()
	if input.PaymentProviderOverride != nil && !IsValidPaymentProvider(*input.PaymentProviderOverride) {
		return CompanySettings{}, ErrInvalidPaymentProvider
	}
	if !IsValidMediaRetention(input.MediaRetentionDays) {
		return CompanySettings{}, ErrInvalidMediaRetention
	}
	tx, err := db.Begin()
	if err != nil {
		return CompanySettings{}, err
	}
	defer tx.Rollback()

	platform, err := scanPlatformSettings(tx.QueryRow(`SELECT id, "paymentProvider", "allowCompanyPaymentOverride",
		"defaultTimezone", "subscriptionGraceDays", "createdAt", "updatedAt"
		FROM platform_settings WHERE id = 1 FOR SHARE`))
	if err != nil {
		return CompanySettings{}, err
	}
	if input.PaymentProviderOverride != nil && !platform.AllowCompanyPaymentOverride {
		return CompanySettings{}, ErrCompanyPaymentOverrideNotAllowed
	}

	var settings CompanySettings
	var override sql.NullString
	var retention sql.NullInt64
	operations := normalizeCompanyOperationSettings(input.Operations)
	operationsJSON, err := json.Marshal(operations)
	if err != nil {
		return CompanySettings{}, err
	}
	err = tx.QueryRow(`UPDATE companies SET
		name = $1,
		email = $2,
		timezone = COALESCE(NULLIF($3, ''), timezone),
		"paymentProviderOverride" = $4,
		"mediaRetentionDays" = $5,
		"operationSettings" = $6::jsonb,
		"updatedAt" = now()
		WHERE id = $7
		RETURNING id, name, email, "isActive", timezone, "paymentProviderOverride", "mediaRetentionDays", "createdAt", "updatedAt"`,
		input.Name, input.Email, input.Timezone, input.PaymentProviderOverride, input.MediaRetentionDays, string(operationsJSON), companyID).Scan(
		&settings.Id, &settings.Name, &settings.Email, &settings.IsActive, &settings.Timezone,
		&override, &retention, &settings.CreatedAt, &settings.UpdatedAt)
	if err != nil {
		return CompanySettings{}, err
	}
	settings.PaymentProviderOverride = nullStringPointer(override)
	settings.Operations = operations
	if retention.Valid {
		value := int(retention.Int64)
		settings.MediaRetentionDays = &value
	}
	applyPlatformSettings(&settings, platform)

	if err := tx.Commit(); err != nil {
		return CompanySettings{}, err
	}
	return settings, nil
}

func ResolveCompanyPaymentProvider(companyID int) (string, error) {
	settings, err := GetCompanySettings(companyID)
	if err != nil {
		return "", err
	}
	return settings.EffectivePaymentProvider, nil
}

func GetPlanProviderProduct(planID int, provider string, amountCents int64) (PlanProviderProduct, error) {
	if !IsValidPaymentProvider(provider) {
		return PlanProviderProduct{}, ErrInvalidPaymentProvider
	}
	return scanPlanProviderProduct(db.QueryRow(`SELECT "planId", provider, "amountCents", "providerProductId",
		"createdAt", "updatedAt"
		FROM plan_provider_products
		WHERE "planId" = $1 AND provider = $2 AND "amountCents" = $3`, planID, provider, amountCents))
}

func UpsertPlanProviderProduct(input UpsertPlanProviderProductInput) (PlanProviderProduct, error) {
	if !IsValidPaymentProvider(input.Provider) {
		return PlanProviderProduct{}, ErrInvalidPaymentProvider
	}
	return scanPlanProviderProduct(db.QueryRow(`INSERT INTO plan_provider_products
		("planId", provider, "amountCents", "providerProductId")
		VALUES ($1, $2, $3, $4)
		ON CONFLICT ("planId", provider, "amountCents") DO UPDATE SET
			"providerProductId" = EXCLUDED."providerProductId",
			"updatedAt" = now()
		RETURNING "planId", provider, "amountCents", "providerProductId", "createdAt", "updatedAt"`,
		input.PlanId, input.Provider, input.AmountCents, input.ProviderProductId))
}

func scanPlatformSettings(scanner rowScanner) (PlatformSettings, error) {
	var settings PlatformSettings
	err := scanner.Scan(&settings.Id, &settings.PaymentProvider, &settings.AllowCompanyPaymentOverride,
		&settings.DefaultTimezone, &settings.SubscriptionGraceDays, &settings.CreatedAt, &settings.UpdatedAt)
	return settings, err
}

func scanCompanySettings(scanner rowScanner) (CompanySettings, error) {
	var settings CompanySettings
	var override sql.NullString
	var retention sql.NullInt64
	var operationsJSON []byte
	err := scanner.Scan(&settings.Id, &settings.Name, &settings.Email, &settings.IsActive, &settings.Timezone,
		&override, &retention, &operationsJSON, &settings.PaymentProvider, &settings.AllowCompanyPaymentOverride, &settings.DefaultTimezone,
		&settings.SubscriptionGraceDays, &settings.CreatedAt, &settings.UpdatedAt)
	if err != nil {
		return CompanySettings{}, err
	}
	settings.PaymentProviderOverride = nullStringPointer(override)
	_ = json.Unmarshal(operationsJSON, &settings.Operations)
	settings.Operations = normalizeCompanyOperationSettings(settings.Operations)
	if retention.Valid {
		value := int(retention.Int64)
		settings.MediaRetentionDays = &value
	}
	settings.EffectivePaymentProvider = effectivePaymentProvider(settings.PaymentProvider,
		settings.AllowCompanyPaymentOverride, settings.PaymentProviderOverride)
	return settings, nil
}

type cachedOperationSettings struct {
	Settings CompanyOperationSettings `json:"settings"`
	Timezone string                   `json:"timezone"`
}

func GetCompanyOperationSettings(companyID int) (CompanyOperationSettings, error) {
	var cached cachedOperationSettings
	err := objectcache.Get("configuration", "operations-v2:"+strconv.Itoa(companyID), time.Minute, &cached, func() error {
		var err error
		cached.Settings, err = getCompanyOperationSettings(companyID)
		cached.Timezone = cached.Settings.Timezone
		return err
	})
	cached.Settings.Timezone = cached.Timezone
	return cached.Settings, err
}

func getCompanyOperationSettings(companyID int) (CompanyOperationSettings, error) {
	var raw []byte
	var timezone string
	if err := db.QueryRow(`SELECT "operationSettings", timezone FROM companies WHERE id = $1`, companyID).Scan(&raw, &timezone); err != nil {
		return CompanyOperationSettings{}, err
	}
	var settings CompanyOperationSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return CompanyOperationSettings{}, err
	}
	settings = normalizeCompanyOperationSettings(settings)
	settings.Timezone = timezone
	return settings, nil
}

func normalizeCompanyOperationSettings(settings CompanyOperationSettings) CompanyOperationSettings {
	if settings.MessageEditPolicy == "" || !ValidMessageEditPolicy(settings.MessageEditPolicy) {
		settings.MessageEditPolicy = MessageEditingDisabled
	}
	if settings.MessageSignatureTemplate == "" {
		settings.MessageSignatureTemplate = DefaultMessageSignatureTemplate
	}
	if settings.BusinessHoursStart == "" {
		settings.BusinessHoursStart = "08:00"
	}
	if settings.BusinessHoursEnd == "" {
		settings.BusinessHoursEnd = "18:00"
	}
	if len(settings.BusinessHoursDays) == 0 {
		settings.BusinessHoursDays = []int{1, 2, 3, 4, 5}
	}
	if settings.BusinessHoursLunchStart == "" {
		settings.BusinessHoursLunchStart = "12:00"
	}
	if settings.BusinessHoursLunchEnd == "" {
		settings.BusinessHoursLunchEnd = "13:00"
	}
	if settings.AutoAssignStrategy != "random" {
		settings.AutoAssignStrategy = "least_loaded"
	}
	if settings.MaxAttachmentMB <= 0 || settings.MaxAttachmentMB > 50 {
		settings.MaxAttachmentMB = 50
	}
	if len(settings.AllowedAttachmentTypes) == 0 {
		settings.AllowedAttachmentTypes = []string{"image", "video", "audio", "document"}
	}
	return settings
}

func applyPlatformSettings(settings *CompanySettings, platform PlatformSettings) {
	settings.PaymentProvider = platform.PaymentProvider
	settings.AllowCompanyPaymentOverride = platform.AllowCompanyPaymentOverride
	settings.DefaultTimezone = platform.DefaultTimezone
	settings.SubscriptionGraceDays = platform.SubscriptionGraceDays
	settings.EffectivePaymentProvider = effectivePaymentProvider(platform.PaymentProvider,
		platform.AllowCompanyPaymentOverride, settings.PaymentProviderOverride)
}

func effectivePaymentProvider(platformProvider string, allowOverride bool, override *string) string {
	if allowOverride && override != nil && IsValidPaymentProvider(*override) {
		return *override
	}
	return platformProvider
}

func scanPlanProviderProduct(scanner rowScanner) (PlanProviderProduct, error) {
	var product PlanProviderProduct
	err := scanner.Scan(&product.PlanId, &product.Provider, &product.AmountCents, &product.ProviderProductId,
		&product.CreatedAt, &product.UpdatedAt)
	return product, err
}

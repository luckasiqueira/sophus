package repo

import (
	"errors"
	"time"
)

const (
	SubscriptionStatusTrialing = "trialing"
	SubscriptionStatusActive   = "active"
	SubscriptionStatusPastDue  = "past_due"
	SubscriptionStatusCanceled = "canceled"
	SubscriptionStatusExpired  = "expired"
)

const (
	PaymentStatusPending    = "pending"
	PaymentStatusProcessing = "processing"
	PaymentStatusPaid       = "paid"
	PaymentStatusFailed     = "failed"
	PaymentStatusCanceled   = "canceled"
	PaymentStatusRefunded   = "refunded"
)

const (
	PaymentProviderMercadoPago = "mercado_pago"
	PaymentProviderAbacatePay  = "abacate_pay"
)

const (
	BillingCycleMonthly = "monthly"
	BillingCycleAnnual  = "annual"
)

func IsValidBillingCycle(cycle string) bool {
	return cycle == BillingCycleMonthly || cycle == BillingCycleAnnual
}

func IsValidPaymentProvider(provider string) bool {
	return provider == PaymentProviderMercadoPago || provider == PaymentProviderAbacatePay
}

var (
	ErrConnectionLimitReached           = errors.New("plan connection limit reached")
	ErrPlanLimitBelowUsage              = errors.New("plan limit is below current usage")
	ErrInvalidPaymentProvider           = errors.New("invalid payment provider")
	ErrCompanyPaymentOverrideNotAllowed = errors.New("company payment provider override is not allowed")
	ErrInvalidSubscriptionGraceDays     = errors.New("subscription grace days must be between 0 and 90")
	ErrInvalidBillingCycle              = errors.New("invalid billing cycle")
	ErrInvalidSubscriptionDueDate       = errors.New("invalid subscription due date")
)

type PlatformAdmin struct {
	Id             int       `json:"id"`
	Name           string    `json:"name"`
	Email          string    `json:"email"`
	Password       string    `json:"-"`
	IsActive       bool      `json:"isActive"`
	SessionVersion int       `json:"-"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type CreatePlatformAdminInput struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"-"`
}

type UpdatePlatformAdminProfileInput struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	PasswordHash string `json:"-"`
}

type PlatformSettings struct {
	Id                          int       `json:"id"`
	PaymentProvider             string    `json:"paymentProvider"`
	AllowCompanyPaymentOverride bool      `json:"allowCompanyPaymentOverride"`
	DefaultTimezone             string    `json:"defaultTimezone"`
	SubscriptionGraceDays       int       `json:"subscriptionGraceDays"`
	CreatedAt                   time.Time `json:"createdAt"`
	UpdatedAt                   time.Time `json:"updatedAt"`
}

type UpdatePlatformSettingsInput struct {
	PaymentProvider             string `json:"paymentProvider"`
	AllowCompanyPaymentOverride bool   `json:"allowCompanyPaymentOverride"`
	DefaultTimezone             string `json:"defaultTimezone"`
	SubscriptionGraceDays       int    `json:"subscriptionGraceDays"`
}

type Plan struct {
	Id                int       `json:"id"`
	Name              string    `json:"name"`
	Description       string    `json:"description"`
	MonthlyPriceCents int64     `json:"monthlyPriceCents"`
	AnnualPriceCents  int64     `json:"annualPriceCents"`
	AgentLimit        int       `json:"agentLimit"`
	ConnectionLimit   int       `json:"connectionLimit"`
	IsActive          bool      `json:"isActive"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

type PlanInput struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	MonthlyPriceCents int64  `json:"monthlyPriceCents"`
	AnnualPriceCents  int64  `json:"annualPriceCents"`
	AgentLimit        int    `json:"agentLimit"`
	ConnectionLimit   int    `json:"connectionLimit"`
	IsActive          bool   `json:"isActive"`
}

type CreatePlanInput = PlanInput
type UpdatePlanInput = PlanInput

type Company struct {
	Id        int       `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	IsActive  bool      `json:"isActive"`
	Timezone  string    `json:"timezone"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type CompanySummary struct {
	Id                    int        `json:"id"`
	Name                  string     `json:"name"`
	Email                 string     `json:"email"`
	IsActive              bool       `json:"isActive"`
	Timezone              string     `json:"timezone"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
	CurrentSubscriptionId *int       `json:"currentSubscriptionId"`
	CurrentPlanId         *int       `json:"currentPlanId"`
	CurrentPlanName       *string    `json:"currentPlanName"`
	SubscriptionStatus    *string    `json:"subscriptionStatus"`
	PriceCents            *int64     `json:"priceCents"`
	BillingCycle          *string    `json:"billingCycle"`
	CurrentPeriodStart    *time.Time `json:"currentPeriodStart"`
	CurrentPeriodEnd      *time.Time `json:"currentPeriodEnd"`
}

type CreateCompanyInput struct {
	Name              string `json:"name"`
	Email             string `json:"email"`
	Timezone          string `json:"timezone"`
	PlanId            int    `json:"planId"`
	BillingCycle      string `json:"billingCycle"`
	AdminName         string `json:"adminName"`
	AdminEmail        string `json:"adminEmail"`
	AdminPasswordHash string `json:"-"`
}

type CreateCompanyWithAdminInput = CreateCompanyInput

type UpdateCompanyInput struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Timezone string `json:"timezone"`
}

type CompanySettings struct {
	Id                          int       `json:"id"`
	Name                        string    `json:"name"`
	Email                       string    `json:"email"`
	IsActive                    bool      `json:"isActive"`
	Timezone                    string    `json:"timezone"`
	PaymentProviderOverride     *string   `json:"paymentProviderOverride"`
	PaymentProvider             string    `json:"paymentProvider"`
	AllowCompanyPaymentOverride bool      `json:"allowCompanyPaymentOverride"`
	DefaultTimezone             string    `json:"defaultTimezone"`
	SubscriptionGraceDays       int       `json:"subscriptionGraceDays"`
	EffectivePaymentProvider    string    `json:"effectivePaymentProvider"`
	CreatedAt                   time.Time `json:"createdAt"`
	UpdatedAt                   time.Time `json:"updatedAt"`
}

type UpdateCompanySettingsInput struct {
	Name                    string  `json:"name"`
	Email                   string  `json:"email"`
	Timezone                string  `json:"timezone"`
	PaymentProviderOverride *string `json:"paymentProviderOverride"`
}

type PlanProviderProduct struct {
	PlanId            int       `json:"planId"`
	Provider          string    `json:"provider"`
	AmountCents       int64     `json:"amountCents"`
	ProviderProductId string    `json:"providerProductId"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

type PlanProviderProductInput struct {
	PlanId            int    `json:"planId"`
	Provider          string `json:"provider"`
	AmountCents       int64  `json:"amountCents"`
	ProviderProductId string `json:"providerProductId"`
}

type UpsertPlanProviderProductInput = PlanProviderProductInput

type Subscription struct {
	Id                     int        `json:"id"`
	CompanyId              int        `json:"companyId"`
	PlanId                 int        `json:"planId"`
	Status                 string     `json:"status"`
	PriceCents             int64      `json:"priceCents"`
	DurationDays           int        `json:"durationDays"`
	BillingCycle           string     `json:"billingCycle"`
	StartsAt               time.Time  `json:"startsAt"`
	CurrentPeriodStart     time.Time  `json:"currentPeriodStart"`
	CurrentPeriodEnd       time.Time  `json:"currentPeriodEnd"`
	BasePeriodEnd          time.Time  `json:"-"`
	PreviousSubscriptionId *int       `json:"-"`
	CanceledAt             *time.Time `json:"canceledAt"`
	CreatedAt              time.Time  `json:"createdAt"`
	UpdatedAt              time.Time  `json:"updatedAt"`
}

type Payment struct {
	Id                   int        `json:"id"`
	CompanyId            int        `json:"companyId"`
	SubscriptionId       int        `json:"subscriptionId"`
	Status               string     `json:"status"`
	AmountCents          int64      `json:"amountCents"`
	DueDate              time.Time  `json:"dueDate"`
	Provider             string     `json:"provider"`
	ProviderPaymentId    *string    `json:"providerPaymentId"`
	ProviderPreferenceId *string    `json:"providerPreferenceId"`
	CheckoutURL          *string    `json:"checkoutURL"`
	ReceiptURL           *string    `json:"receiptURL"`
	CheckoutKey          *string    `json:"-"`
	PaidAt               *time.Time `json:"paidAt"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}

type rowScanner interface {
	Scan(dest ...interface{}) error
}

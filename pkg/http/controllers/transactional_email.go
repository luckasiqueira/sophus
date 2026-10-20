package controllers

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"sophus/internal/mailer"
	"sophus/internal/repo"
	"sophus/utils/env"
)

// Transactional emails are sent in the background so a slow SMTP server never
// delays a payment webhook or the signup response. Failures are logged and
// never change the outcome of the business operation.

const transactionalEmailTimeout = 30 * time.Second

// brazilZone is UTC-3. Brazil has not observed DST since 2019, so a fixed offset is exact.
var brazilZone = time.FixedZone("BRT", -3*60*60)

type transactionalMail struct {
	sender  mailer.Sender
	enabled bool
}

var transactionalMailer = sync.OnceValue(func() transactionalMail {
	cfg, err := mailer.ConfigFromEnv(env.Backend)
	if err != nil {
		log.Printf("email desativado: configuração SMTP inválida: %v", err)
		return transactionalMail{sender: mailer.NewSender(mailer.Config{})}
	}
	return transactionalMail{sender: mailer.NewSender(cfg), enabled: cfg.Enabled}
})

func init() {
	repo.PaymentPaidHook = onPaymentPaid
}

// onPaymentPaid runs when a payment first becomes paid, whether by a provider
// webhook or by manual confirmation. It sends the payment and access emails.
func onPaymentPaid(payment repo.Payment) {
	if !transactionalMailer().enabled {
		return
	}
	paidAt := time.Now()
	if payment.PaidAt != nil {
		paidAt = *payment.PaidAt
	}
	sendCompanyEmail(payment.CompanyId, mailer.KindPaymentConfirmed, func(data *mailer.Data, subscription repo.Subscription) {
		data.Amount = formatBRL(payment.AmountCents)
		data.PaidAt = formatDateTime(paidAt)
		data.NextDueDate = formatDate(subscription.CurrentPeriodEnd)
		if payment.ReceiptURL != nil {
			data.ReceiptURL = *payment.ReceiptURL
		}
	})
	sendCompanyEmail(payment.CompanyId, mailer.KindAccessReleased, func(data *mailer.Data, subscription repo.Subscription) {
		data.AccessUntil = formatDate(subscription.CurrentPeriodEnd)
	})
}

// sendWelcomeEmail is called once a company and its first admin are created.
func sendWelcomeEmail(companyID int) {
	sendCompanyEmail(companyID, mailer.KindWelcome, func(data *mailer.Data, subscription repo.Subscription) {
		if subscription.Status == "trialing" {
			data.AccessUntil = formatDate(subscription.CurrentPeriodEnd)
		}
	})
}

// sendCompanyEmail loads the company's admin and plan, lets fill add the message-specific
// values, and sends the email in the background. It returns immediately.
func sendCompanyEmail(companyID int, kind mailer.Kind, fill func(data *mailer.Data, subscription repo.Subscription)) {
	mail := transactionalMailer()
	if !mail.enabled {
		return
	}
	go func() {
		company, err := repo.GetCompanyById(companyID)
		if err != nil {
			log.Printf("email %s: empresa %d não encontrada: %v", kind, companyID, err)
			return
		}
		admin, ok, err := companyAdmin(companyID)
		if err != nil || !ok {
			log.Printf("email %s: admin da empresa %d não encontrado: %v", kind, companyID, err)
			return
		}
		subscription, err := repo.GetCurrentSubscription(companyID)
		if err != nil {
			log.Printf("email %s: assinatura da empresa %d indisponível: %v", kind, companyID, err)
			return
		}
		plan, err := repo.GetPlanById(subscription.PlanId)
		if err != nil {
			log.Printf("email %s: plano da empresa %d indisponível: %v", kind, companyID, err)
			return
		}

		data := mailer.Data{
			Name:         admin.Name,
			CompanyName:  company.Name,
			PlanName:     plan.Name,
			LoginURL:     appLoginURL(),
			SupportEmail: supportEmail(),
		}
		fill(&data, subscription)

		rendered, err := mailer.Render(kind, mailer.DefaultVariant, data)
		if err != nil {
			log.Printf("email %s: falha ao montar a mensagem: %v", kind, err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), transactionalEmailTimeout)
		defer cancel()
		err = mail.sender.Send(ctx, mailer.Message{To: admin.Email, Subject: rendered.Subject, HTML: rendered.HTML, Text: rendered.Text})
		if err != nil {
			log.Printf("email %s da empresa %d não enviado: %v", kind, companyID, err)
		}
	}()
}

// companyAdmin picks the original admin when active, otherwise any active admin.
func companyAdmin(companyID int) (repo.Agent, bool, error) {
	agents, err := repo.ListAgentsByCompany(companyID)
	if err != nil {
		return repo.Agent{}, false, err
	}
	var chosen repo.Agent
	found := false
	for _, agent := range agents {
		if !agent.IsAdmin() || !agent.IsActive {
			continue
		}
		if agent.IsOriginalAdmin {
			return agent, true, nil
		}
		if !found {
			chosen, found = agent, true
		}
	}
	return chosen, found, nil
}

func appLoginURL() string {
	base := strings.TrimSpace(env.Backend["APP_DOMAIN"])
	if base == "" {
		return ""
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/") + "/login"
}

func supportEmail() string {
	if value := strings.TrimSpace(env.Backend["SUPPORT_EMAIL"]); value != "" {
		return value
	}
	return strings.TrimSpace(env.Backend["SMTP_FROM_EMAIL"])
}

// formatBRL renders cents as "R$ 1.234,56".
func formatBRL(cents int64) string {
	whole := strconv.FormatInt(cents/100, 10)
	var groups []string
	for len(whole) > 3 {
		groups = append([]string{whole[len(whole)-3:]}, groups...)
		whole = whole[:len(whole)-3]
	}
	groups = append([]string{whole}, groups...)
	return fmt.Sprintf("R$ %s,%02d", strings.Join(groups, "."), cents%100)
}

func formatDate(value time.Time) string {
	return value.In(brazilZone).Format("02/01/2006")
}

func formatDateTime(value time.Time) string {
	return value.In(brazilZone).Format("02/01/2006 15:04")
}

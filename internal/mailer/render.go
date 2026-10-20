package mailer

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	texttemplate "text/template"
)

//go:embed templates
var templateFS embed.FS

// Kind identifies a transactional email.
type Kind string

const (
	KindWelcome          Kind = "welcome"
	KindPaymentConfirmed Kind = "payment_confirmed"
	KindAccessReleased   Kind = "access_released"
)

// Variant is the visual layout a Kind is rendered with.
type Variant string

const (
	VariantClassic Variant = "classic"
	VariantBanner  Variant = "banner"
	VariantDark    Variant = "dark"
)

// DefaultVariant is the layout used when sending. Chosen by the product owner for all messages.
const DefaultVariant = VariantClassic

// Kinds and Variants list every option, in the order used for previews.
var (
	Kinds    = []Kind{KindWelcome, KindPaymentConfirmed, KindAccessReleased}
	Variants = []Variant{VariantClassic, VariantBanner, VariantDark}
)

var subjects = map[Kind]string{
	KindWelcome:          "Bem-vindo ao Sophus",
	KindPaymentConfirmed: "Pagamento confirmado",
	KindAccessReleased:   "Seu acesso ao Sophus está liberado",
}

// header holds the per-message banner text each layout shows above the body.
type header struct {
	Hero    string
	Kicker  string
	Status  string
	Edition string
}

var headers = map[Kind]header{
	KindWelcome:          {Hero: "Bem-vindo ao Sophus", Kicker: "Conta criada", Status: "Conta ativa", Edition: "Boas-vindas"},
	KindPaymentConfirmed: {Hero: "Pagamento confirmado", Kicker: "Recibo", Status: "Pago", Edition: "Comprovante de pagamento"},
	KindAccessReleased:   {Hero: "Seu acesso está liberado", Kicker: "Acesso", Status: "Liberado", Edition: "Acesso liberado"},
}

// Data is the set of values any template may use. Empty fields are hidden by the templates.
type Data struct {
	Name         string
	CompanyName  string
	PlanName     string
	Amount       string
	PaidAt       string
	NextDueDate  string
	AccessUntil  string
	LoginURL     string
	ReceiptURL   string
	SupportEmail string
}

// Rendered is a finished email, ready to hand to a Sender.
type Rendered struct {
	Subject string
	HTML    string
	Text    string
}

// Render builds the subject, HTML and plain-text bodies for kind using variant.
func Render(kind Kind, variant Variant, data Data) (Rendered, error) {
	subject, ok := subjects[kind]
	if !ok {
		return Rendered{}, fmt.Errorf("modelo de email desconhecido: %s", kind)
	}

	body, err := renderBody(kind, data)
	if err != nil {
		return Rendered{}, err
	}
	layout, err := template.ParseFS(templateFS, "templates/layouts/"+string(variant)+".html")
	if err != nil {
		return Rendered{}, fmt.Errorf("layout de email %q: %w", variant, err)
	}
	head := headers[kind]
	var html bytes.Buffer
	err = layout.Execute(&html, layoutData{
		Title:     subject,
		HeroTitle: head.Hero,
		Kicker:    head.Kicker,
		Status:    head.Status,
		Edition:   head.Edition,
		Content:   template.HTML(body),
	})
	if err != nil {
		return Rendered{}, err
	}

	textBody, err := renderText(kind, data)
	if err != nil {
		return Rendered{}, err
	}
	return Rendered{Subject: subject, HTML: html.String(), Text: textBody}, nil
}

type layoutData struct {
	Title     string
	HeroTitle string
	Kicker    string
	Status    string
	Edition   string
	Content   template.HTML
}

func renderBody(kind Kind, data Data) (string, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/bodies/"+string(kind)+".html")
	if err != nil {
		return "", fmt.Errorf("corpo do email %q: %w", kind, err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}

func renderText(kind Kind, data Data) (string, error) {
	tmpl, err := texttemplate.ParseFS(templateFS, "templates/text/"+string(kind)+".txt")
	if err != nil {
		return "", fmt.Errorf("texto do email %q: %w", kind, err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}

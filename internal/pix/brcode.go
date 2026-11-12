// Package pix builds static Pix charges: the "copia e cola" BR Code (EMV
// payload defined by the Banco Central do Brasil) and its QR Code image.
package pix

import (
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"unicode"

	"github.com/skip2/go-qrcode"
	"golang.org/x/text/unicode/norm"
)

// Key types accepted for a Pix key ("chave").
const (
	KeyCPF    = "cpf"
	KeyCNPJ   = "cnpj"
	KeyPhone  = "phone"
	KeyEmail  = "email"
	KeyRandom = "random"
)

const (
	maxNameLength        = 25
	maxCityLength        = 15
	maxDescriptionLength = 40
	MaxAmount            = 1_000_000
)

var (
	digitsOnly    = regexp.MustCompile(`\D`)
	randomKeyForm = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// Charge is one static Pix charge. Amount 0 lets the payer type the value.
type Charge struct {
	KeyType     string
	Key         string
	Name        string
	City        string
	Amount      float64
	Description string
}

// NormalizeKey validates a Pix key and returns it in the format the BR Code
// expects: digits for CPF and CNPJ, +55 and digits for phones, lowercase
// e-mail and the random key (EVP) as given.
func NormalizeKey(keyType, key string) (string, error) {
	key = strings.TrimSpace(key)
	switch keyType {
	case KeyCPF:
		digits := digitsOnly.ReplaceAllString(key, "")
		if len(digits) != 11 || !validCPF(digits) {
			return "", errors.New("CPF da chave Pix inválido")
		}
		return digits, nil
	case KeyCNPJ:
		digits := digitsOnly.ReplaceAllString(key, "")
		if len(digits) != 14 || !validCNPJ(digits) {
			return "", errors.New("CNPJ da chave Pix inválido")
		}
		return digits, nil
	case KeyPhone:
		digits := digitsOnly.ReplaceAllString(key, "")
		if strings.HasPrefix(digits, "55") && (len(digits) == 12 || len(digits) == 13) {
			digits = digits[2:]
		}
		if len(digits) != 10 && len(digits) != 11 {
			return "", errors.New("telefone da chave Pix deve ter DDD e número")
		}
		return "+55" + digits, nil
	case KeyEmail:
		address, err := mail.ParseAddress(key)
		if err != nil || address.Address != key || len(key) > 77 {
			return "", errors.New("e-mail da chave Pix inválido")
		}
		return strings.ToLower(key), nil
	case KeyRandom:
		if !randomKeyForm.MatchString(key) {
			return "", errors.New("chave aleatória do Pix inválida")
		}
		return strings.ToLower(key), nil
	}
	return "", errors.New("tipo de chave Pix inválido")
}

func validCPF(digits string) bool {
	if strings.Count(digits, digits[:1]) == len(digits) {
		return false
	}
	for _, length := range []int{9, 10} {
		sum := 0
		for index := 0; index < length; index++ {
			sum += int(digits[index]-'0') * (length + 1 - index)
		}
		check := sum * 10 % 11 % 10
		if check != int(digits[length]-'0') {
			return false
		}
	}
	return true
}

func validCNPJ(digits string) bool {
	if strings.Count(digits, digits[:1]) == len(digits) {
		return false
	}
	weights := []int{6, 5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}
	for _, length := range []int{12, 13} {
		sum := 0
		for index := 0; index < length; index++ {
			sum += int(digits[index]-'0') * weights[index+13-length]
		}
		check := sum % 11
		if check < 2 {
			check = 0
		} else {
			check = 11 - check
		}
		if check != int(digits[length]-'0') {
			return false
		}
	}
	return true
}

// plainText keeps the merchant fields in the uppercase ASCII banks expect.
func plainText(value string, limit int) string {
	var builder strings.Builder
	for _, r := range norm.NFD.String(strings.TrimSpace(value)) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' || r == '.'):
			builder.WriteRune(unicode.ToUpper(r))
		}
	}
	text := strings.Join(strings.Fields(builder.String()), " ")
	if len(text) > limit {
		text = strings.TrimSpace(text[:limit])
	}
	return text
}

func field(id, value string) string {
	return fmt.Sprintf("%s%02d%s", id, len(value), value)
}

// Payload returns the "Pix copia e cola" code of the charge.
func (charge Charge) Payload() (string, error) {
	key, err := NormalizeKey(charge.KeyType, charge.Key)
	if err != nil {
		return "", err
	}
	name, city := plainText(charge.Name, maxNameLength), plainText(charge.City, maxCityLength)
	if name == "" || city == "" {
		return "", errors.New("nome do recebedor e cidade são obrigatórios no Pix")
	}
	if charge.Amount < 0 || charge.Amount > MaxAmount {
		return "", fmt.Errorf("valor do Pix deve estar entre 0 e %d", MaxAmount)
	}
	account := field("00", "br.gov.bcb.pix") + field("01", key)
	if description := plainText(charge.Description, maxDescriptionLength); description != "" {
		account += field("02", description)
	}
	if len(account) > 99 {
		return "", errors.New("chave e descrição do Pix excedem o tamanho permitido")
	}
	payload := field("00", "01") + field("26", account) + field("52", "0000") + field("53", "986")
	if charge.Amount > 0 {
		payload += field("54", fmt.Sprintf("%.2f", charge.Amount))
	}
	payload += field("58", "BR") + field("59", name) + field("60", city) + field("62", field("05", "***")) + "6304"
	return payload + fmt.Sprintf("%04X", crc16(payload)), nil
}

// crc16 is CRC-16/CCITT-FALSE (polynomial 0x1021, initial 0xFFFF).
func crc16(data string) uint16 {
	crc := uint16(0xFFFF)
	for index := 0; index < len(data); index++ {
		crc ^= uint16(data[index]) << 8
		for bit := 0; bit < 8; bit++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// QRCode renders the payload as a PNG QR Code.
func QRCode(payload string) ([]byte, error) {
	return qrcode.Encode(payload, qrcode.Medium, 512)
}

// KeyLabel names the key type for the customer.
func KeyLabel(keyType string) string {
	return map[string]string{KeyCPF: "CPF", KeyCNPJ: "CNPJ", KeyPhone: "Telefone", KeyEmail: "E-mail", KeyRandom: "Chave aleatória"}[keyType]
}

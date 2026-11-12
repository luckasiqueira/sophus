package pix

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestCRC16MatchesReference(t *testing.T) {
	// CRC-16/CCITT-FALSE check value for "123456789".
	if got := crc16("123456789"); got != 0x29B1 {
		t.Fatalf("crc16 = %04X", got)
	}
}

func TestPayloadStructure(t *testing.T) {
	charge := Charge{KeyType: KeyEmail, Key: "pagamentos@example.com", Name: "Loja São João Ltda", City: "São Paulo", Amount: 49.9, Description: "Pedido 12"}
	payload, err := charge.Payload()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"000201", "26", "0014br.gov.bcb.pix", "0122pagamentos@example.com", "0209PEDIDO 12",
		"52040000", "5303986", "540549.90", "5802BR", "5918LOJA SAO JOAO LTDA", "6009SAO PAULO", "62070503***", "6304",
	} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("payload misses %q: %s", expected, payload)
		}
	}
	if body, checksum := payload[:len(payload)-4], payload[len(payload)-4:]; fmt.Sprintf("%04X", crc16(body)) != checksum {
		t.Fatalf("checksum %s does not match the payload", checksum)
	}
	withoutAmount, err := Charge{KeyType: KeyRandom, Key: "123e4567-e89b-12d3-a456-426614174000", Name: "Loja", City: "Rio"}.Payload()
	if err != nil || strings.Contains(withoutAmount, "5303986540") {
		t.Fatalf("a charge without amount must omit field 54: %s %v", withoutAmount, err)
	}
}

func TestCRC16MatchesBancoCentralExample(t *testing.T) {
	// Static BR Code example from the Banco Central's Pix manual.
	body := "00020126580014br.gov.bcb.pix0136123e4567-e12b-12d1-a456-4266554400005204000053039865802BR5913Fulano de Tal6008BRASILIA62070503***6304"
	if got := fmt.Sprintf("%04X", crc16(body)); got != "1D3D" {
		t.Fatalf("crc16 = %s", got)
	}
}

func TestNormalizeKey(t *testing.T) {
	valid := map[[2]string]string{
		{KeyCPF, "529.982.247-25"}:       "52998224725",
		{KeyCNPJ, "11.222.333/0001-81"}:  "11222333000181",
		{KeyPhone, "(11) 98888-7777"}:    "+5511988887777",
		{KeyPhone, "+55 11 98888-7777"}:  "+5511988887777",
		{KeyEmail, "Vendas@Example.com"}: "vendas@example.com",
	}
	for input, expected := range valid {
		if got, err := NormalizeKey(input[0], input[1]); err != nil || got != expected {
			t.Fatalf("NormalizeKey(%v) = %q, %v", input, got, err)
		}
	}
	for _, input := range [][2]string{{KeyCPF, "111.111.111-11"}, {KeyCPF, "529.982.247-24"}, {KeyCNPJ, "11.222.333/0001-80"}, {KeyPhone, "9888"}, {KeyEmail, "sem-arroba"}, {KeyRandom, "abc"}, {"pix", "x"}} {
		if _, err := NormalizeKey(input[0], input[1]); err == nil {
			t.Fatalf("NormalizeKey(%v) must fail", input)
		}
	}
}

func TestQRCodeIsPNG(t *testing.T) {
	image, err := QRCode("000201")
	if err != nil || !bytes.HasPrefix(image, []byte("\x89PNG")) {
		t.Fatalf("QR code is not a PNG: %v", err)
	}
}

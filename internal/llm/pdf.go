package llm

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	pdf "github.com/ledongthuc/pdf"
)

const (
	MaxKnowledgePDFPages = 200
	MaxKnowledgePDFText  = 450000
)

func ExtractPDFText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("não foi possível ler o PDF")
	}
	return ExtractPDFData(data)
}

func ExtractPDFData(data []byte) (text string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			text = ""
			err = errors.New("o PDF é inválido ou está corrompido")
		}
	}()
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", errors.New("não foi possível ler o PDF; verifique se o arquivo não está corrompido")
	}
	if reader.NumPage() <= 0 || reader.NumPage() > MaxKnowledgePDFPages {
		return "", fmt.Errorf("o PDF deve possuir entre 1 e %d páginas", MaxKnowledgePDFPages)
	}
	plainText, err := reader.GetPlainText()
	if err != nil {
		return "", errors.New("não foi possível extrair o texto do PDF")
	}
	extracted, err := io.ReadAll(io.LimitReader(plainText, MaxKnowledgePDFText+1))
	if err != nil {
		return "", errors.New("não foi possível extrair o texto do PDF")
	}
	if len(extracted) > MaxKnowledgePDFText {
		return "", fmt.Errorf("o texto extraído excede %d caracteres", MaxKnowledgePDFText)
	}
	text = strings.Map(func(character rune) rune {
		if character == '\n' || character == '\r' || character == '\t' || character >= 32 {
			return character
		}
		return -1
	}, string(extracted))
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("o PDF não possui texto extraível; documentos escaneados precisam de OCR")
	}
	return text, nil
}

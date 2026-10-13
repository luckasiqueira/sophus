package llm

import (
	"errors"
	"fmt"
	"io"
	"strings"

	pdf "github.com/ledongthuc/pdf"
)

const (
	MaxKnowledgePDFPages = 200
	MaxKnowledgePDFText  = 450000
)

func ExtractPDFText(path string) (text string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			text = ""
			err = errors.New("o PDF é inválido ou está corrompido")
		}
	}()
	file, reader, err := pdf.Open(path)
	if err != nil {
		return "", errors.New("não foi possível ler o PDF; verifique se o arquivo não está corrompido")
	}
	defer file.Close()
	if reader.NumPage() <= 0 || reader.NumPage() > MaxKnowledgePDFPages {
		return "", fmt.Errorf("o PDF deve possuir entre 1 e %d páginas", MaxKnowledgePDFPages)
	}
	plainText, err := reader.GetPlainText()
	if err != nil {
		return "", errors.New("não foi possível extrair o texto do PDF")
	}
	data, err := io.ReadAll(io.LimitReader(plainText, MaxKnowledgePDFText+1))
	if err != nil {
		return "", errors.New("não foi possível extrair o texto do PDF")
	}
	if len(data) > MaxKnowledgePDFText {
		return "", fmt.Errorf("o texto extraído excede %d caracteres", MaxKnowledgePDFText)
	}
	text = strings.Map(func(character rune) rune {
		if character == '\n' || character == '\r' || character == '\t' || character >= 32 {
			return character
		}
		return -1
	}, string(data))
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("o PDF não possui texto extraível; documentos escaneados precisam de OCR")
	}
	return text, nil
}

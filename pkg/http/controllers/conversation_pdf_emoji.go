package controllers

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/go-pdf/fpdf"
	"sophus/internal/reporting/emojis"
	"sophus/pkg/http/requests"
)

// Twemoji artwork is licensed under CC BY 4.0. PDFs include attribution.
const conversationEmojiURL = "https://cdn.jsdelivr.net/gh/jdecked/twemoji@" + emojis.ArtworkVersion + "/assets/72x72/"

var conversationEmojiCache sync.Map

type conversationEmojiLoader func(string) ([]byte, error)

type conversationPDFToken struct{ text, emoji string }

func conversationPDFTokens(text string) []conversationPDFToken {
	runes := []rune(strings.ReplaceAll(text, "\r\n", "\n"))
	var tokens []conversationPDFToken
	for i := 0; i < len(runes); {
		start := i
		if image, end := emojis.Match(runes, i); image != "" {
			i = end
			tokens = append(tokens, conversationPDFToken{emoji: image, text: string(runes[start:i])})
			continue
		}
		if unicode.IsSpace(runes[i]) {
			tokens = append(tokens, conversationPDFToken{text: string(runes[i])})
			i++
			continue
		}
		i++
		for i < len(runes) && !unicode.IsSpace(runes[i]) {
			if image, _ := emojis.Match(runes, i); image != "" {
				break
			}
			i++
		}
		tokens = append(tokens, conversationPDFToken{text: string(runes[start:i])})
	}
	return tokens
}

func loadConversationEmoji(ctx context.Context, key string) ([]byte, error) {
	if value, ok := conversationEmojiCache.Load(key); ok {
		return value.([]byte), nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	timeout := timeUntilDeadline(ctx)
	r := requests.Request{URL: conversationEmojiURL + key + ".png", Method: "GET", PublicOnly: true, MaxResponseBytes: 128 << 10, Timeout: timeout}
	if err := r.Do(); err != nil {
		return nil, err
	}
	config, err := png.DecodeConfig(bytes.NewReader(r.Response.Body))
	if err != nil || config.Width > 128 || config.Height > 128 {
		return nil, fmt.Errorf("invalid emoji PNG")
	}
	conversationEmojiCache.Store(key, r.Response.Body)
	return r.Response.Body, nil
}

// A request never waits beyond the export's shared asset-loading deadline.
func timeUntilDeadline(ctx context.Context) time.Duration {
	timeout := 3 * time.Second
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < timeout {
		timeout = time.Until(deadline)
	}
	if timeout <= 0 {
		return time.Millisecond
	}
	return timeout
}

type conversationEmojiWriter struct {
	pdf    *fpdf.Fpdf
	load   conversationEmojiLoader
	images map[string]bool
	failed map[string]bool
}

func (w *conversationEmojiWriter) write(width, height float64, text string) {
	pdf := w.pdf
	margin := pdf.GetCellMargin()
	pdf.SetCellMargin(0)
	defer pdf.SetCellMargin(margin)
	left := pdf.GetX()
	_, _, _, bottom := pdf.GetMargins()
	_, pageHeight := pdf.GetPageSize()
	_, fontHeight := pdf.GetFontSize()
	size := fontHeight * 1.2
	pending := ""
	pendingWidth := 0.0
	flush := func() {
		if pending == "" {
			return
		}
		pdf.CellFormat(pendingWidth, height, pending, "", 0, "L", false, 0, "")
		pending, pendingWidth = "", 0
	}
	newLine := func() { flush(); pdf.Ln(height); pdf.SetX(left) }
	ensureSpace := func(advance float64) {
		if pdf.GetX()+pendingWidth+advance > left+width && pdf.GetX()+pendingWidth > left {
			newLine()
		}
		if pdf.GetY()+height > pageHeight-bottom {
			flush()
			pdf.AddPage()
			pdf.SetX(left)
		}
	}
	for _, token := range conversationPDFTokens(text) {
		if token.text == "\n" {
			newLine()
			continue
		}
		if token.emoji != "" {
			name := "emoji-" + token.emoji
			if !w.images[name] && !w.failed[name] {
				data, err := w.load(token.emoji)
				if err == nil {
					pdf.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: "PNG"}, bytes.NewReader(data))
					w.images[name] = true
				} else {
					w.failed[name] = true
				}
			}
			if w.images[name] {
				ensureSpace(size)
				flush()
				x, y := pdf.GetX(), pdf.GetY()
				pdf.ImageOptions(name, x, y+(height-size)/2, size, size, false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")
				pdf.SetX(x + size)
				continue
			}
		}
		part := conversationPDFText(strings.ReplaceAll(token.text, "\t", "    "))
		advance := pdf.GetStringWidth(part)
		if advance <= width {
			ensureSpace(advance)
			pending += part
			pendingWidth += advance
		} else {
			for _, r := range part {
				char := string(r)
				advance := pdf.GetStringWidth(char)
				ensureSpace(advance)
				pending += char
				pendingWidth += advance
			}
		}
	}
	newLine()
}

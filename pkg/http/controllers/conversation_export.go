package controllers

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"sophus/internal/repo"

	"github.com/go-pdf/fpdf"
	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
)

func ExportCurrentConversation(ctx iris.Context)  { exportConversationPDF(ctx, false) }
func ExportContactConversations(ctx iris.Context) { exportConversationPDF(ctx, true) }

func exportConversationPDF(ctx iris.Context, all bool) {
	agent, ok := requireAdmin(ctx)
	if !ok {
		return
	}
	conversationURL, err := uuid.Parse(ctx.Params().Get("url"))
	if err != nil {
		writeJSONError(ctx, iris.StatusBadRequest, "conversa inválida")
		return
	}
	export, err := repo.GetConversationExport(ctx.Request().Context(), agent.CompanyId, conversationURL, all)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(ctx, iris.StatusNotFound, "conversa não encontrada")
			return
		}
		ctx.Application().Logger().Errorf("conversation export failed: company=%d conversation=%s error=%v", agent.CompanyId, conversationURL, err)
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível carregar o histórico para exportação")
		return
	}
	data, err := buildConversationExportPDF(export)
	if err != nil {
		ctx.Application().Logger().Errorf("conversation PDF failed: company=%d conversation=%s error=%v", agent.CompanyId, conversationURL, err)
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível gerar o PDF da conversa")
		return
	}
	filename := conversationExportFilename(export.Contact.Number, all)
	ctx.Header("Cache-Control", "private, no-store")
	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	ctx.ContentType("application/pdf")
	_, _ = ctx.Write(data)
}

func exportStatusLabel(status string) string {
	switch status {
	case repo.ConversationStatusOpen:
		return "Aberta"
	case repo.ConversationStatusClosed:
		return "Encerrada"
	case repo.ConversationStatusPending:
		return "Pendente"
	case repo.ConversationStatusRunning:
		return "Em automação"
	default:
		return status
	}
}

func exportEntryText(entry repo.ConversationExportEntry) string {
	if entry.IsDeleted {
		return "[Mensagem excluída]"
	}
	text := entry.Text
	if entry.MediaType != "" && entry.MediaType != "text" && entry.MediaType != "conversation" && entry.MediaType != "flow" {
		kind := map[string]string{"image": "Imagem", "video": "Vídeo", "audio": "Áudio", "document": "Documento", "sticker": "Figurinha", "view_once": "Visualização única"}[entry.MediaType]
		if kind == "" {
			kind = entry.MediaType
		}
		media := "[" + kind + "]"
		if entry.MediaPath != "" {
			media += " " + filepath.Base(strings.ReplaceAll(entry.MediaPath, "\\", "/"))
		}
		if text != "" {
			text += "\n"
		}
		text += media
	}
	if entry.QuotedID != "" {
		text = "Em resposta à mensagem " + entry.QuotedID + "\n" + text
	}
	if text == "" {
		return "[Mensagem sem conteúdo textual]"
	}
	return text
}

func conversationExportFilename(number string, all bool) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, number)
	// Brazilian WhatsApp identities use DDI 55 followed by DDD and 8/9 digits.
	// A local number with DDD 55 must not have those first digits removed.
	if strings.HasPrefix(digits, "55") && (len(digits) == 12 || len(digits) == 13) {
		digits = digits[2:]
	}
	if digits == "" {
		digits = "sem-numero"
	}
	prefix := "conversa-"
	if all {
		prefix = "conversas-contato-"
	}
	return prefix + digits + ".pdf"
}

// fpdf's UTF-8 font writer supports only the basic multilingual plane.
// Keep unsupported symbols identifiable rather than dropping message content.
func conversationPDFText(text string) string {
	var result strings.Builder
	for _, r := range text {
		if r > 0xffff {
			fmt.Fprintf(&result, "[U+%X]", r)
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func buildConversationExportPDF(export repo.ConversationExport) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return buildConversationExportPDFWithEmojis(export, func(key string) ([]byte, error) { return loadConversationEmoji(ctx, key) })
}

func buildConversationExportPDFWithEmojis(export repo.ConversationExport, load conversationEmojiLoader) ([]byte, error) {
	location, err := time.LoadLocation(export.Timezone)
	if err != nil {
		location = time.UTC
	}
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 18)
	pdf.AddUTF8FontFromBytes("Go", "", goregular.TTF)
	pdf.AddUTF8FontFromBytes("Go", "B", gobold.TTF)
	writer := conversationEmojiWriter{pdf: pdf, load: load, images: make(map[string]bool), failed: make(map[string]bool)}
	title := "Exportação da conversa"
	if export.AllConversations {
		title = "Histórico completo do contato"
	}
	pdf.SetTitle(title, true)
	pdf.SetAuthor("Sophus", true)
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont("Go", "", 8)
		pdf.SetTextColor(110, 110, 120)
		pdf.CellFormat(180, 5, fmt.Sprintf("Sophus · %s · Página %d", title, pdf.PageNo()), "", 0, "R", false, 0, "")
		if len(writer.images) > 0 {
			pdf.SetY(-7)
			pdf.SetFont("Go", "", 6)
			pdf.CellFormat(180, 3, "Emojis: Twemoji (CC BY 4.0) · https://twemoji.twitter.com", "", 0, "R", false, 0, "")
		}
	})
	pdf.AddPage()
	pdf.SetFont("Go", "B", 20)
	pdf.SetTextColor(97, 35, 204)
	pdf.MultiCell(180, 10, title, "", "L", false)
	pdf.SetTextColor(60, 60, 70)
	pdf.SetFont("Go", "", 10)
	header := fmt.Sprintf("Empresa: %s\nContato: %s\nTelefone: %s", export.CompanyName, export.Contact.Name, export.Contact.Number)
	if export.Contact.Email != "" {
		header += "\nEmail: " + export.Contact.Email
	}
	header += fmt.Sprintf("\nGerado em: %s · Fuso: %s\nConversas: %d", export.GeneratedAt.In(location).Format("02/01/2006 15:04:05"), location.String(), len(export.Conversations))
	writer.write(180, 6, header)
	for index, conversation := range export.Conversations {
		if index > 0 {
			pdf.AddPage()
		} else {
			pdf.Ln(6)
		}
		pdf.SetFont("Go", "B", 13)
		pdf.SetTextColor(97, 35, 204)
		conversationTitle := fmt.Sprintf("Conversa %d de %d", index+1, len(export.Conversations))
		if conversation.Protocol > 0 {
			conversationTitle += fmt.Sprintf(" · Protocolo #%d", conversation.Protocol)
		}
		pdf.MultiCell(180, 7, conversationTitle, "", "L", false)
		pdf.SetFont("Go", "", 9)
		pdf.SetTextColor(80, 80, 90)
		connection := conversation.ConnectionName
		if conversation.ConnectionNumber != "" {
			connection += " · " + conversation.ConnectionNumber
		}
		writer.write(180, 5, fmt.Sprintf("Conexão: %s\nFila: %s · Agente: %s\nSituação atual: %s\nCriada em: %s · Atualizada em: %s\nIdentificador: %s",
			connection, conversation.DepartmentName, conversation.AgentName, exportStatusLabel(conversation.Status),
			conversation.CreatedAt.In(location).Format("02/01/2006 15:04"), conversation.UpdatedAt.In(location).Format("02/01/2006 15:04"), conversation.URL.String()))
		pdf.Ln(5)
		if len(conversation.Entries) == 0 {
			pdf.MultiCell(180, 6, "Nenhuma mensagem ou nota interna registrada nesta conversa.", "", "L", false)
		}
		for _, entry := range conversation.Entries {
			if pdf.GetY()+18 > 279 {
				pdf.AddPage()
			}
			label := "Recebida"
			if entry.IsFromMe {
				label = "Enviada pela empresa"
			}
			if entry.Kind == "note" {
				label = "Nota interna"
			}
			if entry.SenderName != "" && !entry.IsFromMe {
				label += " · " + entry.SenderName
			}
			if entry.SenderNumber != "" && !entry.IsFromMe {
				label += " · " + entry.SenderNumber
			}
			pdf.SetFont("Go", "B", 9)
			if entry.Kind == "note" {
				pdf.SetTextColor(180, 110, 20)
			} else if entry.IsFromMe {
				pdf.SetTextColor(97, 35, 204)
			} else {
				pdf.SetTextColor(25, 120, 95)
			}
			writer.write(180, 5, entry.CreatedAt.In(location).Format("02/01/2006 15:04:05")+" · "+label)
			pdf.SetFont("Go", "", 10)
			pdf.SetTextColor(45, 45, 55)
			writer.write(180, 5.5, exportEntryText(entry))
			pdf.Ln(4)
		}
	}
	var output bytes.Buffer
	if err := pdf.Output(&output); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

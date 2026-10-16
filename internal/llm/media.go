package llm

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Endpoints are variables so tests can point them at a local server.
var (
	geminiAPIBase   = "https://generativelanguage.googleapis.com/v1beta"
	groqChatURL     = "https://api.groq.com/openai/v1/chat/completions"
	claudeURL       = "https://api.anthropic.com/v1/messages"
	groqVisionModel = "meta-llama/llama-4-scout-17b-16e-instruct"
)

// Request size limits documented by the providers for inline (base64) media.
// Base64 grows data by a third, so the raw limits keep the encoded request
// below Gemini's 20 MB and Groq's 4 MB image ceilings.
const (
	geminiInlineMaxBytes = 14 << 20
	groqImageMaxBytes    = 3 << 20
	// Claude accepts images up to 5 MB and requests up to 32 MB.
	claudeImageMaxBytes    = 5 << 20
	claudeDocumentMaxBytes = 22 << 20
	defaultMediaPrompt     = "Transcreva ou resuma este arquivo. Retorne somente o texto útil, sem comentários sobre o processo."
	defaultAudioPrompt     = "Transcreva este áudio em português, exatamente como foi falado. Retorne somente a transcrição."
	geminiMediaOutputSize  = 8192
)

// mediaKinds lists, per provider, the attachment kinds the Transcrever Mídia
// block can send: OpenAI and Gemini read every kind; Groq transcribes audio
// with Whisper and reads images with its Llama 4 Scout vision model; Claude
// reads images and PDF or text documents, but neither audio nor video.
var mediaKinds = map[string]map[string]bool{
	"openai": {"audio": true, "image": true, "video": true, "document": true},
	"gemini": {"audio": true, "image": true, "video": true, "document": true},
	"groq":   {"audio": true, "image": true},
	"claude": {"image": true, "document": true},
}

// MediaAnalysisProvider reports whether a credential of this provider can be
// used by the media transcription block at all.
func MediaAnalysisProvider(provider string) bool {
	return len(mediaKinds[provider]) > 0
}

func SupportsMediaAnalysis(provider, kind string) bool {
	return mediaKinds[provider][kind]
}

// UnsupportedMediaReason explains, in the flow's language, why a credential
// cannot read a given attachment kind.
func UnsupportedMediaReason(provider, kind string) string {
	names := map[string]string{"audio": "áudio", "image": "imagem", "video": "vídeo", "document": "documento"}
	if !MediaAnalysisProvider(provider) {
		return "credencial incompatível com transcrição de mídia; use OpenAI, Gemini, Claude ou Groq"
	}
	if provider == "groq" {
		return fmt.Sprintf("a credencial Groq analisa somente áudio e imagem, não %s", names[kind])
	}
	if provider == "claude" {
		return fmt.Sprintf("a credencial Claude analisa somente imagem e documento PDF ou de texto, não %s", names[kind])
	}
	return fmt.Sprintf("a credencial não analisa %s", names[kind])
}

func mediaInstruction(kind, prompt string) string {
	if prompt = strings.TrimSpace(prompt); prompt != "" {
		return prompt
	}
	if kind == "audio" {
		return defaultAudioPrompt
	}
	return defaultMediaPrompt
}

// geminiDocumentType accepts the document formats Gemini reads inline.
func geminiDocumentType(mimeType string) bool {
	mimeType = strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0]))
	return mimeType == "application/pdf" || strings.HasPrefix(mimeType, "text/")
}

func analyzeMediaGemini(ctx context.Context, client *http.Client, model, apiKey, kind, mimeType string, data []byte, prompt string, safeMode bool) (Response, error) {
	if len(data) > geminiInlineMaxBytes {
		return Response{}, &mediaError{fmt.Sprintf("o arquivo tem %d MB; o Gemini aceita até %d MB por mídia", len(data)>>20, geminiInlineMaxBytes>>20)}
	}
	if kind == "document" && !geminiDocumentType(mimeType) {
		return Response{}, &mediaError{"o Gemini lê somente documentos PDF ou de texto"}
	}
	mimeType = strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0])
	payload := map[string]interface{}{
		"contents": []map[string]interface{}{{
			"role": "user",
			"parts": []map[string]interface{}{
				{"text": mediaInstruction(kind, prompt)},
				{"inlineData": map[string]string{"mimeType": mimeType, "data": base64.StdEncoding.EncodeToString(data)}},
			},
		}},
		"generationConfig": map[string]interface{}{"maxOutputTokens": geminiMediaOutputSize, "temperature": 0.2},
	}
	if safeMode {
		payload["systemInstruction"] = map[string]interface{}{"parts": []map[string]string{{"text": SafeMediaInstructions()}}}
	}
	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	endpoint := geminiAPIBase + "/models/" + url.PathEscape(model) + ":generateContent"
	if err := doJSON(ctx, client, "gemini", endpoint, apiKey, "", payload, &result); err != nil {
		return Response{}, err
	}
	if result.PromptFeedback.BlockReason != "" {
		return Response{}, &mediaError{"o Gemini recusou analisar esta mídia por suas políticas de segurança"}
	}
	var parts []string
	if len(result.Candidates) > 0 {
		for _, part := range result.Candidates[0].Content.Parts {
			if text := strings.TrimSpace(part.Text); text != "" {
				parts = append(parts, text)
			}
		}
	}
	if len(parts) == 0 {
		return Response{}, &mediaError{"o Gemini retornou uma resposta vazia para esta mídia"}
	}
	return Response{Text: strings.Join(parts, "\n")}, nil
}

func analyzeImageGroq(ctx context.Context, client *http.Client, apiKey, mimeType string, data []byte, prompt string, safeMode bool) (Response, error) {
	if len(data) > groqImageMaxBytes {
		return Response{}, &mediaError{fmt.Sprintf("a imagem tem %.1f MB; a Groq aceita imagens de até %d MB", float64(len(data))/(1<<20), groqImageMaxBytes>>20)}
	}
	mimeType = strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0])
	messages := []map[string]interface{}{}
	if safeMode {
		messages = append(messages, map[string]interface{}{"role": "system", "content": SafeMediaInstructions()})
	}
	messages = append(messages, map[string]interface{}{"role": "user", "content": []map[string]interface{}{
		{"type": "text", "text": mediaInstruction("image", prompt)},
		{"type": "image_url", "image_url": map[string]string{"url": "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)}},
	}})
	payload := map[string]interface{}{"model": groqVisionModel, "messages": messages, "temperature": 0.2, "max_completion_tokens": 4096}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := doJSON(ctx, client, "groq", groqChatURL, apiKey, "", payload, &result); err != nil {
		return Response{}, err
	}
	if len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
		return Response{}, &mediaError{"a Groq retornou uma resposta vazia para esta imagem"}
	}
	return Response{Text: strings.TrimSpace(result.Choices[0].Message.Content)}, nil
}

var claudeImageTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}

func analyzeMediaClaude(ctx context.Context, client *http.Client, model, apiKey, kind, mimeType string, data []byte, prompt string, safeMode bool) (Response, error) {
	mimeType = strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0]))
	var block map[string]interface{}
	switch {
	case kind == "image":
		if !claudeImageTypes[mimeType] {
			return Response{}, &mediaError{"o Claude lê somente imagens JPEG, PNG, GIF ou WebP"}
		}
		if len(data) > claudeImageMaxBytes {
			return Response{}, &mediaError{fmt.Sprintf("a imagem tem %.1f MB; o Claude aceita imagens de até %d MB", float64(len(data))/(1<<20), claudeImageMaxBytes>>20)}
		}
		block = map[string]interface{}{"type": "image", "source": map[string]string{"type": "base64", "media_type": mimeType, "data": base64.StdEncoding.EncodeToString(data)}}
	case mimeType == "application/pdf":
		if len(data) > claudeDocumentMaxBytes {
			return Response{}, &mediaError{fmt.Sprintf("o documento tem %d MB; o Claude aceita até %d MB", len(data)>>20, claudeDocumentMaxBytes>>20)}
		}
		block = map[string]interface{}{"type": "document", "source": map[string]string{"type": "base64", "media_type": "application/pdf", "data": base64.StdEncoding.EncodeToString(data)}}
	case strings.HasPrefix(mimeType, "text/"):
		if len(data) > claudeDocumentMaxBytes {
			return Response{}, &mediaError{fmt.Sprintf("o documento tem %d MB; o Claude aceita até %d MB", len(data)>>20, claudeDocumentMaxBytes>>20)}
		}
		block = map[string]interface{}{"type": "document", "source": map[string]string{"type": "text", "media_type": "text/plain", "data": string(data)}}
	default:
		return Response{}, &mediaError{"o Claude lê somente documentos PDF ou de texto"}
	}
	system := ""
	if safeMode {
		system = SafeMediaInstructions()
	}
	payload := claudePayload(model, 4096, 0.2, system,
		[]map[string]interface{}{{"role": "user", "content": []map[string]interface{}{block, {"type": "text", "text": mediaInstruction(kind, prompt)}}}})
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := doJSON(ctx, client, "claude", claudeURL, apiKey, "2023-06-01", payload, &result); err != nil {
		return Response{}, err
	}
	var parts []string
	for _, content := range result.Content {
		if text := strings.TrimSpace(content.Text); content.Type == "text" && text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		if err := claudeEmptyResponseError(result.StopReason); err != nil {
			return Response{}, err
		}
		return Response{}, &mediaError{"o Claude retornou uma resposta vazia para esta mídia"}
	}
	return Response{Text: strings.Join(parts, "\n")}, nil
}

// mediaError is a problem with the attachment itself, safe to show as is.
type mediaError struct{ message string }

func (err *mediaError) Error() string { return err.message }

// MediaErrorMessage returns a reason fit for the flow: attachment problems
// verbatim, provider failures through SafeErrorMessage.
func MediaErrorMessage(err error) string {
	var media *mediaError
	if errors.As(err, &media) {
		return media.message
	}
	return SafeErrorMessage(err)
}

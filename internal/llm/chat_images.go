package llm

import (
	"encoding/base64"
	"strings"
)

// Image is a picture attached to a chat message, sent inline (base64).
type Image struct {
	MIME string
	Data []byte
}

// MaxChatImages caps the images an AI agent may receive as context.
const MaxChatImages = 10

var chatImageTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}

// ChatImageType reports whether providers accept this image format in chat.
func ChatImageType(mimeType string) bool {
	return chatImageTypes[strings.ToLower(strings.TrimSpace(mimeType))]
}

// SupportsChatImages reports whether the provider's chat model can look at
// images: OpenAI, Claude and Gemini models do; on Groq only the Llama 4
// vision models; DeepSeek's chat API reads text only.
func SupportsChatImages(provider, model string) bool {
	switch provider {
	case "openai", "claude", "gemini":
		return true
	case "groq":
		return model == groqVisionModel || strings.Contains(strings.ToLower(model), "llama-4")
	}
	return false
}

// ChatImageLimits returns, per provider, the largest image accepted and the
// raw bytes all images of one request may add up to. Base64 grows data by a
// third: Groq takes 4 MB per encoded image and 5 images, Claude 5 MB per image
// and 32 MB per request, Gemini 20 MB of inline data, OpenAI 50 MB.
func ChatImageLimits(provider string) (maxCount, perImage, total int) {
	switch provider {
	case "groq":
		return 5, groqImageMaxBytes, groqImageMaxBytes
	case "claude":
		return MaxChatImages, claudeImageMaxBytes, 20 << 20
	case "gemini":
		return MaxChatImages, geminiInlineMaxBytes, geminiInlineMaxBytes
	}
	return MaxChatImages, 20 << 20, 30 << 20
}

func dataURL(image Image) string {
	return "data:" + image.MIME + ";base64," + base64.StdEncoding.EncodeToString(image.Data)
}

// openAIContent is a message's content in the chat completions format:
// plain text, or text and image parts when it has images.
func openAIContent(message Message) interface{} {
	if len(message.Images) == 0 {
		return message.Content
	}
	parts := []interface{}{map[string]interface{}{"type": "text", "text": message.Content}}
	for _, image := range message.Images {
		parts = append(parts, map[string]interface{}{"type": "image_url", "image_url": map[string]string{"url": dataURL(image)}})
	}
	return parts
}

func openAIMessages(system string, messages []Message) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(messages)+1)
	if system != "" {
		result = append(result, map[string]interface{}{"role": "system", "content": system})
	}
	for _, message := range messages {
		result = append(result, map[string]interface{}{"role": message.Role, "content": openAIContent(message)})
	}
	return result
}

// claudeContent puts images before the text, as Anthropic recommends.
func claudeContent(message Message) interface{} {
	if len(message.Images) == 0 {
		return message.Content
	}
	blocks := make([]interface{}, 0, len(message.Images)+1)
	for _, image := range message.Images {
		blocks = append(blocks, map[string]interface{}{"type": "image", "source": map[string]string{
			"type": "base64", "media_type": image.MIME, "data": base64.StdEncoding.EncodeToString(image.Data),
		}})
	}
	return append(blocks, map[string]interface{}{"type": "text", "text": message.Content})
}

func claudeMessages(messages []Message) []interface{} {
	result := make([]interface{}, 0, len(messages))
	for _, message := range messages {
		result = append(result, map[string]interface{}{"role": message.Role, "content": claudeContent(message)})
	}
	return result
}

func geminiContents(messages []Message) []interface{} {
	result := make([]interface{}, 0, len(messages))
	for _, message := range messages {
		role := "user"
		if message.Role == "assistant" {
			role = "model"
		}
		parts := []interface{}{map[string]string{"text": message.Content}}
		for _, image := range message.Images {
			parts = append(parts, map[string]interface{}{"inlineData": map[string]string{
				"mimeType": image.MIME, "data": base64.StdEncoding.EncodeToString(image.Data),
			}})
		}
		result = append(result, map[string]interface{}{"role": role, "parts": parts})
	}
	return result
}

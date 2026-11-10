package waha

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/crypto/hkdf"
)

const (
	maxMediaBytes = 72 << 20
	mediaHost     = "https://mmg.whatsapp.net"
	macLength     = 10
)

var mediaAppInfo = map[string]string{
	"imageMessage":    "WhatsApp Image Keys",
	"stickerMessage":  "WhatsApp Image Keys",
	"videoMessage":    "WhatsApp Video Keys",
	"audioMessage":    "WhatsApp Audio Keys",
	"documentMessage": "WhatsApp Document Keys",
}

var mediaHTTPClient = &http.Client{}

// POST /message/downloadmedia {message:{<kind>Message:{...}}} -> {data:{base64, mimetype}}
//
// Evolution GO decrypts the attachment with the connection's whatsmeow client;
// the operation only needs the message's own media key, so the bridge performs
// the same download + decryption locally, independently of the engine.
func downloadMediaEndpoint(ctx context.Context, payload []byte) result {
	var request struct {
		Message map[string]json.RawMessage `json:"message"`
	}
	if err := decode(payload, &request); err != nil {
		return failure(http.StatusBadRequest, err.Error())
	}
	for kind, raw := range request.Message {
		appInfo, ok := mediaAppInfo[kind]
		if !ok {
			continue
		}
		var media map[string]any
		if err := json.Unmarshal(raw, &media); err != nil {
			return failure(http.StatusBadRequest, "invalid media message")
		}
		data, err := DownloadMedia(ctx, appInfo, media)
		if err != nil {
			return failure(http.StatusInternalServerError, err.Error())
		}
		return success(map[string]any{
			"data": map[string]string{
				"base64":   base64.StdEncoding.EncodeToString(data),
				"mimetype": field(media, "mimetype"),
			},
			"message": "success",
		})
	}
	return failure(http.StatusBadRequest, "no downloadable media in message")
}

// field reads a protobuf JSON field case-insensitively ("URL" == "url").
func field(values map[string]any, name string) string {
	for key, value := range values {
		if strings.EqualFold(key, name) {
			if text, ok := value.(string); ok {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

func bytesField(values map[string]any, name string) []byte {
	text := field(values, name)
	if text == "" {
		return nil
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(text); err == nil {
			return decoded
		}
	}
	return nil
}

// DownloadMedia fetches and decrypts a WhatsApp attachment (AES-256-CBC with
// keys expanded from mediaKey via HKDF-SHA256, authenticated by HMAC-SHA256).
func DownloadMedia(ctx context.Context, appInfo string, media map[string]any) ([]byte, error) {
	mediaKey := bytesField(media, "mediaKey")
	if len(mediaKey) != 32 {
		return nil, errors.New("media message has no valid mediaKey")
	}
	urls := []string{}
	if direct := field(media, "url"); strings.HasPrefix(direct, "https://") {
		urls = append(urls, direct)
	}
	if path := field(media, "directPath"); path != "" {
		urls = append(urls, mediaHost+path)
	}
	if len(urls) == 0 {
		return nil, errors.New("media message has no download URL")
	}
	var lastErr error
	for _, target := range urls {
		encrypted, err := fetchMedia(ctx, target)
		if err != nil {
			lastErr = err
			continue
		}
		if expected := bytesField(media, "fileEncSHA256"); len(expected) == sha256.Size {
			if sum := sha256.Sum256(encrypted); !bytes.Equal(sum[:], expected) {
				lastErr = errors.New("encrypted media hash mismatch")
				continue
			}
		}
		plain, err := decryptMedia(encrypted, mediaKey, appInfo)
		if err != nil {
			lastErr = err
			continue
		}
		if expected := bytesField(media, "fileSHA256"); len(expected) == sha256.Size {
			if sum := sha256.Sum256(plain); !bytes.Equal(sum[:], expected) {
				lastErr = errors.New("decrypted media hash mismatch")
				continue
			}
		}
		return plain, nil
	}
	return nil, lastErr
}

func fetchMedia(ctx context.Context, target string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Origin", "https://web.whatsapp.com")
	response, err := mediaHTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download media: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download media: HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxMediaBytes+1))
	if err != nil {
		return nil, fmt.Errorf("download media: %w", err)
	}
	if len(data) > maxMediaBytes {
		return nil, errors.New("media exceeds the size limit")
	}
	return data, nil
}

func expandMediaKey(mediaKey []byte, appInfo string) (iv, cipherKey, macKey []byte, err error) {
	expanded := make([]byte, 112)
	if _, err = io.ReadFull(hkdf.New(sha256.New, mediaKey, nil, []byte(appInfo)), expanded); err != nil {
		return nil, nil, nil, err
	}
	return expanded[:16], expanded[16:48], expanded[48:80], nil
}

func decryptMedia(encrypted, mediaKey []byte, appInfo string) ([]byte, error) {
	if len(encrypted) <= macLength {
		return nil, errors.New("media file is too short")
	}
	iv, cipherKey, macKey, err := expandMediaKey(mediaKey, appInfo)
	if err != nil {
		return nil, err
	}
	file, mac := encrypted[:len(encrypted)-macLength], encrypted[len(encrypted)-macLength:]
	signer := hmac.New(sha256.New, macKey)
	signer.Write(iv)
	signer.Write(file)
	if !hmac.Equal(signer.Sum(nil)[:macLength], mac) {
		return nil, errors.New("media HMAC mismatch")
	}
	if len(file)%aes.BlockSize != 0 {
		return nil, errors.New("media ciphertext is not block aligned")
	}
	block, err := aes.NewCipher(cipherKey)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(file))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, file)
	padding := int(plain[len(plain)-1])
	if padding == 0 || padding > aes.BlockSize || padding > len(plain) {
		return nil, errors.New("invalid media padding")
	}
	for _, value := range plain[len(plain)-padding:] {
		if int(value) != padding {
			return nil, errors.New("invalid media padding")
		}
	}
	return plain[:len(plain)-padding], nil
}

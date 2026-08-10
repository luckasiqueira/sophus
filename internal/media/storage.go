package media

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"sophus/utils/env"

	"github.com/google/uuid"
)

const sniffSize = 512

var allowedTypes = map[string]map[string]string{
	"image": {
		"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp",
	},
	"video": {
		"video/mp4": ".mp4", "video/webm": ".webm", "video/quicktime": ".mov",
	},
	"audio": {
		"audio/webm": ".webm", "audio/ogg": ".ogg", "audio/mpeg": ".mp3", "audio/mp4": ".m4a",
		"audio/wav": ".wav", "audio/x-wav": ".wav",
	},
}

var maxSizes = map[string]int64{
	"image": 10 << 20,
	"video": 50 << 20,
	"audio": 20 << 20,
}

type Stored struct {
	Path     string `json:"path"`
	URL      string `json:"url"`
	MimeType string `json:"mimeType"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
}

func Save(companyID int, kind, originalName, declaredType string, source io.Reader) (Stored, error) {
	allowed, ok := allowedTypes[kind]
	if !ok {
		return Stored{}, errors.New("tipo de mídia inválido")
	}
	root := strings.TrimSpace(env.Backend["MEDIA_DIRECTORY"])
	if root == "" {
		return Stored{}, errors.New("MEDIA_DIRECTORY não configurada")
	}

	buffer := make([]byte, sniffSize)
	read, readErr := io.ReadFull(source, buffer)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return Stored{}, readErr
	}
	buffer = buffer[:read]
	mimeType := selectMimeType(allowed, declaredType, originalName, buffer)
	extension, ok := allowed[mimeType]
	if !ok {
		return Stored{}, fmt.Errorf("formato de %s não suportado: %s", kind, mimeType)
	}

	directory := filepath.Join(root, strconv.Itoa(companyID), "flows")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return Stored{}, err
	}
	fileName := uuid.NewString() + extension
	token, err := Sign(companyID, fileName)
	if err != nil {
		return Stored{}, err
	}
	filePath := filepath.Join(directory, fileName)
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return Stored{}, err
	}
	saved := false
	defer func() {
		_ = file.Close()
		if !saved {
			_ = os.Remove(filePath)
		}
	}()

	limit := maxSizes[kind]
	written, err := io.Copy(file, io.LimitReader(io.MultiReader(bytes.NewReader(buffer), source), limit+1))
	if err != nil {
		return Stored{}, err
	}
	if written > limit {
		return Stored{}, fmt.Errorf("arquivo excede o limite de %d MB", limit>>20)
	}
	if err := file.Close(); err != nil {
		return Stored{}, err
	}
	saved = true
	return Stored{
		Path:     fileName,
		URL:      fmt.Sprintf("/medias/%d/flows/%s?token=%s", companyID, fileName, token),
		MimeType: mimeType,
		Name:     filepath.Base(originalName),
		Size:     written,
	}, nil
}

func FilePath(companyID int, fileName string) (string, error) {
	if fileName == "" || filepath.Base(fileName) != fileName {
		return "", errors.New("caminho de mídia inválido")
	}
	root := strings.TrimSpace(env.Backend["MEDIA_DIRECTORY"])
	if root == "" {
		return "", errors.New("MEDIA_DIRECTORY não configurada")
	}
	flowPath := filepath.Join(root, strconv.Itoa(companyID), "flows", fileName)
	if _, err := os.Stat(flowPath); err == nil {
		return flowPath, nil
	}
	legacyPath := filepath.Join(root, strconv.Itoa(companyID), fileName)
	if _, err := os.Stat(legacyPath); err == nil {
		return legacyPath, nil
	}
	return flowPath, nil
}

func SignedURL(companyID int, fileName string) (string, error) {
	if _, err := FilePath(companyID, fileName); err != nil {
		return "", err
	}
	baseURL, err := publicBaseURL()
	if err != nil {
		return "", err
	}
	token, err := Sign(companyID, fileName)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/medias/%d/flows/%s?token=%s", baseURL, companyID, url.PathEscape(fileName), token), nil
}

func Sign(companyID int, fileName string) (string, error) {
	if fileName == "" || filepath.Base(fileName) != fileName {
		return "", errors.New("caminho de mídia inválido")
	}
	secret := strings.TrimSpace(env.Backend["SALT_JWT"])
	if secret == "" {
		return "", errors.New("SALT_JWT não configurado")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d:%s", companyID, fileName)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func Verify(companyID int, fileName, token string) bool {
	expected, err := Sign(companyID, fileName)
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(expected), []byte(token))
}

func publicBaseURL() (string, error) {
	value := strings.TrimSpace(env.Backend["APP_DOMAIN"])
	if value == "" {
		return "", errors.New("APP_DOMAIN não configurada")
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("APP_DOMAIN inválida")
	}
	return strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/"), nil
}

func selectMimeType(allowed map[string]string, declaredType, originalName string, header []byte) string {
	declaredType = normalizeMimeType(declaredType)
	if _, ok := allowed[declaredType]; ok {
		return declaredType
	}
	detected := normalizeMimeType(http.DetectContentType(header))
	if _, ok := allowed[detected]; ok {
		return detected
	}
	fromExtension := normalizeMimeType(mime.TypeByExtension(strings.ToLower(filepath.Ext(originalName))))
	if _, ok := allowed[fromExtension]; ok {
		return fromExtension
	}
	return detected
}

func normalizeMimeType(value string) string {
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = value[:index]
	}
	return strings.ToLower(strings.TrimSpace(value))
}

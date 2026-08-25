package media

import (
	"errors"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"sophus/utils/env"
)

const (
	ScopeMessages = "messages"
	ScopeFlows    = "flows"
)

type Item struct {
	Name       string    `json:"name"`
	Scope      string    `json:"scope"`
	Kind       string    `json:"kind"`
	MimeType   string    `json:"mimeType"`
	SizeBytes  int64     `json:"sizeBytes"`
	CreatedAt  time.Time `json:"createdAt"`
	PreviewURL string    `json:"previewURL"`
}

func MaxSize(kind string) int64 {
	return maxSizes[kind]
}

func List(companyID int) ([]Item, error) {
	items := []Item{}
	for _, scope := range []string{ScopeMessages, ScopeFlows} {
		directory, err := scopeDirectory(companyID, scope)
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(directory)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(entry.Name())))
			items = append(items, Item{
				Name: entry.Name(), Scope: scope, Kind: kindForMime(mimeType), MimeType: mimeType,
				SizeBytes: info.Size(), CreatedAt: info.ModTime(),
				PreviewURL: "/medias/" + strconv.Itoa(companyID) + "/" + scopePath(scope, entry.Name()),
			})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func ScanCompany(companyID int) (int64, int, error) {
	items, err := List(companyID)
	if err != nil {
		return 0, 0, err
	}
	var total int64
	for _, item := range items {
		total += item.SizeBytes
	}
	return total, len(items), nil
}

func Delete(companyID int, scope, name string) (int64, error) {
	path, err := ScopedPath(companyID, scope, name)
	if err != nil {
		return 0, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return 0, errors.New("arquivo de mídia inválido")
	}
	if err := os.Remove(path); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func ScopedPath(companyID int, scope, name string) (string, error) {
	if companyID <= 0 || name == "" || filepath.Base(name) != name || strings.Contains(name, ":") {
		return "", errors.New("caminho de mídia inválido")
	}
	directory, err := scopeDirectory(companyID, scope)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, name), nil
}

func scopeDirectory(companyID int, scope string) (string, error) {
	root := strings.TrimSpace(env.Backend["MEDIA_DIRECTORY"])
	if root == "" {
		return "", errors.New("MEDIA_DIRECTORY não configurada")
	}
	companyDirectory := filepath.Join(root, strconv.Itoa(companyID))
	switch scope {
	case ScopeMessages:
		return companyDirectory, nil
	case ScopeFlows:
		return filepath.Join(companyDirectory, "flows"), nil
	default:
		return "", errors.New("escopo de mídia inválido")
	}
}

func scopePath(scope, name string) string {
	if scope == ScopeFlows {
		return "flows/" + name
	}
	return name
}

func kindForMime(mimeType string) string {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	default:
		return "document"
	}
}

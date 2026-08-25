package utils

import (
	"encoding/base64"
	"os"
)

func MediaDecoder(base46, path, fileName string) error {
	data, err := base64.StdEncoding.DecodeString(base46)
	if err != nil {
		return err
	}
	err = os.MkdirAll(path, 0o755)
	if err != nil {
		return err
	}
	return os.WriteFile(path+fileName, data, 0644)
}

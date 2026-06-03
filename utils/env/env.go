package env

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
)

var Backend = envReader() //(".env-backend", "env")

func envReader() map[string]string {
	envList := make(map[string]string)
	f, err := os.Open(".env")
	defer f.Close()
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") || strings.Contains(err.Error(), "cannot find the file") {
			systemEnvs := os.Environ()
			for _, env := range systemEnvs {
				e := strings.SplitN(env, "=", 2)
				key, value := e[0], e[1]
				envList[key] = value
			}
			return envList
		}
	}
	envList, err = godotenv.Read(".env")
	if err != nil {
		//fmt.Println(err)
		os.Exit(1)
	}
	return envList
}

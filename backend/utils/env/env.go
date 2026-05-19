package env

import "github.com/joho/godotenv"

var Backend, _ = godotenv.Read(".env-backend", "env")

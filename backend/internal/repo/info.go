package repo

import (
	"fmt"
	"zubly/backend/utils/env"
)

var apiBaseURL = fmt.Sprintf("https://%s", env.Backend["WPP_API_DOMAIN"])

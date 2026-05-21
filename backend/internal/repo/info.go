package repo

import (
	"fmt"
	"sophus/backend/utils/env"
)

var apiBaseURL = fmt.Sprintf("https://%s", env.Backend["WPP_API_DOMAIN"])

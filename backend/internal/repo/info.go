package repo

import (
	"fmt"
	"sophus/backend/utils/env"
)

var ApiBaseURL = fmt.Sprintf("https://%s", env.Backend["WPP_API_DOMAIN"])

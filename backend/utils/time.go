package utils

import "time"

func TimeNow() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func TimeFromTimestamp(timestamp string) (time.Time, error) {
	return time.Parse("15:04:05 02-01-2006", timestamp)
}

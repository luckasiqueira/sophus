package repo

import (
	"strings"
	"unicode/utf8"
)

const (
	minimumTypingDelay = 700
	maximumTypingDelay = 7000
	typingDelayPerRune = 45
)

func TypingDelayMilliseconds(message string) int {
	length := utf8.RuneCountInString(strings.TrimSpace(message))
	delay := length * typingDelayPerRune
	if delay < minimumTypingDelay {
		return minimumTypingDelay
	}
	if delay > maximumTypingDelay {
		return maximumTypingDelay
	}
	return delay
}

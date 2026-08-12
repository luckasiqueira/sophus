package repo

import (
	"strings"
	"testing"
)

func TestTypingDelayIsProportionalAndBounded(t *testing.T) {
	short := TypingDelayMilliseconds("Oi")
	medium := TypingDelayMilliseconds("Esta é uma mensagem um pouco maior")
	long := TypingDelayMilliseconds(strings.Repeat("a", 1000))

	if short != minimumTypingDelay {
		t.Fatalf("short delay = %d, want %d", short, minimumTypingDelay)
	}
	if medium <= short {
		t.Fatalf("medium delay = %d, must be greater than short delay %d", medium, short)
	}
	if long != maximumTypingDelay {
		t.Fatalf("long delay = %d, want %d", long, maximumTypingDelay)
	}
}

func TestTypingDelayCountsUnicodeRunes(t *testing.T) {
	if got, want := TypingDelayMilliseconds(strings.Repeat("á", 20)), 20*typingDelayPerRune; got != want {
		t.Fatalf("unicode delay = %d, want %d", got, want)
	}
}

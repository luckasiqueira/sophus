package emojis

import "testing"

func TestEveryStandardizedEmojiHasArtworkAndMatchesWholeSequence(t *testing.T) {
	if FullyQualifiedCount != 3944 || len(emojiImages) != 5225 {
		t.Fatalf("incomplete catalog: %d full emojis, %d variants", FullyQualifiedCount, len(emojiImages))
	}
	for sequence, expected := range emojiImages {
		runes := []rune(sequence)
		image, end := Match(runes, 0)
		if image != expected || end != len(runes) {
			t.Errorf("sequence %q: image=%q end=%d, want %q %d", sequence, image, end, expected, len(runes))
		}
	}
}

func TestEmojiDoesNotConsumeFollowingText(t *testing.T) {
	runes := []rune("x👨‍👩‍👧‍👦y")
	image, end := Match(runes, 1)
	if image == "" || string(runes[end:]) != "y" {
		t.Fatalf("incorrect sequence boundary: %q %d", image, end)
	}
	if image, _ := Match([]rune("Olá, 123"), 0); image != "" {
		t.Fatal("ordinary text classified as emoji")
	}
}

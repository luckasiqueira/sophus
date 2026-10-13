// Package emojis recognizes the complete Unicode emoji catalog and maps each
// standardized sequence to verified Twemoji artwork, including alternate forms.
package emojis

//go:generate go run generate.go

type node struct {
	image string
	next  map[rune]*node
}

var catalogRoot = buildCatalog()

func buildCatalog() *node {
	root := &node{next: make(map[rune]*node)}
	for sequence, image := range emojiImages {
		current := root
		for _, r := range sequence {
			if current.next[r] == nil {
				current.next[r] = &node{next: make(map[rune]*node)}
			}
			current = current.next[r]
		}
		current.image = image
	}
	return root
}

// Match returns the longest standardized sequence starting at offset, so skin
// tones, flags, keycaps and ZWJ families/professions remain one rendered image.
func Match(text []rune, offset int) (image string, end int) {
	end = offset
	current := catalogRoot
	for index := offset; index < len(text); index++ {
		current = current.next[text[index]]
		if current == nil {
			break
		}
		if current.image != "" {
			image, end = current.image, index+1
		}
	}
	return image, end
}

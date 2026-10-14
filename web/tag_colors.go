package web

import (
	"fmt"
	"strings"

	"sophus/internal/repo"
)

// TagColorStyle maps a stored tag color key (repo.ContactTagColors) to the
// Tailwind classes used by tag chips, both server-rendered and in page scripts.
type TagColorStyle struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Chip  string `json:"chip"`
	Dot   string `json:"dot"`
}

var tagColorStyles = []TagColorStyle{
	{Key: "purple", Label: "Roxo", Chip: "bg-purple-100 text-purple-800", Dot: "bg-purple-500"},
	{Key: "blue", Label: "Azul", Chip: "bg-blue-100 text-blue-800", Dot: "bg-blue-500"},
	{Key: "teal", Label: "Verde-água", Chip: "bg-teal-100 text-teal-800", Dot: "bg-teal-500"},
	{Key: "green", Label: "Verde", Chip: "bg-green-100 text-green-800", Dot: "bg-green-500"},
	{Key: "amber", Label: "Âmbar", Chip: "bg-amber-100 text-amber-800", Dot: "bg-amber-500"},
	{Key: "orange", Label: "Laranja", Chip: "bg-orange-100 text-orange-800", Dot: "bg-orange-500"},
	{Key: "red", Label: "Vermelho", Chip: "bg-red-100 text-red-800", Dot: "bg-red-500"},
	{Key: "pink", Label: "Rosa", Chip: "bg-pink-100 text-pink-800", Dot: "bg-pink-500"},
	{Key: "gray", Label: "Cinza", Chip: "bg-stone-100 text-stone-700", Dot: "bg-stone-400"},
}

func tagColorStyle(color string) TagColorStyle {
	for _, style := range tagColorStyles {
		if style.Key == color {
			return style
		}
	}
	return tagColorStyle(repo.DefaultContactTagColor)
}

const conversationCardTagLimit = 2

func conversationCardTags(conversation repo.Conversation) []repo.ConversationTag {
	if len(conversation.Tags) > conversationCardTagLimit {
		return conversation.Tags[:conversationCardTagLimit]
	}
	return conversation.Tags
}

func conversationHiddenTagCount(conversation repo.Conversation) int {
	return max(len(conversation.Tags)-conversationCardTagLimit, 0)
}

func conversationTagNames(tags []repo.ConversationTag) string {
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		names = append(names, tag.Name)
	}
	return strings.Join(names, ", ")
}

func conversationTagsLabel(conversation repo.Conversation) string {
	return fmt.Sprintf("Tags: %s", conversationTagNames(conversation.Tags))
}

func conversationHiddenTagsLabel(conversation repo.Conversation) string {
	return conversationTagNames(conversation.Tags[conversationCardTagLimit:])
}

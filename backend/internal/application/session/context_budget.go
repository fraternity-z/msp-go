package session

import "strings"

const (
	// MaxChatMessageKiB is the semantic limit for one student chat message.
	// Keeping it below the model-input budget leaves room for recent history.
	MaxChatMessageKiB = 12

	maxChatMessageBytes    = MaxChatMessageKiB << 10
	maxChatModelInputBytes = 16 << 10
	maxChatHistoryMessages = 30
	// Keep transport-specific attachment rendering out of the application
	// budget contract while reserving enough space for labels and separators.
	chatAttachmentFramingAllowanceBytes = 256
)

// chatHistoryByteBudget returns the model-input bytes left for history after
// accounting for the dynamic system instruction, current message, and image
// attachment paths. The fixed agent instruction and generated response use the
// model capacity intentionally reserved above this application budget.
func chatHistoryByteBudget(message string, instruction string, attachments []string) (int, bool) {
	used := len(strings.TrimSpace(message)) + len(strings.TrimSpace(instruction))
	if len(attachments) > 0 {
		used += chatAttachmentFramingAllowanceBytes
		for _, attachment := range attachments {
			used += len(attachment)
		}
	}
	if used > maxChatModelInputBytes {
		return 0, false
	}
	return maxChatModelInputBytes - used, true
}

// selectRecentChatHistory keeps a chronological suffix of model-relevant
// messages. A user message followed by an assistant response is selected as one
// unit so trimming does not normally leave an answer without its question. A
// trailing user message remains eligible because it represents an interrupted
// previous turn. Individual message content is never truncated.
func selectRecentChatHistory(messages []Message, byteBudget int) []Message {
	if byteBudget <= 0 || len(messages) == 0 {
		return []Message{}
	}

	relevant := make([]Message, 0, len(messages))
	for _, message := range messages {
		if normalizedChatRole(message.Role) == "assistant" && message.Knowledge != nil && len(message.Knowledge.Citations) > 0 {
			continue
		}
		if chatHistoryMessageBytes(message) > 0 {
			relevant = append(relevant, message)
		}
	}
	if len(relevant) == 0 {
		return []Message{}
	}

	start := len(relevant)
	remaining := byteBudget
	for index := len(relevant) - 1; index >= 0; {
		turnStart := index
		if normalizedChatRole(relevant[index].Role) == "assistant" && index > 0 && normalizedChatRole(relevant[index-1].Role) == "user" {
			turnStart = index - 1
		}

		turnBytes := 0
		for messageIndex := turnStart; messageIndex <= index; messageIndex++ {
			turnBytes += chatHistoryMessageBytes(relevant[messageIndex])
		}
		if turnBytes > remaining {
			break
		}

		start = turnStart
		remaining -= turnBytes
		index = turnStart - 1
	}
	if start == len(relevant) {
		return []Message{}
	}

	selected := make([]Message, len(relevant)-start)
	copy(selected, relevant[start:])
	return selected
}

func chatHistoryMessageBytes(message Message) int {
	if normalizedChatRole(message.Role) == "" {
		return 0
	}
	return len(strings.TrimSpace(message.Content))
}

func normalizedChatRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user":
		return "user"
	case "assistant":
		return "assistant"
	default:
		return ""
	}
}

package tokens

import (
	"strings"
)

// EstimateTokens returns an estimate of the number of tokens in the given text.
func EstimateTokens(text string) int {
	words := strings.Fields(text)
	tokenCount := 0

	for _, word := range words {
		// Simple heuristic: longer words typically split into more tokens
		wordLen := len([]rune(word))
		switch {
		case wordLen <= 4:
			tokenCount += 1
		case wordLen <= 8:
			tokenCount += 2
		default:
			// For longer words, assume 1 token per 4 characters, rounded up
			tokenCount += (wordLen + 3) / 4
		}
	}

	return tokenCount
}

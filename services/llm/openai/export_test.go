package openai

import "sync"

// ResetReasoningEfforts forgets the reasoning efforts learned for models
func ResetReasoningEfforts() {
	reasoningEfforts = sync.Map{}
}

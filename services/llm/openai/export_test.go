package openai

// ResetLearned forgets what has been learned about models from the API rejecting requests
func ResetLearned() {
	reasoningEfforts.Clear()
	logprobsUnsupported.Clear()
}

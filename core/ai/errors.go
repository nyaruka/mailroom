package ai

const (
	ErrorCredentials = "credentials"
	ErrorRateLimit   = "ratelimit"
	ErrorReasoning   = "reasoning"
	ErrorUnknown     = "unknown"
)

type ServiceError struct {
	Message      string
	Code         string
	Instructions string
	Input        string
}

func (e *ServiceError) Error() string { return e.Message }

package mnotify

// BaseURL is our default base URL for mNotify channels (public for testing overriding)
var BaseURL = `https://api.mnotify.com/api`

const callPath = `/initiate-ivr-call`

// CallRequest is the body of a request to start an outgoing call
type CallRequest struct {
	FromNumber string `json:"fromNumber"`
	ToNumber   string `json:"toNumber"`
	UUID       string `json:"uuid"`
	UniqueID   string `json:"uniqueId"`
	Context    string `json:"context"`
}

// CallResponse is the response to a request to start an outgoing call
type CallResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// Playback plays an audio file to the caller
type Playback struct {
	URL string `json:"url"`
}

// GetDigits plays an audio file and collects DTMF input from the caller
type GetDigits struct {
	URL         string `json:"url,omitempty"`
	NumDigits   int    `json:"num_digits,omitempty"`
	Timeout     int    `json:"timeout,omitempty"` // in milliseconds
	FinishOnKey string `json:"finish_on_key,omitempty"`
	SkipAudio   bool   `json:"skip_audio,omitempty"`
}

// Record records audio from the caller
type Record struct {
	URL         string `json:"url,omitempty"`
	MaxDuration int    `json:"max_duration,omitempty"` // in seconds
	FinishOnKey string `json:"finish_on_key,omitempty"`
	PlayBeep    bool   `json:"play_beep"`
}

// Hangup ends the call
type Hangup struct{}

// Action is a single instruction to the provider. A response carries exactly one because its actions are keyed by
// name, which means it can neither repeat an action nor order several.
type Action struct {
	Playback  *Playback  `json:"playback,omitempty"`
	GetDigits *GetDigits `json:"get_digits,omitempty"`
	Record    *Record    `json:"record,omitempty"`
	Hangup    *Hangup    `json:"hangup,omitempty"`
}

// Response is what we write back in reply to a callback
type Response struct {
	Actions *Action `json:"actions"`
	Message string  `json:"message,omitempty"`
}

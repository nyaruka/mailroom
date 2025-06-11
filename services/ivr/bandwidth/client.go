package bandwidth

// BaseURL is our default base URL for Bandwith channels (public for testing overriding)
var BaseURL = `https://voice.bandwidth.com/api/v2`

type CallRequest struct {
	To               string `json:"to"`
	From             string `json:"from"`
	AnswerURL        string `json:"answerUrl"`
	DisconnectURL    string `json:"disconnectUrl"`
	ApplicationID    string `json:"applicationId"`
	MachineDetection *struct {
		Mode        string `json:"mode"`
		CallbackURL string `json:"callbackUrl"`
	} `json:"machineDetection"`
}

type CallResponse struct {
	CallID string `json:"callId"`
}

type SpeakSentence struct {
	XMLName string `xml:"SpeakSentence"`
	Text    string `xml:",chardata"`
	Locale  string `xml:"locale,attr,omitempty"`
}

type PlayAudio struct {
	XMLName string `xml:"PlayAudio"`
	URL     string `xml:",chardata"`
}

type Hangup struct {
	XMLName string `xml:"Hangup"`
}

type Redirect struct {
	XMLName string `xml:"Redirect"`
	URL     string `xml:"redirectUrl,attr"`
}

type PhoneNumber struct {
	XMLName string `xml:"PhoneNumber"`
	Number  string `xml:",chardata"`
}

type Transfer struct {
	XMLName      string        `xml:"Transfer"`
	URL          string        `xml:"transferCompleteUrl,attr"`
	Timeout      int           `xml:"callTimeout,attr,omitempty"`
	TimeLimit    int           `xml:"timeLimit,attr,omitempty"`
	PhoneNumbers []PhoneNumber `xml:",innerxml"`
}

type Gather struct {
	XMLName           string `xml:"Gather"`
	MaxDigits         int    `xml:"maxDigits,attr,omitempty"`
	TerminatingDigits string `xml:"terminatingDigits,attr,omitempty"`
	Timeout           int    `xml:"interDigitTimeout,attr,omitempty"`
	URL               string `xml:"gatherUrl,attr,omitempty"`
	Commands          []any  `xml:",innerxml"`
}

type Record struct {
	XMLName     string `xml:"Record"`
	URL         string `xml:"recordCompleteUrl,attr,omitempty"`
	MaxDuration int    `xml:"maxDuration,attr,omitempty"`
}

type Response struct {
	XMLName  string  `xml:"Response"`
	Message  string  `xml:",comment"`
	Gather   *Gather `xml:"Gather"`
	Commands []any   `xml:",innerxml"`
}

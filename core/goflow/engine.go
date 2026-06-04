package goflow

import (
	"context"
	"strings"
	"sync"
	"text/template"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/engine"
	"github.com/nyaruka/goflow/services/webhooks"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/shopspring/decimal"
)

var eng, simulator flows.Engine
var engInit, simulatorInit sync.Once

var checkSendable func(*runtime.Runtime) flows.CheckSendableCallback
var claimURN func(*runtime.Runtime) flows.ClaimURNCallback
var emailFactory func(*runtime.Runtime) engine.EmailServiceFactory
var llmFactory func(*runtime.Runtime) engine.LLMServiceFactory
var airtimeFactory func(*runtime.Runtime) engine.AirtimeServiceFactory
var llmPrompts map[string]*template.Template

func Reset() {
	engInit, eng = sync.Once{}, nil
	simulatorInit, simulator = sync.Once{}, nil
}

func RegisterCheckSendable(f func(*runtime.Runtime) flows.CheckSendableCallback) {
	checkSendable = f
}

func RegisterClaimURN(f func(*runtime.Runtime) flows.ClaimURNCallback) {
	claimURN = f
}

// RegisterEmailServiceFactory can be used by outside callers to register a email service factory
// for use by the engine
func RegisterEmailServiceFactory(f func(*runtime.Runtime) engine.EmailServiceFactory) {
	emailFactory = f
}

// RegisterLLMServiceFactory can be used by outside callers to register an LLM service factory
// for use by the engine
func RegisterLLMServiceFactory(f func(*runtime.Runtime) engine.LLMServiceFactory) {
	llmFactory = f
}

// RegisterAirtimeServiceFactory can be used by outside callers to register a airtime serivce factory
// for use by the engine
func RegisterAirtimeServiceFactory(f func(*runtime.Runtime) engine.AirtimeServiceFactory) {
	airtimeFactory = f
}

// RegisterAirtimeServiceFactory can be used by outside callers to register a airtime serivce factory
// for use by the engine
func RegisterLLMPrompts(p map[string]*template.Template) {
	llmPrompts = p
}

// userAgent returns the User-Agent header value for webhook calls. Only the major.minor
// portion of the version is included to avoid leaking specific build details.
func userAgent(version string) string {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) >= 2 {
		return "Mailroom/" + parts[0] + "." + parts[1]
	}
	return "Mailroom/" + version
}

// Engine returns the global engine instance for use with real sessions
func Engine(rt *runtime.Runtime) flows.Engine {
	engInit.Do(func() {
		webhookHeaders := map[string]string{
			"User-Agent":      userAgent(rt.Config.Version),
			"X-Mailroom-Mode": "normal",
		}

		eng = engine.NewBuilder().
			WithHTTPClient(rt.HTTP.Engine).
			WithWebhookServiceFactory(webhooks.NewServiceFactory(webhookHeaders, rt.Config.WebhooksMaxBodyBytes)).
			WithLLMServiceFactory(llmFactory(rt)).
			WithEmailServiceFactory(emailFactory(rt)).
			WithAirtimeServiceFactory(airtimeFactory(rt)).
			WithMaxStepsPerSprint(rt.Config.MaxStepsPerSprint).
			WithMaxSprintsPerSession(rt.Config.MaxSprintsPerSession).
			WithMaxFieldChars(rt.Config.MaxValueLength).
			WithMaxResultChars(rt.Config.MaxValueLength).
			WithLLMPrompts(llmPrompts).
			WithCheckSendable(checkSendable(rt)).
			WithClaimURN(claimURN(rt)).
			Build()
	})

	return eng
}

// Simulator returns the global engine instance for use with simulated sessions
func Simulator(ctx context.Context, rt *runtime.Runtime) flows.Engine {
	simulatorInit.Do(func() {
		webhookHeaders := map[string]string{
			"User-Agent":      userAgent(rt.Config.Version),
			"X-Mailroom-Mode": "simulation",
		}

		simulator = engine.NewBuilder().
			WithHTTPClient(rt.HTTP.Simulator).
			WithWebhookServiceFactory(webhooks.NewServiceFactory(webhookHeaders, rt.Config.WebhooksMaxBodyBytes)).
			WithLLMServiceFactory(llmFactory(rt)).                     // simulated sessions do real LLM calls
			WithEmailServiceFactory(simulatorEmailServiceFactory).     // but faked emails
			WithAirtimeServiceFactory(simulatorAirtimeServiceFactory). // and faked airtime transfers
			WithMaxStepsPerSprint(rt.Config.MaxStepsPerSprint).
			WithMaxSprintsPerSession(rt.Config.MaxSprintsPerSession).
			WithMaxFieldChars(rt.Config.MaxValueLength).
			WithMaxResultChars(rt.Config.MaxValueLength).
			WithLLMPrompts(llmPrompts).
			Build()
	})

	return simulator
}

func simulatorEmailServiceFactory(flows.SessionAssets) (flows.EmailService, error) {
	return &simulatorEmailService{}, nil
}

type simulatorEmailService struct{}

func (s *simulatorEmailService) Send(addresses []string, subject, body string) error {
	return nil
}

func simulatorAirtimeServiceFactory(flows.SessionAssets) (flows.AirtimeService, error) {
	return &simulatorAirtimeService{}, nil
}

type simulatorAirtimeService struct{}

func (s *simulatorAirtimeService) Create(ctx context.Context, transferUUID flows.EventUUID, sender urns.URN, recipient urns.URN, amounts map[string]decimal.Decimal, logHTTP flows.HTTPLogCallback) (*flows.AirtimeTransfer, error) {
	transfer := &flows.AirtimeTransfer{
		// fake but non-empty so simulated flows that branch on @results.airtime.external_id behave as
		// they would in production (where the real DT One service always populates this)
		ExternalID: "123456789",
		Sender:     sender,
		Recipient:  recipient,
		Amount:     decimal.Zero,
	}

	// pick arbitrary currency/amount pair in map
	for currency, amount := range amounts {
		transfer.Currency = currency
		transfer.Amount = amount
		break
	}

	return transfer, nil
}

func (s *simulatorAirtimeService) Confirm(ctx context.Context, transfer *flows.AirtimeTransfer, logHTTP flows.HTTPLogCallback) error {
	return nil
}

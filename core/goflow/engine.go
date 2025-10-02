package goflow

import (
	"context"
	"sync"
	"text/template"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/engine"
	"github.com/nyaruka/goflow/services/webhooks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/shopspring/decimal"
)

var eng, simulator flows.Engine
var engInit, simulatorInit sync.Once

var checkSendable func(*runtime.Runtime) flows.CheckSendableCallback
var emailFactory func(*runtime.Runtime) engine.EmailServiceFactory
var classificationFactory func(*runtime.Runtime) engine.ClassificationServiceFactory
var llmFactory func(*runtime.Runtime) engine.LLMServiceFactory
var airtimeFactory func(*runtime.Runtime) engine.AirtimeServiceFactory
var llmPrompts map[string]*template.Template

func Reset() {
	engInit, eng = sync.Once{}, nil
}

func RegisterCheckSendable(f func(*runtime.Runtime) flows.CheckSendableCallback) {
	checkSendable = f
}

// RegisterEmailServiceFactory can be used by outside callers to register a email service factory
// for use by the engine
func RegisterEmailServiceFactory(f func(*runtime.Runtime) engine.EmailServiceFactory) {
	emailFactory = f
}

// RegisterClassificationServiceFactory can be used by outside callers to register a classification service factory
// for use by the engine
func RegisterClassificationServiceFactory(f func(*runtime.Runtime) engine.ClassificationServiceFactory) {
	classificationFactory = f
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

// Engine returns the global engine instance for use with real sessions
func Engine(rt *runtime.Runtime) flows.Engine {
	engInit.Do(func() {
		webhookHeaders := map[string]string{
			"User-Agent":      "RapidProMailroom/" + rt.Config.Version,
			"X-Mailroom-Mode": "normal",
		}

		httpClient, httpAccess := HTTP(rt.Config)

		eng = engine.NewBuilder().
			WithWebhookServiceFactory(webhooks.NewServiceFactory(httpClient, nil, httpAccess, webhookHeaders, rt.Config.WebhooksMaxBodyBytes)).
			WithClassificationServiceFactory(classificationFactory(rt)).
			WithLLMServiceFactory(llmFactory(rt)).
			WithEmailServiceFactory(emailFactory(rt)).
			WithAirtimeServiceFactory(airtimeFactory(rt)).
			WithMaxStepsPerSprint(rt.Config.MaxStepsPerSprint).
			WithMaxSprintsPerSession(rt.Config.MaxSprintsPerSession).
			WithMaxFieldChars(rt.Config.MaxValueLength).
			WithMaxResultChars(rt.Config.MaxValueLength).
			WithLLMPrompts(llmPrompts).
			WithCheckSendable(checkSendable(rt)).
			Build()
	})

	return eng
}

// Simulator returns the global engine instance for use with simulated sessions
func Simulator(ctx context.Context, rt *runtime.Runtime) flows.Engine {
	simulatorInit.Do(func() {
		webhookHeaders := map[string]string{
			"User-Agent":      "RapidProMailroom/" + rt.Config.Version,
			"X-Mailroom-Mode": "simulation",
		}

		httpClient, httpAccess := HTTP(rt.Config) // don't do retries in simulator

		simulator = engine.NewBuilder().
			WithWebhookServiceFactory(webhooks.NewServiceFactory(httpClient, nil, httpAccess, webhookHeaders, rt.Config.WebhooksMaxBodyBytes)).
			WithClassificationServiceFactory(classificationFactory(rt)). // simulated sessions do real classification
			WithLLMServiceFactory(llmFactory(rt)).                       // simulated sessions do real LLM calls
			WithEmailServiceFactory(simulatorEmailServiceFactory).       // but faked emails
			WithAirtimeServiceFactory(simulatorAirtimeServiceFactory).   // and faked airtime transfers
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

func (s *simulatorAirtimeService) Transfer(ctx context.Context, sender urns.URN, recipient urns.URN, amounts map[string]decimal.Decimal, logHTTP flows.HTTPLogCallback) (*flows.AirtimeTransfer, error) {
	transfer := &flows.AirtimeTransfer{
		Sender:    sender,
		Recipient: recipient,
		Amount:    decimal.Zero,
	}

	// pick arbitrary currency/amount pair in map
	for currency, amount := range amounts {
		transfer.Currency = currency
		transfer.Amount = amount
		break
	}

	return transfer, nil
}

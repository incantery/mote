package provider

import (
	"errors"
	"os"
	"strings"
)

// The environment New reads when a Config leaves a field empty. They
// are the names the SDKs and the tools around them already use, so a
// machine set up for either one needs nothing new.
const (
	EnvAnthropicKey  = "ANTHROPIC_API_KEY"
	EnvAnthropicBase = "ANTHROPIC_BASE_URL"
	EnvOpenAIKey     = "OPENAI_API_KEY"
	EnvOpenAIBase    = "OPENAI_BASE_URL"
)

// Config is what New needs to choose. Every field may be empty: an
// empty one is read from the environment, and a Config with nothing
// in it but a model name is a working provider on a machine that has
// a key.
type Config struct {
	// Model is the profile's `model:` hint, and it is what decides
	// which provider answers. A name starting with "claude" asks for
	// the Anthropic one; anything else is OpenAI-compatible.
	Model string

	// AnthropicKey, else ANTHROPIC_API_KEY.
	AnthropicKey string
	// AnthropicBase, else ANTHROPIC_BASE_URL, else the API's own.
	AnthropicBase string
	// OpenAIKey, else OPENAI_API_KEY.
	OpenAIKey string
	// OpenAIBase, else OPENAI_BASE_URL, else https://api.openai.com/v1.
	OpenAIBase string
}

// New is the provider a profile asked for.
//
// The rule is one line: a model whose name starts with "claude", and
// an Anthropic key to call it with, gets the Anthropic provider;
// everything else gets the OpenAI-compatible one. That is deliberately
// less clever than it could be — a claude model with no Anthropic key
// is somebody's proxy, and going through it is the right answer
// rather than an error about a key they did not mean to use. The one
// thing added to the rule is the case where the OpenAI side has
// nothing at all to call and an Anthropic key is sitting there: that
// is a machine with one key on it, and a name it did not recognise.
//
// The model is remembered: the returned provider uses it for a
// Request that does not name one, so a harness that has read a
// profile does not repeat itself every turn.
func New(cfg Config) (Provider, error) {
	anthropicKey := pick(cfg.AnthropicKey, os.Getenv(EnvAnthropicKey))
	openAIKey := pick(cfg.OpenAIKey, os.Getenv(EnvOpenAIKey))
	openAIBase := pick(cfg.OpenAIBase, os.Getenv(EnvOpenAIBase))

	claude := strings.HasPrefix(strings.ToLower(cfg.Model), "claude")
	nowhereElse := openAIKey == "" && openAIBase == ""

	if anthropicKey != "" && (claude || nowhereElse) {
		a := NewAnthropic(pick(cfg.AnthropicBase, os.Getenv(EnvAnthropicBase)), anthropicKey)
		if cfg.Model != "" {
			a.Model = cfg.Model
		}
		return a, nil
	}
	if nowhereElse {
		return nil, errors.New("no provider for " + named(cfg.Model) + ": set " + wanted(claude))
	}
	o := NewOpenAI(openAIBase, openAIKey)
	o.Model = cfg.Model
	return o, nil
}

func named(model string) string {
	if model == "" {
		return "no model in particular"
	}
	return model
}

// wanted says which key would have worked, which is more use than
// saying that none did.
func wanted(claude bool) string {
	if claude {
		return EnvAnthropicKey + ", or " + EnvOpenAIKey + " and " + EnvOpenAIBase +
			" to reach it through something else"
	}
	return EnvOpenAIKey + ", or " + EnvOpenAIBase + " for an endpoint that needs no key"
}

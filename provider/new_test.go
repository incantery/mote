package provider

import (
	"strings"
	"testing"
)

// The profile's `model:` line is enough to choose, and the choice is
// one line of rule.
func TestNewChooses(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  Config
		env  map[string]string
		want string
	}{
		{"a claude model and a key",
			Config{Model: "claude-opus-5", AnthropicKey: "sk-ant"}, nil, "*provider.Anthropic"},
		{"the key from the environment",
			Config{Model: "claude-sonnet-5"}, map[string]string{EnvAnthropicKey: "sk-ant"}, "*provider.Anthropic"},
		{"a claude model through somebody's proxy",
			Config{Model: "claude-opus-5", OpenAIBase: "http://127.0.0.1:1234/v1"}, nil, "*provider.OpenAI"},
		{"anything else",
			Config{Model: "gpt-5", OpenAIKey: "sk-oai"}, nil, "*provider.OpenAI"},
		{"a local endpoint with no key at all",
			Config{Model: "qwen3", OpenAIBase: "http://127.0.0.1:1234/v1"}, nil, "*provider.OpenAI"},
		{"one key on the machine and a name it does not know",
			Config{Model: "something"}, map[string]string{EnvAnthropicKey: "sk-ant"}, "*provider.Anthropic"},
	} {
		t.Run(c.name, func(t *testing.T) {
			blank(t)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			p, err := New(c.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got := typeOf(p); got != c.want {
				t.Fatalf("chose %s, want %s", got, c.want)
			}
		})
	}
}

// The model the profile named is the model the provider uses when a
// Request does not name one.
func TestNewRemembersTheModel(t *testing.T) {
	blank(t)
	p, err := New(Config{Model: "claude-haiku-9", AnthropicKey: "sk-ant"})
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := p.(*Anthropic); !ok || a.Model != "claude-haiku-9" {
		t.Fatalf("provider is %+v", p)
	}
	p, err = New(Config{Model: "gpt-5", OpenAIKey: "sk-oai"})
	if err != nil {
		t.Fatal(err)
	}
	if o, ok := p.(*OpenAI); !ok || o.Model != "gpt-5" {
		t.Fatalf("provider is %+v", p)
	}
}

// A machine with nothing on it is told which key would have worked
// rather than that none did.
func TestNewWithNoKeyAnywhere(t *testing.T) {
	blank(t)
	_, err := New(Config{Model: "claude-opus-5"})
	if err == nil || !strings.Contains(err.Error(), EnvAnthropicKey) {
		t.Fatalf("error is %v", err)
	}
	_, err = New(Config{Model: "gpt-5"})
	if err == nil || !strings.Contains(err.Error(), EnvOpenAIKey) {
		t.Fatalf("error is %v", err)
	}
}

// blank takes the machine's own keys out of the way, so a test says
// the same thing on a laptop with keys as on one without.
func blank(t *testing.T) {
	t.Helper()
	for _, k := range []string{EnvAnthropicKey, EnvAnthropicBase, EnvOpenAIKey, EnvOpenAIBase} {
		t.Setenv(k, "")
	}
}

func typeOf(p Provider) string {
	switch p.(type) {
	case *Anthropic:
		return "*provider.Anthropic"
	case *OpenAI:
		return "*provider.OpenAI"
	}
	return "unknown"
}

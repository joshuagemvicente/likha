package model

import "strings"

// Provider describes one entry of the predefined accepted provider list
// (specs/v1-spec.md section 3). Adding a provider is a table row; adding a
// non-OpenAI-compatible protocol would be a new client surface, which v1 does
// not include.
type Provider struct {
	Name          string   // canonical name used with --provider and stored keys
	DisplayName   string   // human-readable name shown in the interface
	BaseURL       string   // OpenAI-compatible /v1-style base URL for chat completions
	KeyEnv        string   // dedicated environment variable for the provider's API key
	DefaultModel  string   // model used when the user supplies none; "" requires --model
	Hosted        bool     // hosted providers require a user-supplied API key
	SessionHeader string   // header carrying the per-conversation session ID, if the provider requires one
	Auth          AuthKind // authentication kind; zero value is a user-supplied API key
}

// AuthKind selects how Likha authenticates to a provider. The zero value is
// the classic BYOK key so every existing provider row is unchanged.
type AuthKind uint8

const (
	// AuthAPIKey sends the user's static API key as a Bearer credential.
	AuthAPIKey AuthKind = iota
	// AuthOAuth authenticates through an OAuth 2 login (ChatGPT Plus/Pro):
	// no API key exists, access tokens expire and refresh automatically,
	// and the credential is an OAuthCredentials value, not a string.
	AuthOAuth
)

// Providers is the predefined hosted-provider list. Rows without a completed
// admission probe are pending-probe and must not be described as accepted.
// Likha hosts no models and does not bundle a local inference server.
var Providers = []Provider{
	{Name: "openai", DisplayName: "OpenAI", BaseURL: "https://api.openai.com/v1", KeyEnv: "LIKHA_OPENAI_API_KEY", DefaultModel: "gpt-4o-mini", Hosted: true},
	{Name: "openrouter", DisplayName: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", KeyEnv: "LIKHA_OPENROUTER_API_KEY", DefaultModel: "openai/gpt-4o-mini", Hosted: true},
	{Name: "bedrock", DisplayName: "Amazon Bedrock", BaseURL: "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1", KeyEnv: "LIKHA_BEDROCK_API_KEY", DefaultModel: "anthropic.claude-3-5-haiku-20241022-v1:0", Hosted: true},
	{Name: "dialagram", DisplayName: "Dialagram", BaseURL: "https://dialagram.me/router/v1", KeyEnv: "LIKHA_DIALAGRAM_API_KEY", Hosted: true},
	{Name: "opencode-zen", DisplayName: "Opencode Zen", BaseURL: "https://opencode.ai/zen/v1", KeyEnv: "LIKHA_OPENCODE_ZEN_API_KEY", DefaultModel: "gpt-5.3-codex", Hosted: true, SessionHeader: "x-opencode-session"},
	{Name: "opencode-go", DisplayName: "Opencode Go", BaseURL: "https://opencode.ai/zen/go/v1", KeyEnv: "LIKHA_OPENCODEGO_API_KEY", DefaultModel: "glm-5.3-flash", Hosted: true, SessionHeader: "x-opencode-session"},
	{Name: "chatgpt", DisplayName: "ChatGPT (Plus/Pro)", BaseURL: "https://chatgpt.com/backend-api/codex", DefaultModel: "gpt-5.5", Hosted: true, SessionHeader: "session-id", Auth: AuthOAuth},
	{Name: "groq", DisplayName: "Groq", BaseURL: "https://api.groq.com/openai/v1", KeyEnv: "LIKHA_GROQ_API_KEY", Hosted: true},
	{Name: "xai", DisplayName: "xAI", BaseURL: "https://api.x.ai/v1", KeyEnv: "LIKHA_XAI_API_KEY", Hosted: true},
	{Name: "together", DisplayName: "Together AI", BaseURL: "https://api.together.ai/v1", KeyEnv: "LIKHA_TOGETHER_API_KEY", Hosted: true},
	{Name: "mistral", DisplayName: "Mistral AI", BaseURL: "https://api.mistral.ai/v1", KeyEnv: "LIKHA_MISTRAL_API_KEY", Hosted: true},
	{Name: "cerebras", DisplayName: "Cerebras", BaseURL: "https://api.cerebras.ai/v1", KeyEnv: "LIKHA_CEREBRAS_API_KEY", Hosted: true},
}

// ChatGPT OAuth 2 login constants. They follow the flow OpenCode and other
// agent harnesses use against OpenAI's Codex backend: the public PKCE client
// of the Codex CLI, a fixed loopback callback port, and the ChatGPT plan
// endpoint. The client ID is OpenAI's public native-application client; the
// login screen identifies it as such. Using a ChatGPT subscription through a
// third-party harness is subject to OpenAI's consumer terms.
const (
	ChatGPTIssuer       = "https://auth.openai.com"
	ChatGPTClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	ChatGPTCallbackPort = 1455
	ChatGPTOriginator   = "likha"
	// ChatGPTDevicePath is where the user enters the headless device code.
	ChatGPTDevicePath = "/codex/device"
)

// OAuthCredentials is the stored token set of an OAuth provider login.
// It persists in the private state directory (providers.json, 0600), never
// in the repository or session database.
type OAuthCredentials struct {
	Refresh   string `json:"refresh"`
	Access    string `json:"access"`
	Expires   int64  `json:"expires"` // Unix milliseconds; <= now means expired
	AccountID string `json:"account_id,omitempty"`
}

// ChatGPTModels is the curated model list for the ChatGPT provider. The
// Codex backend has no OpenAI-compatible model-list route, so first-run
// setup offers this documented list instead of fetching one.
var ChatGPTModels = []string{
	"gpt-5.5",
	"gpt-5.4",
	"gpt-5.4-mini",
	"gpt-5.3-codex-spark",
	"gpt-6-sol",
	"gpt-6-luna",
}

// LookupProvider resolves a canonical provider name, case-insensitively.
func LookupProvider(name string) (Provider, bool) {
	for _, p := range Providers {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return Provider{}, false
}

// ProviderBaseURLs returns the base URL of every listed provider.
func ProviderBaseURLs() []string {
	urls := make([]string, 0, len(Providers))
	for _, p := range Providers {
		urls = append(urls, p.BaseURL)
	}
	return urls
}

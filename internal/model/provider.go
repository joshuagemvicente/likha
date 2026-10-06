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
	{Name: "chatgpt", DisplayName: "ChatGPT (Plus/Pro)", BaseURL: ChatGPTResource, Hosted: true, Auth: AuthOAuth},
	{Name: "groq", DisplayName: "Groq", BaseURL: "https://api.groq.com/openai/v1", KeyEnv: "LIKHA_GROQ_API_KEY", Hosted: true},
	{Name: "xai", DisplayName: "xAI", BaseURL: "https://api.x.ai/v1", KeyEnv: "LIKHA_XAI_API_KEY", Hosted: true},
	{Name: "together", DisplayName: "Together AI", BaseURL: "https://api.together.ai/v1", KeyEnv: "LIKHA_TOGETHER_API_KEY", Hosted: true},
	{Name: "mistral", DisplayName: "Mistral AI", BaseURL: "https://api.mistral.ai/v1", KeyEnv: "LIKHA_MISTRAL_API_KEY", Hosted: true},
	{Name: "cerebras", DisplayName: "Cerebras", BaseURL: "https://api.cerebras.ai/v1", KeyEnv: "LIKHA_CEREBRAS_API_KEY", Hosted: true},
	{Name: "claude", DisplayName: "Anthropic Claude", BaseURL: "https://api.anthropic.com/v1", KeyEnv: "LIKHA_CLAUDE_API_KEY", DefaultModel: "claude-opus-5-5", Hosted: true},
	{Name: "deepseek", DisplayName: "DeepSeek", BaseURL: "https://api.deepseek.com/v1", KeyEnv: "LIKHA_DEEPSEEK_API_KEY", DefaultModel: "deepseek-flash", Hosted: true},
	{Name: "gemini", DisplayName: "Google Gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", KeyEnv: "LIKHA_GEMINI_API_KEY", DefaultModel: "gemini-2.5-flash", Hosted: true},
}

// ChatGPT's official Sign in with ChatGPT public-client endpoints.
// ChatGPTClientID is only the initial registration entry point. Token grants
// and subsequent sign-ins use the client ID issued for that registration.
const (
	ChatGPTIssuer       = "https://auth.openai.com"
	ChatGPTClientID     = "dynamic_agent_client"
	ChatGPTResource     = "https://api.openai.com/v1"
	ChatGPTCallbackPort = 0 // use the actual port allocated to the listener
	ChatGPTPlanScope    = "chatgpt.tokens.use.direct"
	// Deprecated: public API requests do not need an originator header.
	ChatGPTOriginator = "likha"
)

// OAuthCredentials is the stored token set of an OAuth provider login.
// It persists in the private state directory (providers.json, 0600), never
// in the repository or session database.
type OAuthCredentials struct {
	Refresh   string   `json:"refresh"`
	Access    string   `json:"access"`
	Expires   int64    `json:"expires"`              // Unix milliseconds; <= now means expired
	AccountID string   `json:"account_id,omitempty"` // legacy only, never an OIDC identity
	Issuer    string   `json:"issuer,omitempty"`
	Subject   string   `json:"subject,omitempty"`
	Email     string   `json:"email,omitempty"`
	ClientID  string   `json:"client_id,omitempty"`
	HostID    string   `json:"ext_agent_host_id,omitempty"`
	IDToken   string   `json:"id_token,omitempty"`
	TokenType string   `json:"token_type,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
}

// HasPlanScope reports the server-granted permission to use the ChatGPT plan.
// Identity scopes alone do not authorize inference.
func (c OAuthCredentials) HasPlanScope() bool {
	for _, scope := range c.Scopes {
		if scope == ChatGPTPlanScope {
			return true
		}
	}
	return false
}

// Registered distinguishes a retained, verified SIWC registration from legacy
// Codex credentials and the first-time registration entry point.
func (c OAuthCredentials) Registered() bool {
	return c.ClientID != "" && c.ClientID != ChatGPTClientID &&
		c.Subject != "" && c.Issuer != "" && c.HostID != ""
}

// Deprecated: discover ChatGPT models at the public resource's /models route.
// This empty compatibility symbol is not a discovery source.
var ChatGPTModels = []string{}

var ClaudeModels = []string{
	"claude-opus-5-5",
	"claude-fable-5-1",
	"claude-fable-5",
	"claude-sonnet-5-5",
	"claude-haiku-4-5",
	"claude-opus-4-7",
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

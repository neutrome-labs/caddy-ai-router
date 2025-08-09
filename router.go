package server

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/neutrome-labs/caddy-ai-router/pkg/auth"
	"github.com/neutrome-labs/caddy-ai-router/pkg/common"
	"github.com/neutrome-labs/caddy-ai-router/pkg/providers"
	"go.uber.org/zap"
)

const APP_VERSION = "0.3.0"

const (
	UserIDContextKeyString                 string = "ai_user_id"
	ApiKeyIDContextKeyString               string = "ai_api_key_id"
	ExternalAPIKeyProviderContextKeyString string = "ai_external_api_key_provider"
	ProviderNameContextKeyString           string = "ai_provider_name"
	ActualModelNameContextKeyString        string = "ai_actual_model_name"
)

func init() {
	caddy.RegisterModule(&AICoreRouter{})
	httpcaddyfile.RegisterHandlerDirective("ai_router", parseAIRouterCaddyfile)
	// New decoupled endpoint handlers
	caddy.RegisterModule(ModelsEndpointHandler{})
	caddy.RegisterModule(ChatCompletionsHandler{})
	httpcaddyfile.RegisterHandlerDirective("ai_models", parseModelsHandlerCaddyfile)
	httpcaddyfile.RegisterHandlerDirective("ai_chat_completions", parseChatHandlerCaddyfile)
}

type AICoreRouter struct {
	// Optional name to reference this router from other handlers (defaults to "default")
	Name                    string                     `json:"name,omitempty"`
	Providers               map[string]*ProviderConfig `json:"providers,omitempty"`
	DefaultProviderForModel map[string][]string        `json:"default_provider_for_model,omitempty"`
	ProviderOrder           []string                   `json:"provider_order,omitempty"`

	logger     *zap.Logger
	mu         sync.RWMutex
	httpClient *http.Client

	knownModelsCache *sync.Map
}

type ProviderConfig struct {
	Name       string `json:"-"`
	APIBaseURL string `json:"api_base_url,omitempty"`
	Style      string `json:"style,omitempty"`
	Provider   providers.Provider
	proxy      *httputil.ReverseProxy
	parsedURL  *url.URL
}

func (*AICoreRouter) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.ai_router",
		New: func() caddy.Module { return new(AICoreRouter) },
	}
}

func (cr *AICoreRouter) Provision(ctx caddy.Context) error {
	cr.logger = ctx.Logger(cr)
	cr.httpClient = &http.Client{Timeout: 15 * time.Second}
	cr.knownModelsCache = &sync.Map{}
	cr.mu.Lock()
	defer cr.mu.Unlock()

	if strings.TrimSpace(cr.Name) == "" {
		cr.Name = "default"
	}

	if common.TryInstrumentAppObservability() {
		cr.logger.Info("PostHog observability instrumentation enabled")
	} else {
		cr.logger.Warn("Failed to initialize PostHog observability instrumentation, skipping")
	}

	if cr.Providers == nil {
		cr.Providers = make(map[string]*ProviderConfig)
	}
	if cr.DefaultProviderForModel == nil {
		cr.DefaultProviderForModel = make(map[string][]string)
	}

	for _, name := range cr.ProviderOrder {
		p := cr.Providers[name]
		p.Name = name
		if p.APIBaseURL == "" {
			return fmt.Errorf("provider %s: api_base_url is required", name)
		}
		parsedURL, err := url.Parse(p.APIBaseURL)
		if err != nil {
			return fmt.Errorf("provider %s: invalid api_base_url '%s': %v", name, p.APIBaseURL, err)
		}
		p.parsedURL = parsedURL

		switch p.Style {
		case "google":
			p.Provider = &providers.GoogleProvider{}
		case "anthropic":
			p.Provider = &providers.AnthropicProvider{}
		case "cloudflare":
			p.Provider = &providers.CloudflareProvider{}
		default:
			p.Provider = &providers.OpenAIProvider{}
		}

		p.proxy = &httputil.ReverseProxy{
			Director:       cr.getDirector(p),
			ModifyResponse: cr.getModifyResponse(p),
			ErrorHandler:   cr.getErrorHandler(p),
		}
		cr.logger.Info("Provisioned provider for core router", zap.String("name", name), zap.String("base_url", p.APIBaseURL))
	}

	for model, providerNames := range cr.DefaultProviderForModel {
		for _, providerName := range providerNames {
			if _, ok := cr.Providers[providerName]; !ok {
				return fmt.Errorf("default provider '%s' for model '%s' is not a configured provider", providerName, model)
			}
		}
	}

	cr.logger.Info("AI Core Router provisioned",
		zap.String("version", APP_VERSION),
		zap.Int("num_providers", len(cr.Providers)),
		zap.Int("num_model_defaults", len(cr.DefaultProviderForModel)),
	)

	// Make this router discoverable by endpoint handlers
	registerRouter(cr.Name, cr)

	common.FireObservabilityEvent("system", "", "router_start", map[string]any{
		"version":            APP_VERSION,
		"num_providers":      len(cr.Providers),
		"num_model_defaults": len(cr.DefaultProviderForModel),
	})

	return nil
}

func (cr *AICoreRouter) Validate() error {
	cr.mu.RLock()
	defer cr.mu.RUnlock()

	if len(cr.Providers) == 0 {
		return fmt.Errorf("at least one provider must be configured for ai_core_router")
	}
	return nil
}

func (cr *AICoreRouter) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	// No-op handler; exists only to provision router config at top-level.
	// Always pass through.
	return next.ServeHTTP(w, r)
}

func (cr *AICoreRouter) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	if cr.Providers == nil {
		cr.Providers = make(map[string]*ProviderConfig)
	}
	if cr.DefaultProviderForModel == nil {
		cr.DefaultProviderForModel = make(map[string][]string)
	}
	if cr.ProviderOrder == nil {
		cr.ProviderOrder = []string{}
	}

	for d.Next() {
		if d.Val() == "ai_router" && !d.NextArg() {
		}
		for d.NextBlock(0) {
			switch d.Val() {
			case "name":
				if !d.NextArg() {
					return d.ArgErr()
				}
				cr.Name = strings.ToLower(strings.TrimSpace(d.Val()))
			case "provider":
				if !d.NextArg() {
					return d.ArgErr()
				}
				providerName := strings.ToLower(d.Val())
				if _, ok := cr.Providers[providerName]; ok {
					return d.Errf("provider %s already defined", providerName)
				}
				p := &ProviderConfig{Name: providerName}
				for d.NextBlock(1) {
					switch d.Val() {
					case "api_base_url":
						if !d.NextArg() {
							return d.ArgErr()
						}
						p.APIBaseURL = d.Val()
					case "style":
						if !d.NextArg() {
							return d.ArgErr()
						}
						p.Style = strings.ToLower(d.Val())
					default:
						return d.Errf("unrecognized provider option '%s' for provider '%s'", d.Val(), providerName)
					}
				}
				if p.APIBaseURL == "" {
					return d.Errf("provider %s: api_base_url is required", providerName)
				}
				cr.Providers[providerName] = p
				cr.ProviderOrder = append(cr.ProviderOrder, providerName)
			case "default_provider_for_model":
				args := d.RemainingArgs()
				if len(args) < 2 {
					return d.Errf("default_provider_for_model expects <model_name> <provider_name_1> [<provider_name_2>...], got %d args", len(args))
				}
				modelName := args[0]
				providerNames := []string{}
				for _, pName := range args[1:] {
					providerNames = append(providerNames, strings.ToLower(pName))
				}
				cr.DefaultProviderForModel[modelName] = providerNames
			default:
				return d.Errf("unrecognized ai_core_router option '%s'", d.Val())
			}
		}
	}
	return nil
}

func (cr *AICoreRouter) getDirector(p *ProviderConfig) func(req *http.Request) {
	return func(r *http.Request) {
		r.URL.Scheme = p.parsedURL.Scheme
		r.URL.Host = p.parsedURL.Host
		r.URL.Path = p.parsedURL.Path
		r.Host = p.parsedURL.Host
		r.Header.Del("X-Forwarded-Proto")

		modelName, _ := r.Context().Value(ActualModelNameContextKeyString).(string)

		if p.Provider != nil {
			if err := p.Provider.ModifyCompletionRequest(r, modelName, cr.logger); err != nil {
				cr.logger.Error("failed to modify request", zap.Error(err), zap.String("provider", p.Name))
			}
		}

		cr.logger.Info("Proxying request to provider",
			zap.String("provider", p.Name),
			zap.String("target_url", r.URL.String()),
			zap.String("model", modelName),
		)

		reqCtx := r.Context()

		userIDVal := reqCtx.Value(UserIDContextKeyString)
		apiKeyIDVal := reqCtx.Value(ApiKeyIDContextKeyString)
		userID, _ := userIDVal.(string)
		apiKeyID, _ := apiKeyIDVal.(string)

		common.FireObservabilityEvent(userID, "", "inference_proxy_request", map[string]any{
			"$ip":        r.RemoteAddr,
			"provider":   r.Context().Value(ProviderNameContextKeyString).(string),
			"model":      r.Context().Value(ActualModelNameContextKeyString).(string),
			"user_id":    userID,
			"api_key_id": apiKeyID,
		})
	}
}

func (cr *AICoreRouter) getModifyResponse(p *ProviderConfig) func(resp *http.Response) error {
	return func(resp *http.Response) error {
		if p.Provider != nil {
			if resp.Header.Get("X-Provider-Name") == "" {
				modelName, _ := resp.Request.Context().Value(ActualModelNameContextKeyString).(string)
				resp.Header.Set("X-Provider-Name", p.Name)
				resp.Header.Set("X-Model-Name", modelName)

				userID, _ := resp.Request.Context().Value(UserIDContextKeyString).(string)
				apiKeyID, _ := resp.Request.Context().Value(ApiKeyIDContextKeyString).(string)

				body := ""
				if resp.StatusCode >= 299 {
					bodyBytes, err := httputil.DumpResponse(resp, false)
					if err == nil {
						body = string(bodyBytes)
					}
				}

				common.FireObservabilityEvent(userID, "", "inference_proxy_response", map[string]any{
					"$ip":          resp.Request.RemoteAddr,
					"status_code":  resp.StatusCode,
					"content_type": resp.Header.Get("Content-Type"),
					"body":         body, // todo: if OBSERVE_PROXY_RESPONSE_BODY
					"provider":     p.Name,
					"model":        modelName,
					"user_id":      userID,
					"api_key_id":   apiKeyID,
				})
			}
			if err := p.Provider.ModifyCompletionResponse(nil, nil, resp, cr.logger); err != nil {
				cr.logger.Error("failed to modify response", zap.Error(err), zap.String("provider", p.Name))
			}
		}
		return nil
	}
}

func (cr *AICoreRouter) getErrorHandler(p *ProviderConfig) func(rw http.ResponseWriter, r *http.Request, err error) {
	return func(rw http.ResponseWriter, r *http.Request, err error) {
		urlWithoutQs := r.URL.String()
		if r.URL.RawQuery != "" {
			urlWithoutQs = urlWithoutQs[:len(urlWithoutQs)-len(r.URL.RawQuery)-1]
		}

		cr.logger.Error("Upstream proxy error",
			zap.String("provider", p.Name),
			zap.String("target_url", urlWithoutQs),
			zap.Error(err),
		)

		reqCtx := r.Context()

		userIDVal := reqCtx.Value(UserIDContextKeyString)
		apiKeyIDVal := reqCtx.Value(ApiKeyIDContextKeyString)
		userID, _ := userIDVal.(string)
		apiKeyID, _ := apiKeyIDVal.(string)

		common.FireObservabilityEvent(userID, urlWithoutQs, "$exception", map[string]any{
			"$exception_list": []map[string]any{
				{
					"type":  "ProxyError",
					"value": err.Error(),
					"mechanism": map[string]any{
						"handled":   true,
						"synthetic": false,
					},
				},
			},
			"provider":   r.Context().Value(ProviderNameContextKeyString).(string),
			"model":      r.Context().Value(ActualModelNameContextKeyString).(string),
			"user_id":    userID,
			"api_key_id": apiKeyID,
		})

		http.Error(rw, fmt.Sprintf("Error proxying to upstream provider %s: %v", p.Name, err), http.StatusBadGateway)
	}
}

func parseAIRouterCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var cr AICoreRouter
	err := cr.UnmarshalCaddyfile(h.Dispenser)
	if err != nil {
		return nil, err
	}
	return &cr, nil
}

var (
	_ caddy.Provisioner           = (*AICoreRouter)(nil)
	_ caddy.Validator             = (*AICoreRouter)(nil)
	_ caddyhttp.MiddlewareHandler = (*AICoreRouter)(nil)
	_ caddyfile.Unmarshaler       = (*AICoreRouter)(nil)
)

// --- Shared registry and decoupled endpoint handlers ---

// simple in-process registry mapping router names to instances
var routerRegistry sync.Map // map[string]*AICoreRouter

func registerRouter(name string, r *AICoreRouter) {
	routerRegistry.Store(strings.ToLower(name), r)
}

func getRouter(name string) (*AICoreRouter, bool) {
	if strings.TrimSpace(name) == "" {
		name = "default"
	}
	if v, ok := routerRegistry.Load(strings.ToLower(name)); ok {
		if cr, ok2 := v.(*AICoreRouter); ok2 {
			return cr, true
		}
	}
	return nil, false
}

// ModelsEndpointHandler serves aggregated models under any path.
type ModelsEndpointHandler struct {
	Router string `json:"router,omitempty"`
	logger *zap.Logger
}

func (ModelsEndpointHandler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.ai_models",
		New: func() caddy.Module { return new(ModelsEndpointHandler) },
	}
}

func (h *ModelsEndpointHandler) Provision(ctx caddy.Context) error {
	h.logger = ctx.Logger(h)
	return nil
}

func (h *ModelsEndpointHandler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	cr, ok := getRouter(h.Router)
	if !ok {
		http.Error(w, fmt.Sprintf("ai_models: router '%s' not found", h.Router), http.StatusInternalServerError)
		return nil
	}

	// Fire a pageview event for observability (without query string)
	urlWithoutQs := r.URL.String()
	if r.URL.RawQuery != "" {
		urlWithoutQs = urlWithoutQs[:len(urlWithoutQs)-len(r.URL.RawQuery)-1]
	}
	common.FireObservabilityEvent("system", urlWithoutQs, "$pageview", map[string]any{
		"$ip": r.RemoteAddr,
	})

	// Discover API key provider from context if present
	var apiKeyService auth.ExternalAPIKeyProvider
	if val := r.Context().Value(ExternalAPIKeyProviderContextKeyString); val != nil {
		if svc, ok := val.(auth.ExternalAPIKeyProvider); ok {
			apiKeyService = svc
		}
	}
	if apiKeyService == nil {
		apiKeyService = auth.NewDefaultEnvAPIKeyProvider(cr.logger)
	}

	if r.Method == http.MethodGet {
		return cr.handleGetManagedModels(w, r, next, apiKeyService)
	}
	// Not our method; pass through
	return next.ServeHTTP(w, r)
}

func parseModelsHandlerCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var mh ModelsEndpointHandler
	for h.Next() {
		for h.NextBlock(0) {
			switch h.Val() {
			case "router":
				if !h.NextArg() {
					return nil, h.ArgErr()
				}
				mh.Router = h.Val()
			default:
				return nil, h.Errf("unrecognized ai_models option '%s'", h.Val())
			}
		}
	}
	return &mh, nil
}

// ChatCompletionsHandler serves chat completions under any path.
type ChatCompletionsHandler struct {
	Router string `json:"router,omitempty"`
	logger *zap.Logger
}

func (ChatCompletionsHandler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.ai_chat_completions",
		New: func() caddy.Module { return new(ChatCompletionsHandler) },
	}
}

func (h *ChatCompletionsHandler) Provision(ctx caddy.Context) error {
	h.logger = ctx.Logger(h)
	return nil
}

func (h *ChatCompletionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	cr, ok := getRouter(h.Router)
	if !ok {
		http.Error(w, fmt.Sprintf("ai_chat_completions: router '%s' not found", h.Router), http.StatusInternalServerError)
		return nil
	}

	// Fire a pageview event for observability (without query string)
	urlWithoutQs := r.URL.String()
	if r.URL.RawQuery != "" {
		urlWithoutQs = urlWithoutQs[:len(urlWithoutQs)-len(r.URL.RawQuery)-1]
	}
	common.FireObservabilityEvent("system", urlWithoutQs, "$pageview", map[string]any{
		"$ip": r.RemoteAddr,
	})

	var apiKeyService auth.ExternalAPIKeyProvider
	if val := r.Context().Value(ExternalAPIKeyProviderContextKeyString); val != nil {
		if svc, ok := val.(auth.ExternalAPIKeyProvider); ok {
			apiKeyService = svc
		}
	}
	if apiKeyService == nil {
		apiKeyService = auth.NewDefaultEnvAPIKeyProvider(cr.logger)
	}

	if r.Method == http.MethodPost {
		return cr.handlePostInferenceRequest(w, r, next, apiKeyService)
	}
	return next.ServeHTTP(w, r)
}

func parseChatHandlerCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var ch ChatCompletionsHandler
	for h.Next() {
		for h.NextBlock(0) {
			switch h.Val() {
			case "router":
				if !h.NextArg() {
					return nil, h.ArgErr()
				}
				ch.Router = h.Val()
			default:
				return nil, h.Errf("unrecognized ai_chat_completions option '%s'", h.Val())
			}
		}
	}
	return &ch, nil
}

var (
	_ caddy.Provisioner           = (*ModelsEndpointHandler)(nil)
	_ caddyhttp.MiddlewareHandler = (*ModelsEndpointHandler)(nil)
	_ caddy.Provisioner           = (*ChatCompletionsHandler)(nil)
	_ caddyhttp.MiddlewareHandler = (*ChatCompletionsHandler)(nil)
)

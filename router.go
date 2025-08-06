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

const APP_VERSION = "0.2.0"

const (
	UserIDContextKeyString                 string = "ai_user_id"
	ApiKeyIDContextKeyString               string = "ai_api_key_id"
	ExternalAPIKeyProviderContextKeyString string = "ai_external_api_key_provider"
	ProviderNameContextKeyString           string = "ai_provider_name"
	ActualModelNameContextKeyString        string = "ai_actual_model_name"
)

func init() {
	caddy.RegisterModule(AICoreRouter{})
	httpcaddyfile.RegisterHandlerDirective("ai_core_router", parseAICoreRouterCaddyfile)
}

type AICoreRouter struct {
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

func (AICoreRouter) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.ai_core_router",
		New: func() caddy.Module { return new(AICoreRouter) },
	}
}

func (cr *AICoreRouter) Provision(ctx caddy.Context) error {
	cr.logger = ctx.Logger(cr)
	cr.httpClient = &http.Client{Timeout: 15 * time.Second}
	cr.knownModelsCache = &sync.Map{}
	cr.mu.Lock()
	defer cr.mu.Unlock()

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
	// This logic will be simplified as the handlers will be moved into this package.
	// For now, we'll keep the existing logic.
	cr.logger.Debug("ServeHTTP invoked in ai_core_router",
		zap.String("method", r.Method),
		zap.String("host", r.Host),
		zap.String("uri", r.RequestURI),
		zap.String("path", r.URL.Path),
	)

	reqCtx := r.Context()

	var apiKeyService auth.ExternalAPIKeyProvider
	apiKeyServiceVal := reqCtx.Value("ai_external_api_key_provider")
	if apiKeyServiceVal != nil {
		var ok bool
		apiKeyService, ok = apiKeyServiceVal.(auth.ExternalAPIKeyProvider)
		if !ok {
			cr.logger.Warn("Value found for ExternalAPIKeyProviderContextKeyString but type assertion to ExternalAPIKeyProvider failed.")
		}
	}

	if apiKeyService == nil {
		apiKeyService = auth.NewDefaultEnvAPIKeyProvider(cr.logger)
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	urlWithoutQs := scheme + "://" + r.Host + "/<prefix>" + r.URL.Path
	defer func() {
		common.FireObservabilityEvent("system", urlWithoutQs, "$pageview", map[string]any{
			"$ip": r.RemoteAddr,
		})
	}()

	if r.Method == http.MethodGet && r.URL.Path == "/models" {
		return cr.handleGetManagedModels(w, r.WithContext(reqCtx), next, apiKeyService)
	}

	if r.Method == http.MethodPost && r.URL.Path == "/chat/completions" {
		return cr.handlePostInferenceRequest(w, r.WithContext(reqCtx), next, apiKeyService)
	}

	return next.ServeHTTP(w, r.WithContext(reqCtx))
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
		if d.Val() == "ai_core_router" && !d.NextArg() {
		}
		for d.NextBlock(0) {
			switch d.Val() {
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

func parseAICoreRouterCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
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

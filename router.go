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
	"github.com/neutrome-labs/caddy-ai-router/pkg/providers"
	"go.uber.org/zap"
)

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
	DefaultProviderForModel map[string]string          `json:"default_provider_for_model,omitempty"`
	SuperDefaultProvider    string                     `json:"super_default_provider,omitempty"`

	logger     *zap.Logger
	mu         sync.RWMutex
	httpClient *http.Client
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
	cr.mu.Lock()
	defer cr.mu.Unlock()

	if cr.Providers == nil {
		cr.Providers = make(map[string]*ProviderConfig)
	}
	if cr.DefaultProviderForModel == nil {
		cr.DefaultProviderForModel = make(map[string]string)
	}

	for name, p := range cr.Providers {
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
			Director: func(req *http.Request) {
				req.URL.Scheme = p.parsedURL.Scheme
				req.URL.Host = p.parsedURL.Host
				req.URL.Path = p.parsedURL.Path
				req.Host = p.parsedURL.Host
				req.Header.Del("X-Forwarded-Proto")

				modelName, _ := req.Context().Value(ActualModelNameContextKeyString).(string)

				if p.Provider != nil {
					if err := p.Provider.ModifyCompletionRequest(req, modelName, cr.logger); err != nil {
						cr.logger.Error("failed to modify request", zap.Error(err), zap.String("provider", p.Name))
					}
				}

				cr.logger.Info("Proxying request to provider",
					zap.String("provider", p.Name),
					zap.String("target_url", req.URL.String()),
					zap.String("model", modelName),
				)
			},
			ModifyResponse: func(resp *http.Response) error {
				if p.Provider != nil {
					if err := p.Provider.ModifyCompletionResponse(nil, nil, resp, cr.logger); err != nil {
						cr.logger.Error("failed to modify response", zap.Error(err), zap.String("provider", p.Name))
					}
				}
				return nil
			},
			ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
				cr.logger.Error("Upstream proxy error",
					zap.String("provider", p.Name),
					zap.String("target_url", req.URL.String()),
					zap.Error(err),
				)
				http.Error(rw, fmt.Sprintf("Error proxying to upstream provider %s: %v", p.Name, err), http.StatusBadGateway)
			},
		}
		cr.logger.Info("Provisioned provider for core router", zap.String("name", name), zap.String("base_url", p.APIBaseURL))
	}

	if cr.SuperDefaultProvider != "" {
		if _, ok := cr.Providers[cr.SuperDefaultProvider]; !ok {
			return fmt.Errorf("super_default_provider '%s' is not a configured provider", cr.SuperDefaultProvider)
		}
	}
	for model, providerName := range cr.DefaultProviderForModel {
		if _, ok := cr.Providers[providerName]; !ok {
			return fmt.Errorf("default provider '%s' for model '%s' is not a configured provider", providerName, model)
		}
	}

	cr.logger.Info("AI Core Router provisioned",
		zap.Int("num_providers", len(cr.Providers)),
		zap.String("super_default_provider", cr.SuperDefaultProvider),
		zap.Int("num_model_defaults", len(cr.DefaultProviderForModel)),
	)
	return nil
}

func (cr *AICoreRouter) Validate() error {
	cr.mu.RLock()
	defer cr.mu.RUnlock()

	if len(cr.Providers) == 0 {
		return fmt.Errorf("at least one provider must be configured for ai_core_router")
	}
	if cr.SuperDefaultProvider != "" {
		if _, ok := cr.Providers[cr.SuperDefaultProvider]; !ok {
			return fmt.Errorf("super_default_provider '%s' is not a configured provider", cr.SuperDefaultProvider)
		}
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

	if r.Method == http.MethodGet && r.URL.Path == "/models" {
		return cr.handleGetManagedModels(w, r.WithContext(reqCtx), apiKeyService)
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
		cr.DefaultProviderForModel = make(map[string]string)
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
			case "default_provider_for_model":
				args := d.RemainingArgs()
				if len(args) != 2 {
					return d.Errf("default_provider_for_model expects <model_name> <provider_name>, got %d args", len(args))
				}
				modelName := args[0]
				providerName := strings.ToLower(args[1])
				cr.DefaultProviderForModel[modelName] = providerName
			case "super_default_provider":
				if !d.NextArg() {
					return d.ArgErr()
				}
				cr.SuperDefaultProvider = strings.ToLower(d.Val())
			default:
				return d.Errf("unrecognized ai_core_router option '%s'", d.Val())
			}
		}
	}
	return nil
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

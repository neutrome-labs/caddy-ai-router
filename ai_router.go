package ai_router

import (
	// Ensure context import is present
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings" // For UnmarshalCaddyfile
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

const (
	// UserIDContextKeyString is the key for the user ID in the request context.
	UserIDContextKeyString string = "ai_user_id"
	// ApiKeyIDContextKeyString is the key for the key ID in the request context.
	ApiKeyIDContextKeyString string = "ai_api_key_id"
	// ExternalAPIKeyProviderContextKeyString is the key for an external API key provider service in the request context.
	ExternalAPIKeyProviderContextKeyString string = "ai_external_api_key_provider" // Renamed and made generic
	// ProviderNameContextKeyString is the key for the resolved provider name in the request context.
	ProviderNameContextKeyString string = "ai_provider_name"
	// ActualModelNameContextKeyString is the key for the actual model name in the request context.
	ActualModelNameContextKeyString string = "ai_actual_model_name"
)

// ExternalAPIKeyProvider defines the interface ai_router expects from a service
// that can provide API keys for upstream providers.
type ExternalAPIKeyProvider interface {
	// GetExternalAPIKey fetches an API key for a given target identifier (e.g., provider name)
	// and an optional user ID (for user-specific keys).
	GetExternalAPIKey(targetIdentifier string, userID string) (string, error)
}

func init() {
	caddy.RegisterModule(AICoreRouter{})
	httpcaddyfile.RegisterHandlerDirective("ai_core_router", parseAICoreRouterCaddyfile)
}

// AICoreRouter is a Caddy HTTP handler module that routes requests to different AI providers.
type AICoreRouter struct {
	Providers               map[string]*ProviderConfig `json:"providers,omitempty"`
	DefaultProviderForModel map[string]string          `json:"default_provider_for_model,omitempty"`
	SuperDefaultProvider    string                     `json:"super_default_provider,omitempty"`
	UpstreamPath            string                     `json:"upstream_path,omitempty"`

	// ProviderTargetMapping is removed. Provider name will be used directly.

	logger     *zap.Logger
	mu         sync.RWMutex
	httpClient *http.Client
}

// ProviderConfig holds configuration for an AI provider.
type ProviderConfig struct {
	Name       string `json:"-"`
	APIBaseURL string `json:"api_base_url,omitempty"`
	proxy      *httputil.ReverseProxy
	parsedURL  *url.URL
}

// CaddyModule returns the Caddy module information.
func (AICoreRouter) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.ai_core_router",
		New: func() caddy.Module { return new(AICoreRouter) },
	}
}

// Provision sets up the module.
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
	// ProviderTargetMapping initialization removed.

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

		p.proxy = &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL.Scheme = p.parsedURL.Scheme
				req.URL.Host = p.parsedURL.Host
				req.URL.Path = singleJoiningSlash(p.parsedURL.Path, cr.UpstreamPath)
				req.Host = p.parsedURL.Host
				req.Header.Del("X-Forwarded-Proto")
			},
			ModifyResponse: func(resp *http.Response) error {
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
	if cr.UpstreamPath == "" {
		cr.logger.Warn("upstream_path is not set for core router, requests will be proxied to provider's base URL root")
	}

	// Logging for ProviderTargetMapping removed.
	cr.logger.Info("AI Core Router provisioned",
		zap.Int("num_providers", len(cr.Providers)),
		zap.String("super_default_provider", cr.SuperDefaultProvider),
		zap.Int("num_model_defaults", len(cr.DefaultProviderForModel)),
		zap.String("upstream_path", cr.UpstreamPath),
	)
	return nil
}

// Validate ensures the module is configured correctly.
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

// ServeHTTP implements the caddyhttp.Handler interface.
func (cr *AICoreRouter) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	cr.logger.Debug("ServeHTTP invoked in ai_core_router",
		zap.String("method", r.Method),
		zap.String("host", r.Host),
		zap.String("uri", r.RequestURI),
		zap.String("path", r.URL.Path),
	)

	reqCtx := r.Context()

	// Attempt to get an ExternalAPIKeyProvider from context.
	// Use the raw string literal for the key to ensure it matches what ai_helpers sets,
	// avoiding issues with different types for keys with the same string value.
	var apiKeyService ExternalAPIKeyProvider
	apiKeyServiceVal := reqCtx.Value("ai_external_api_key_provider")
	if apiKeyServiceVal != nil {
		var ok bool
		apiKeyService, ok = apiKeyServiceVal.(ExternalAPIKeyProvider) // Assert to new generic interface
		if !ok {
			cr.logger.Warn("Value found for ExternalAPIKeyProviderContextKeyString but type assertion to ExternalAPIKeyProvider failed.",
				zap.Any("type_found", fmt.Sprintf("%T", apiKeyServiceVal)))
			// apiKeyService will remain nil, and handlers should cope with that.
		} else {
			cr.logger.Debug("ExternalAPIKeyProvider successfully retrieved from context.")
		}
	}

	if apiKeyService == nil {
		cr.logger.Info("ExternalAPIKeyProvider not found in context. Initializing DefaultEnvAPIKeyProvider.")
		apiKeyService = NewDefaultEnvAPIKeyProvider(cr.logger)
	}

	if r.Method == http.MethodGet && r.URL.Path == "/models" {
		cr.logger.Debug("Routing to handleGetManagedModels in ai_core_router", zap.String("path", r.URL.Path))
		return cr.handleGetManagedModels(w, r.WithContext(reqCtx), apiKeyService) // Pass generic service
	}

	if r.Method == http.MethodPost && r.URL.Path == "/chat/completions" {
		cr.logger.Debug("Routing to handlePostInferenceRequest in ai_core_router", zap.String("path", r.URL.Path))
		return cr.handlePostInferenceRequest(w, r.WithContext(reqCtx), next, apiKeyService) // Pass generic service
	}

	cr.logger.Debug("Request to ai_core_router not handled by specific sub-routes, passing to next or 404ing",
		zap.String("method", r.Method),
		zap.String("original_path_for_debug", r.RequestURI),
		zap.String("handler_path", r.URL.Path),
	)
	return next.ServeHTTP(w, r.WithContext(reqCtx))
}

// UnmarshalCaddyfile sets up the AICoreRouter from Caddyfile tokens.
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
			// at directive name
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
			case "upstream_path":
				if !d.NextArg() {
					return d.ArgErr()
				}
				cr.UpstreamPath = d.Val()
				if !strings.HasPrefix(cr.UpstreamPath, "/") {
					cr.UpstreamPath = "/" + cr.UpstreamPath
				}
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

// Interface guards
var (
	_ caddy.Provisioner           = (*AICoreRouter)(nil)
	_ caddy.Validator             = (*AICoreRouter)(nil)
	_ caddyhttp.MiddlewareHandler = (*AICoreRouter)(nil)
	_ caddyfile.Unmarshaler       = (*AICoreRouter)(nil)
)

// singleJoiningSlash, llmUsage, llmResponseData, etc. are assumed to be defined elsewhere or will be moved.

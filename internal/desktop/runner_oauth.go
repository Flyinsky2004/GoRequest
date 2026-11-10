package desktop

// Desktop adapter for runner.AuthProvider. It reuses the project-isolated
// core.OAuthService (token store, singleflight refresh, OIDC verification) and
// never opens a browser or loopback listener: the runner is non-interactive.
// See docs/contracts/F01-runner-extension.md.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorequest/internal/core"
	"gorequest/internal/runner"
)

type runnerOAuthProvider struct {
	svc         *core.OAuthService
	projectRoot string
	projectSlug string
	envID       string
	secrets     core.SecretProvider
}

func newRunnerOAuthProvider(svc *core.OAuthService, projectRoot, projectSlug, envID string, secrets core.SecretProvider) *runnerOAuthProvider {
	if strings.TrimSpace(projectSlug) == "" {
		projectSlug = "project"
	}
	return &runnerOAuthProvider{
		svc:         svc,
		projectRoot: projectRoot,
		projectSlug: projectSlug,
		envID:       envID,
		secrets:     secrets,
	}
}

func (p *runnerOAuthProvider) flow(req runner.AuthRequest) (core.OAuthFlowRequest, core.OAuthConfig, error) {
	cfg, err := core.ExtractOAuthConfig(req.Scheme)
	if err != nil {
		return core.OAuthFlowRequest{}, core.OAuthConfig{}, err
	}
	if cfg == nil {
		cfg = &core.OAuthConfig{}
	}
	for _, issue := range core.ValidateOAuthScheme("step "+req.StepID, req.Scheme) {
		if issue.Level == "error" {
			return core.OAuthFlowRequest{}, core.OAuthConfig{}, &core.OAuthError{
				Category: core.OAuthErrInvalidConfig,
				Message:  issue.Message,
			}
		}
	}
	netOpts := req.ExecuteOptions
	netOpts.Interactive = false
	netOpts.Confirmed = false
	netOpts.EnvID = p.envID
	netOpts.Redactor = req.Redactor
	netOpts.Secrets = req.Secrets
	netOpts.CookieJar = nil
	return core.OAuthFlowRequest{
		ProjectRoot:     p.projectRoot,
		ProjectSlug:     p.projectSlug,
		SchemeID:        req.SchemeID,
		SchemeType:      req.SchemeType,
		Config:          *cfg,
		Vars:            req.Variables,
		Secrets:         p.secrets,
		Network:         netOpts,
		TargetPolicy:    netOpts.EffectiveTargetPolicy(),
		HasTargetPolicy: netOpts.HasTargetPolicy,
		EnvID:           p.envID,
		Confirmed:       false,
		Redactor:        req.Redactor,
		Interactive:     false,
	}, *cfg, nil
}

// PreflightAuth is a zero-network check. It mirrors core.OAuthService.
// EnsureValidToken: a cached usable token, a cached refresh token or the
// client_credentials grant can complete without user interaction.
func (p *runnerOAuthProvider) PreflightAuth(ctx context.Context, req runner.AuthRequest) error {
	flow, _, err := p.flow(req)
	if err != nil {
		return err
	}
	resolved, err := flow.ResolveConfig()
	if err != nil {
		return err
	}
	cached, err := p.svc.GetCachedToken(ctx, flow)
	if err != nil {
		return err
	}
	if cached != nil && !cached.IsExpired(time.Now(), core.DefaultTokenExpirySkew) && cachedHasSelectedToken(cached, resolved) {
		return nil
	}
	if cached != nil && strings.TrimSpace(cached.RefreshToken) != "" {
		// Expired access token with a refresh token is refreshed silently at
		// attempt time (singleflight/rotation handled by core.OAuthService).
		return nil
	}
	if resolved.NormalizedGrantType() == core.OAuthGrantClientCredentials {
		return validateClientCredentialsConfig(resolved)
	}
	return &core.OAuthError{
		Category: core.OAuthErrInteractionRequired,
		Message: fmt.Sprintf(
			"scheme %q (%s) has no cached token or refresh token; non-interactive runs cannot sign in or poll a device code",
			req.SchemeID, resolved.NormalizedGrantType()),
	}
}

// ApplyAuth resolves or refreshes the token and injects it into the plan. The
// service registers access/refresh/id tokens and the client secret with
// req.Redactor; the returned values additionally bind the runner's redactors.
func (p *runnerOAuthProvider) ApplyAuth(ctx context.Context, plan *core.Plan, req runner.AuthRequest) (runner.AuthResult, error) {
	flow, _, err := p.flow(req)
	if err != nil {
		return runner.AuthResult{}, err
	}
	rec, err := p.svc.ApplyOAuthToPlan(ctx, plan, flow)
	if err != nil {
		return runner.AuthResult{}, err
	}
	if rec == nil {
		return runner.AuthResult{}, &core.OAuthError{
			Category: core.OAuthErrTokenExchangeFailed,
			Message:  "OAuth provider returned no token record",
		}
	}
	return runner.AuthResult{
		Secrets:   []string{rec.AccessToken, rec.RefreshToken, rec.IDToken},
		ExpiresAt: rec.ExpiresAt,
	}, nil
}

func cachedHasSelectedToken(rec *core.OAuthTokenRecord, cfg core.OAuthConfig) bool {
	if rec == nil {
		return false
	}
	if cfg.NormalizedUseToken() == core.OAuthUseIDToken {
		return strings.TrimSpace(rec.IDToken) != ""
	}
	return strings.TrimSpace(rec.AccessToken) != ""
}

func validateClientCredentialsConfig(cfg core.OAuthConfig) error {
	if strings.TrimSpace(cfg.ClientID) == "" {
		return &core.OAuthError{Category: core.OAuthErrInvalidConfig, Message: "client_credentials requires oauth.clientId"}
	}
	if strings.TrimSpace(cfg.TokenURL) == "" && strings.TrimSpace(cfg.Issuer) == "" && strings.TrimSpace(cfg.DiscoveryURL) == "" {
		return &core.OAuthError{Category: core.OAuthErrInvalidConfig, Message: "client_credentials requires oauth.tokenUrl or an issuer/discoveryUrl"}
	}
	if cfg.NormalizedClientAuthMethod() != core.OAuthClientAuthNone && strings.TrimSpace(cfg.ClientSecret) == "" {
		return &core.OAuthError{Category: core.OAuthErrInvalidConfig, Message: "client_credentials requires oauth.clientSecret or clientAuthMethod: none"}
	}
	return nil
}

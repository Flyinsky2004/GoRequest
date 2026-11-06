package desktop

import "strings"

// oauthMessages holds localized UI copy for OAuth 2.0 and OpenID Connect (A04).
// Kept in a dedicated file to avoid editing shared desktop/i18n.go directly on the branch.
type oauthMessages struct {
	SectionTitle             string
	SectionSub               string
	SchemeTypeOAuth2         string
	SchemeTypeOIDC           string
	GrantTypeLabel           string
	GrantAuthCodePKCE        string
	GrantClientCredentials   string
	GrantDeviceCode          string
	IssuerLabel              string
	IssuerPlaceholder        string
	DiscoveryURLLabel        string
	DiscoveryURLPlaceholder  string
	BtnFetchDiscovery        string
	DiscoveryFetching        string
	DiscoverySuccess         string
	AuthURLLabel             string
	AuthURLPlaceholder       string
	TokenURLLabel            string
	TokenURLPlaceholder      string
	DeviceAuthURLLabel       string
	DeviceAuthURLPlaceholder string
	RevocationURLLabel       string
	RevocationURLPlaceholder string
	JWKSURILabel             string
	JWKSURIPlaceholder       string
	RedirectURLLabel         string
	RedirectURLPlaceholder   string
	ClientIDLabel            string
	ClientIDPlaceholder      string
	ClientSecretLabel        string
	ClientSecretPlaceholder  string
	ClientAuthMethodLabel    string
	ClientAuthBasic          string
	ClientAuthPost           string
	ClientAuthNone           string
	ScopesLabel              string
	ScopesPlaceholder        string
	AudienceLabel            string
	AudiencePlaceholder      string
	ResourceLabel            string
	ResourcePlaceholder      string
	UseTokenLabel            string
	UseAccessToken           string
	UseIDToken               string
	BtnGetTokenPKCE          string
	BtnGetTokenClientCreds   string
	BtnStartDeviceFlow       string
	BtnCancelFlow            string
	BtnRefreshToken          string
	BtnClearLocalToken       string
	BtnRevokeToken           string
	StatusIdle               string
	StatusAuthorizingPKCE    string
	StatusAcquiringToken     string
	StatusPollingDevice      string
	StatusRefreshing         string
	StatusRevoking           string
	StatusTokenAcquired      string
	StatusTokenRefreshed     string
	StatusLocalTokenCleared  string
	DeviceCardTitle          string
	DeviceUserCodeLabel      string
	DeviceVerifyURLLabel     string
	BtnOpenVerificationPage  string
	DevicePollingHint        string
	DeviceSlowDownHint       string
	InspectorTitle           string
	InspectorNoToken         string
	InspectorTokenValid      string
	InspectorTokenExpired    string
	InspectorAccessToken     string
	InspectorRefreshToken    string
	InspectorIDToken         string
	InspectorScope           string
	InspectorExpiresAt       string
	OIDCBadgeVerified        string
	OIDCBadgeUnverified      string
	OIDCAlgKidLabel          string
	OIDCIssuerSubLabel       string
	OIDCAudienceLabel        string
	IsolationScopeHint       string
	LiteralSecretWarn        string
}

var (
	oauthZhCN = oauthMessages{
		SectionTitle:             "OAuth 2.0 / OpenID Connect 鉴权配置",
		SectionSub:               "令牌按本地项目克隆、方案、Issuer、Client 与 Scope 五维隔离存储在系统凭据库，绝不写入共享 YAML 文件。",
		SchemeTypeOAuth2:         "OAuth 2.0",
		SchemeTypeOIDC:           "OpenID Connect (OIDC)",
		GrantTypeLabel:           "授权模式 (Grant Type)",
		GrantAuthCodePKCE:        "Authorization Code + PKCE (S256 系统浏览器)",
		GrantClientCredentials:   "Client Credentials (服务端凭证)",
		GrantDeviceCode:          "Device Authorization (RFC 8628 设备码)",
		IssuerLabel:              "OIDC Issuer",
		IssuerPlaceholder:        "https://accounts.example.com",
		DiscoveryURLLabel:        "Discovery URL (可选)",
		DiscoveryURLPlaceholder:  "https://accounts.example.com/.well-known/openid-configuration",
		BtnFetchDiscovery:        "拉取 Discovery 配置",
		DiscoveryFetching:        "正在拉取并校验 OIDC Discovery 配置...",
		DiscoverySuccess:         "已从 OIDC Discovery 自动填充端点配置",
		AuthURLLabel:             "Authorization Endpoint",
		AuthURLPlaceholder:       "https://accounts.example.com/oauth2/authorize",
		TokenURLLabel:            "Token Endpoint",
		TokenURLPlaceholder:      "https://accounts.example.com/oauth2/token",
		DeviceAuthURLLabel:       "Device Authorization Endpoint",
		DeviceAuthURLPlaceholder: "https://accounts.example.com/oauth2/device/code",
		RevocationURLLabel:       "Revocation Endpoint (可选)",
		RevocationURLPlaceholder: "https://accounts.example.com/oauth2/revoke",
		JWKSURILabel:             "JWKS URI (OIDC 验签)",
		JWKSURIPlaceholder:       "https://accounts.example.com/.well-known/jwks.json",
		RedirectURLLabel:         "Loopback Redirect URI (可选)",
		RedirectURLPlaceholder:   "默认 http://127.0.0.1:0/oauth/callback",
		ClientIDLabel:            "Client ID",
		ClientIDPlaceholder:      "my-desktop-client",
		ClientSecretLabel:        "Client Secret 引用",
		ClientSecretPlaceholder:  "{{secret.oauth_client_secret}}",
		ClientAuthMethodLabel:    "客户端认证方式",
		ClientAuthBasic:          "HTTP Basic (client_secret_basic)",
		ClientAuthPost:           "Form Body (client_secret_post)",
		ClientAuthNone:           "Public Client (none)",
		ScopesLabel:              "Scopes (空格分隔)",
		ScopesPlaceholder:        "openid profile email api:read",
		AudienceLabel:            "Audience (可选)",
		AudiencePlaceholder:      "https://api.example.com",
		ResourceLabel:            "Resource (可选)",
		ResourcePlaceholder:      "urn:example:resource",
		UseTokenLabel:            "注入业务请求的令牌",
		UseAccessToken:           "Access Token (默认推荐)",
		UseIDToken:               "ID Token (仅显式要求时)",
		BtnGetTokenPKCE:          "通过浏览器登录 (PKCE)",
		BtnGetTokenClientCreds:   "获取 Client Token",
		BtnStartDeviceFlow:       "启动设备码授权",
		BtnCancelFlow:            "取消授权",
		BtnRefreshToken:          "立即刷新令牌",
		BtnClearLocalToken:       "清除本地缓存",
		BtnRevokeToken:           "吊销并清除令牌",
		StatusIdle:               "就绪（仅在点击按钮或发送请求时按需执行）",
		StatusAuthorizingPKCE:    "已打开系统浏览器并启动本地回环监听，请完成授权...",
		StatusAcquiringToken:     "正在向 Token Endpoint 请求访问令牌...",
		StatusPollingDevice:      "正在等待用户在浏览器中输入设备验证码...",
		StatusRefreshing:         "正在使用 Refresh Token 轮换刷新令牌...",
		StatusRevoking:           "正在清除本地缓存并请求远端吊销...",
		StatusTokenAcquired:      "令牌获取并校验成功，已存入本地隔离凭据库。",
		StatusTokenRefreshed:     "令牌刷新成功，已原子更新本地凭据库。",
		StatusLocalTokenCleared:  "已从本地隔离凭据库清除令牌。",
		DeviceCardTitle:          "设备授权验证码 (RFC 8628)",
		DeviceUserCodeLabel:      "用户验证码 (User Code)",
		DeviceVerifyURLLabel:     "验证页面地址",
		BtnOpenVerificationPage:  "在浏览器中打开验证页面",
		DevicePollingHint:        "正在轮询授权状态",
		DeviceSlowDownHint:       "服务端要求降速 (slow_down)，轮询间隔已自动 +5s",
		InspectorTitle:           "当前隔离令牌与 OIDC 校验状态",
		InspectorNoToken:         "当前项目克隆与方案下暂无已缓存令牌",
		InspectorTokenValid:      "有效 (Active)",
		InspectorTokenExpired:    "已过期 (Expired)",
		InspectorAccessToken:     "Access Token 摘要",
		InspectorRefreshToken:    "Refresh Token",
		InspectorIDToken:         "ID Token 摘要",
		InspectorScope:           "已授予 Scope",
		InspectorExpiresAt:       "过期时间 (UTC)",
		OIDCBadgeVerified:        "OIDC 签名与 Claims 已校验",
		OIDCBadgeUnverified:      "未校验预览 (Unverified Preview)",
		OIDCAlgKidLabel:          "JWS 算法 / KID",
		OIDCIssuerSubLabel:       "Issuer / Subject",
		OIDCAudienceLabel:        "Audience (aud)",
		IsolationScopeHint:       "凭据隔离作用域",
		LiteralSecretWarn:        "检测到疑似明文 Client Secret，建议改用 {{secret.name}} 引用系统凭据库",
	}

	oauthEn = oauthMessages{
		SectionTitle:             "OAuth 2.0 / OpenID Connect Configuration",
		SectionSub:               "Tokens are isolated by local project clone, scheme, issuer, client, and scopes in the OS credential store, never written to shared YAML.",
		SchemeTypeOAuth2:         "OAuth 2.0",
		SchemeTypeOIDC:           "OpenID Connect (OIDC)",
		GrantTypeLabel:           "Grant Type",
		GrantAuthCodePKCE:        "Authorization Code + PKCE (S256 System Browser)",
		GrantClientCredentials:   "Client Credentials (Machine-to-Machine)",
		GrantDeviceCode:          "Device Authorization (RFC 8628 Device Code)",
		IssuerLabel:              "OIDC Issuer",
		IssuerPlaceholder:        "https://accounts.example.com",
		DiscoveryURLLabel:        "Discovery URL (Optional)",
		DiscoveryURLPlaceholder:  "https://accounts.example.com/.well-known/openid-configuration",
		BtnFetchDiscovery:        "Fetch Discovery",
		DiscoveryFetching:        "Fetching and validating OIDC Discovery metadata...",
		DiscoverySuccess:         "Populated OAuth/OIDC endpoints from Discovery document",
		AuthURLLabel:             "Authorization Endpoint",
		AuthURLPlaceholder:       "https://accounts.example.com/oauth2/authorize",
		TokenURLLabel:            "Token Endpoint",
		TokenURLPlaceholder:      "https://accounts.example.com/oauth2/token",
		DeviceAuthURLLabel:       "Device Authorization Endpoint",
		DeviceAuthURLPlaceholder: "https://accounts.example.com/oauth2/device/code",
		RevocationURLLabel:       "Revocation Endpoint (Optional)",
		RevocationURLPlaceholder: "https://accounts.example.com/oauth2/revoke",
		JWKSURILabel:             "JWKS URI (OIDC Signature Verification)",
		JWKSURIPlaceholder:       "https://accounts.example.com/.well-known/jwks.json",
		RedirectURLLabel:         "Loopback Redirect URI (Optional)",
		RedirectURLPlaceholder:   "Default http://127.0.0.1:0/oauth/callback",
		ClientIDLabel:            "Client ID",
		ClientIDPlaceholder:      "my-desktop-client",
		ClientSecretLabel:        "Client Secret Reference",
		ClientSecretPlaceholder:  "{{secret.oauth_client_secret}}",
		ClientAuthMethodLabel:    "Client Auth Method",
		ClientAuthBasic:          "HTTP Basic (client_secret_basic)",
		ClientAuthPost:           "Form Body (client_secret_post)",
		ClientAuthNone:           "Public Client (none)",
		ScopesLabel:              "Scopes (Space-separated)",
		ScopesPlaceholder:        "openid profile email api:read",
		AudienceLabel:            "Audience (Optional)",
		AudiencePlaceholder:      "https://api.example.com",
		ResourceLabel:            "Resource (Optional)",
		ResourcePlaceholder:      "urn:example:resource",
		UseTokenLabel:            "Token Injected into API Requests",
		UseAccessToken:           "Access Token (Recommended Default)",
		UseIDToken:               "ID Token (Explicit Override Only)",
		BtnGetTokenPKCE:          "Sign In with Browser (PKCE)",
		BtnGetTokenClientCreds:   "Acquire Client Token",
		BtnStartDeviceFlow:       "Start Device Code Flow",
		BtnCancelFlow:            "Cancel Flow",
		BtnRefreshToken:          "Refresh Token Now",
		BtnClearLocalToken:       "Clear Local Token",
		BtnRevokeToken:           "Revoke & Clear Token",
		StatusIdle:               "Ready (executes only on explicit user action or request send)",
		StatusAuthorizingPKCE:    "System browser opened and loopback listener active; waiting for callback...",
		StatusAcquiringToken:     "Requesting token from Token Endpoint...",
		StatusPollingDevice:      "Waiting for user to complete device code verification...",
		StatusRefreshing:         "Refreshing token with Refresh Token rotation...",
		StatusRevoking:           "Clearing local cache and revoking token remotely...",
		StatusTokenAcquired:      "Token acquired, verified, and stored in isolated credential store.",
		StatusTokenRefreshed:     "Token refreshed and rotated in isolated credential store.",
		StatusLocalTokenCleared:  "Token removed from local isolated credential store.",
		DeviceCardTitle:          "Device Authorization Challenge (RFC 8628)",
		DeviceUserCodeLabel:      "User Verification Code",
		DeviceVerifyURLLabel:     "Verification URL",
		BtnOpenVerificationPage:  "Open Verification Page in Browser",
		DevicePollingHint:        "Polling authorization status",
		DeviceSlowDownHint:       "Server requested slow_down; polling interval increased by +5s",
		InspectorTitle:           "Isolated Token & OIDC Verification Inspector",
		InspectorNoToken:         "No cached token for this project clone and scheme",
		InspectorTokenValid:      "Active",
		InspectorTokenExpired:    "Expired",
		InspectorAccessToken:     "Access Token Preview",
		InspectorRefreshToken:    "Refresh Token",
		InspectorIDToken:         "ID Token Preview",
		InspectorScope:           "Granted Scopes",
		InspectorExpiresAt:       "Expires At (UTC)",
		OIDCBadgeVerified:        "OIDC Signature & Claims Verified",
		OIDCBadgeUnverified:      "Unverified Preview",
		OIDCAlgKidLabel:          "JWS Alg / KID",
		OIDCIssuerSubLabel:       "Issuer / Subject",
		OIDCAudienceLabel:        "Audience (aud)",
		IsolationScopeHint:       "Credential Isolation Scope",
		LiteralSecretWarn:        "Client Secret looks like a literal secret; use {{secret.name}} instead",
	}
)

func oauthMsg(locale string) oauthMessages {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "en") {
		return oauthEn
	}
	return oauthZhCN
}

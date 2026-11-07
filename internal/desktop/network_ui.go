package desktop

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"weak"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
	"gorequest/internal/platform"
)

// networkMessages holds localized labels for the B04 Transport, Cookie Jar,
// TLS/mTLS, Proxy, and Response Inspector UI. Keeping these in network_ui.go
// avoids modifying shared internal/desktop/i18n.go in the branch; exact merge
// keys are provided in docs/task-results/B04-integration.patch and B04.md.
type networkMessages struct {
	// Toolbar & Section tabs
	CookiesSection   string
	NetworkSection   string
	TreeMode         string
	CookieJarBtnFmt  string // %d
	NetworkBtn       string
	OpenCookieJarBtn string
	SaveBinaryBtn    string
	CopyJSONPathBtn  string
	CopyValueBtn     string
	ExpandAllBtn     string
	CollapseAllBtn   string

	// Status bar & Size / Compression
	TransferVsBodyFmt string // %s (transfer), %s (body), %s (encoding)
	RedirectsBadgeFmt string // %d
	TLSInsecureBadge  string
	SearchCappedNote  string // %d
	TreeTruncatedNote string // %d

	// Binary Inspector
	BinaryPayloadTitle string
	BinaryInfoFmt      string // %s (mime), %d (bytes), %s (sha256)
	BinaryPreviewLabel string

	// Cookie Jar Modal
	CookieJarTitle          string
	CookieIsolationNote     string
	CookieFilterPlaceholder string
	CookieEmptyHint         string
	CookieUpsertHeader      string
	CookieDomainPlaceholder string
	CookieNamePlaceholder   string
	CookieValuePlaceholder  string
	CookieSaveBtn           string
	CookieEditBtn           string
	CookieClearAll          string
	CookieClearedStatus     string
	ResponseCookiesHeader   string
	NoResponseCookies       string
	RevealSecret            string
	HideSecret              string

	// Network Settings Modal & Request Settings
	NetworkModalTitle   string
	StageTimeoutsHeader string
	DialTimeoutLabel    string
	TLSTimeoutLabel     string
	TTFBTimeoutLabel    string
	TotalTimeoutLabel   string

	// FIX08: request-timeout discoverability (bilingual; see timeout_ui.go).
	RequestTimeoutHeader      string
	RequestTimeoutScopeHint   string
	RequestTimeoutPlaceholder string
	RequestTimeoutUnitHint    string
	TimeoutEntryHint          string
	TimeoutQuickEntry         string
	TimeoutQuickEntryTip      string
	TimeoutQuickEntryInvalid  string
	TimeoutBadgeFmt           string // %s (effective duration)
	TimeoutBadgeInvalid       string
	TimeoutEffectiveFmt       string // %s (effective duration), %s (source)
	TimeoutSourceRequest      string
	TimeoutSourceDefault      string
	TimeoutSourceDeviceTotal  string
	TimeoutNoTabHint          string
	TimeoutInvalidFmt         string // %s (parse error)
	TimeoutNonPositiveFmt     string
	StageTimeoutsNote         string
	NetworkDraftsInvalidFmt   string // %s (validation error)
	RedirectHeader            string
	RedirectModeLabel         string
	RedirectFollowOpt         string
	RedirectNoneOpt           string
	RedirectSameOriginOpt     string
	MaxRedirectsLabel         string
	SendJarCookiesLabel       string
	ProxyHeader               string
	ProxyModeLabel            string
	ProxyURLPlaceholder       string
	NoProxyPlaceholder        string
	ProxyUserPlaceholder      string
	ProxyPassPlaceholder      string
	ProxySecretPlaceholder    string
	TLSHeader                 string
	UseSystemCALabel          string
	CustomCASlotLabel         string
	ClientCertSlotLabel       string
	ClientKeySlotLabel        string
	LocalPathPlaceholder      string
	SNIServerNameLabel        string
	MTLSKeyLocalOnlyNote      string
	TargetPolicyHeader        string
	ProdActionLabel           string
	DeniedHostsLabel          string
	AllowedHostsLabel         string
	EnforceTLSVerifyLabel     string
	RequireHTTPSCredsLabel    string
	SaveNetworkSettingsBtn    string
	NetworkSettingsSaved      string

	// Response Network Trace Tab
	TraceSummaryHeader    string
	TraceTransferSize     string
	TraceDecompressedSize string
	TraceEncoding         string
	TraceRemoteAddr       string
	TraceConnReused       string
	TraceProxyUsed        string
	TraceStageDNS         string
	TraceStageConnect     string
	TraceStageTLS         string
	TraceStageWrite       string
	TraceStageTTFB        string
	TraceStageDownload    string
	TraceTLSHeader        string
	TraceRedirectsHeader  string
	TraceNoRedirects      string
	TraceStrippedBadge    string
	TraceCrossOriginBadge string
	TraceNotObserved      string
	YesLabel              string
	NoLabel               string
}

var networkMessagesZh = networkMessages{
	CookiesSection:   "Cookie",
	NetworkSection:   "网络",
	TreeMode:         "树形",
	CookieJarBtnFmt:  "Cookie (%d)",
	NetworkBtn:       "网络与证书…",
	OpenCookieJarBtn: "管理项目 Cookie 罐…",
	SaveBinaryBtn:    "保存原始二进制文件…",
	CopyJSONPathBtn:  "复制路径",
	CopyValueBtn:     "复制值",
	ExpandAllBtn:     "全部展开",
	CollapseAllBtn:   "全部折叠",

	TransferVsBodyFmt: "%s 传输 · %s 解压 (%s)",
	RedirectsBadgeFmt: "%d 次重定向",
	TLSInsecureBadge:  "⚠ 已跳过 TLS 校验",
	SearchCappedNote:  "（已达上限 %d 处匹配）",
	TreeTruncatedNote: "（JSON 树已按 %d 节点上限截断显示）",

	BinaryPayloadTitle: "二进制响应载荷（已安全隔离渲染，不会损坏原始字节）",
	BinaryInfoFmt:      "类型：%s · 原始大小：%d B · SHA-256：%s",
	BinaryPreviewLabel: "前 512 字节十六进制预览：",

	CookieJarTitle:          "项目隔离 Cookie 罐",
	CookieIsolationNote:     "Cookie 仅保存在当前项目内存会话中，跨项目严格隔离，绝不写入 .gorequest/ 仓库文件。",
	CookieFilterPlaceholder: "按域名、路径或名称筛选 Cookie…",
	CookieEmptyHint:         "当前项目 Cookie 罐为空。",
	CookieUpsertHeader:      "添加或更新 Cookie",
	CookieDomainPlaceholder: "域名 (如 api.example.com)",
	CookieNamePlaceholder:   "名称",
	CookieValuePlaceholder:  "Cookie 值",
	CookieSaveBtn:           "保存 Cookie",
	CookieEditBtn:           "编辑",
	CookieClearAll:          "清空全部",
	CookieClearedStatus:     "已清空当前项目 Cookie 罐",
	ResponseCookiesHeader:   "本次响应 Set-Cookie",
	NoResponseCookies:       "本次响应未设置 Set-Cookie 头。",
	RevealSecret:            "显示",
	HideSecret:              "隐藏",

	NetworkModalTitle:   "网络传输、代理、TLS/mTLS 与目标策略",
	StageTimeoutsHeader: "阶段超时（均可随时取消）",
	DialTimeoutLabel:    "连接/握手拨号超时 (Dial)",
	TLSTimeoutLabel:     "TLS 握手超时",
	TTFBTimeoutLabel:    "首字节响应头超时 (TTFB)",
	TotalTimeoutLabel:   "请求总超时",

	RequestTimeoutHeader:      "请求超时（本机保存，随请求文件共享）",
	RequestTimeoutScopeHint:   "这是本次请求从发起到收到完整响应的总时限；留空或无效时由下方“当前生效”说明列出实际值与来源。",
	RequestTimeoutPlaceholder: "例如 10s、2.5s、500ms、1m30s",
	RequestTimeoutUnitHint:    "单位用 Go 时长写法：ns、us（µs）、ms、s、m、h，可组合，例如 1m30s；不能用纯数字或 10 秒。留空 = 继承默认 10s。",
	TimeoutEntryHint:          "保存请求后此项写入项目里的该请求文件；下方“阶段超时”只保存在本机设备，不随项目共享。",
	TimeoutQuickEntry:         "请求超时…",
	TimeoutQuickEntryTip:      "跳到请求编辑区的「设置」标签页修改请求总超时（⌘S 保存）",
	TimeoutQuickEntryInvalid:  "请求超时值无效，点此修正",
	TimeoutBadgeFmt:           "超时 %s",
	TimeoutBadgeInvalid:       "超时值无效",
	TimeoutEffectiveFmt:       "当前生效：%s（来源：%s）",
	TimeoutSourceRequest:      "本请求设置",
	TimeoutSourceDefault:      "内置默认（请求未设置）",
	TimeoutSourceDeviceTotal:  "本机网络设置的总超时（优先于请求设置）",
	TimeoutNoTabHint:          "从请求编辑区打开此面板即可设置该请求的总超时。",
	TimeoutInvalidFmt:         "输入无效：%s；请改成如 10s、500ms。修正并保存前不会按新值发送。",
	TimeoutNonPositiveFmt:     "超时必须为正数；0 或负数会被忽略并回退到内置默认 10s。",
	StageTimeoutsNote:         "以下拨号 / TLS / 首字节阶段时限只保存在本机设备配置（network.json），作用于这台机器上的全部请求，不写入项目请求文件；与上方的“请求总超时”互不相同。",
	NetworkDraftsInvalidFmt:   "本机阶段超时未保存：%s。已保留上一份有效设置；请修正后重试。",
	RedirectHeader:            "重定向与会话 Cookie 策略",
	RedirectModeLabel:         "重定向策略",
	RedirectFollowOpt:         "跟随重定向 (跨源自动剥离凭据)",
	RedirectNoneOpt:           "不跟随 (直接返回 3xx)",
	RedirectSameOriginOpt:     "仅同源重定向",
	MaxRedirectsLabel:         "最大重定向跳数",
	SendJarCookiesLabel:       "启用项目隔离 Cookie 罐自动收发",
	ProxyHeader:               "HTTP / SOCKS5 代理（本机凭据）",
	ProxyModeLabel:            "代理模式",
	ProxyURLPlaceholder:       "代理地址 (如 http://127.0.0.1:8080 或 socks5://127.0.0.1:1080)",
	NoProxyPlaceholder:        "免代理列表 (如 localhost, 127.0.0.1, .internal)",
	ProxyUserPlaceholder:      "代理用户名 (本机)",
	ProxyPassPlaceholder:      "会话代理密码 (不落盘)",
	ProxySecretPlaceholder:    "或绑定钥匙串秘密名 (如 proxy_pass)",
	TLSHeader:                 "TLS 证书校验、自定义 CA 与 mTLS（本机文件映射）",
	UseSystemCALabel:          "信任操作系统根证书池 (System CA)",
	CustomCASlotLabel:         "自定义 CA 槽位 (local:<slot>)",
	ClientCertSlotLabel:       "mTLS 客户端证书槽位 (local:<slot>)",
	ClientKeySlotLabel:        "mTLS 客户端私钥槽位 (仅限本机 local:<slot>)",
	LocalPathPlaceholder:      "本机绝对路径 (仅存于当前设备，不写入项目)",
	SNIServerNameLabel:        "TLS SNI ServerName 覆盖",
	MTLSKeyLocalOnlyNote:      "安全边界：mTLS 私钥与代理密码仅引用本机配置，禁止从共享项目文件加载私钥，默认严格启用 TLS 校验。",
	TargetPolicyHeader:        "本机目标与生产环境防护策略 (B05 策略端口)",
	ProdActionLabel:           "生产目标动作",
	DeniedHostsLabel:          "禁止访问域名列表 (逗号分隔，支持 *.example.com)",
	AllowedHostsLabel:         "允许访问域名白名单 (留空为不限)",
	EnforceTLSVerifyLabel:     "强制 TLS 证书校验（禁止请求跳过证书验证）",
	RequireHTTPSCredsLabel:    "非回环地址携带凭据时要求 HTTPS",
	SaveNetworkSettingsBtn:    "应用并保存本机网络设置",
	NetworkSettingsSaved:      "已保存本机网络与证书映射设置",

	TraceSummaryHeader:    "传输大小与连接摘要",
	TraceTransferSize:     "网络传输字节 (Wire)",
	TraceDecompressedSize: "解压后 Body 大小",
	TraceEncoding:         "内容编码 (Content-Encoding)",
	TraceRemoteAddr:       "远端地址",
	TraceConnReused:       "连接复用",
	TraceProxyUsed:        "实际使用代理",
	TraceStageDNS:         "DNS 解析",
	TraceStageConnect:     "TCP 建连",
	TraceStageTLS:         "TLS 握手",
	TraceStageWrite:       "请求发送",
	TraceStageTTFB:        "首字节等待 (TTFB)",
	TraceStageDownload:    "响应接收",
	TraceTLSHeader:        "TLS 与证书详情",
	TraceRedirectsHeader:  "重定向逐跳审计记录",
	TraceNoRedirects:      "本次请求未发生重定向。",
	TraceStrippedBadge:    "已剥离跨源/降级敏感凭据",
	TraceCrossOriginBadge: "跨源跳转",
	TraceNotObserved:      "未触发/复用",
	YesLabel:              "是",
	NoLabel:               "否",
}

var networkMessagesEn = networkMessages{
	CookiesSection:   "Cookies",
	NetworkSection:   "Network",
	TreeMode:         "Tree",
	CookieJarBtnFmt:  "Cookies (%d)",
	NetworkBtn:       "Network & TLS…",
	OpenCookieJarBtn: "Manage Project Cookie Jar…",
	SaveBinaryBtn:    "Save Raw Binary…",
	CopyJSONPathBtn:  "Copy Path",
	CopyValueBtn:     "Copy Value",
	ExpandAllBtn:     "Expand All",
	CollapseAllBtn:   "Collapse All",

	TransferVsBodyFmt: "%s wire · %s body (%s)",
	RedirectsBadgeFmt: "%d redirects",
	TLSInsecureBadge:  "⚠ TLS Verify Skipped",
	SearchCappedNote:  "(capped at %d matches)",
	TreeTruncatedNote: "(JSON tree truncated at %d nodes)",

	BinaryPayloadTitle: "Binary Response Payload (rendered safely without UTF-8 corruption)",
	BinaryInfoFmt:      "MIME: %s · Size: %d B · SHA-256: %s",
	BinaryPreviewLabel: "First 512 bytes hex preview:",

	CookieJarTitle:          "Project Cookie Jar",
	CookieIsolationNote:     "Cookies are isolated to this project's session in memory and are never written to shared .gorequest/ files.",
	CookieFilterPlaceholder: "Filter cookies by domain, path, or name…",
	CookieEmptyHint:         "No cookies in this project's Cookie Jar.",
	CookieUpsertHeader:      "Add or Update Cookie",
	CookieDomainPlaceholder: "Domain (e.g. api.example.com)",
	CookieNamePlaceholder:   "Name",
	CookieValuePlaceholder:  "Cookie value",
	CookieSaveBtn:           "Save Cookie",
	CookieEditBtn:           "Edit",
	CookieClearAll:          "Clear All",
	CookieClearedStatus:     "Cleared project Cookie Jar",
	ResponseCookiesHeader:   "Response Set-Cookie",
	NoResponseCookies:       "No Set-Cookie headers in this response.",
	RevealSecret:            "Show",
	HideSecret:              "Hide",

	NetworkModalTitle:   "Network Transport, Proxy, TLS/mTLS & Target Policy",
	StageTimeoutsHeader: "Stage Timeouts (Cancellable)",
	DialTimeoutLabel:    "Connect / Dial Timeout",
	TLSTimeoutLabel:     "TLS Handshake Timeout",
	TTFBTimeoutLabel:    "Response Header Timeout (TTFB)",
	TotalTimeoutLabel:   "Overall Request Timeout",

	RequestTimeoutHeader:      "Request Timeout (saved with this request, shared in the project)",
	RequestTimeoutScopeHint:   "Total deadline from sending this request until the full response is read. Leave blank or invalid — the effective value and its source are shown below.",
	RequestTimeoutPlaceholder: "e.g. 10s, 2.5s, 500ms, 1m30s",
	RequestTimeoutUnitHint:    "Use Go duration units: ns, us, ms, s, m, h; combine them, e.g. 1m30s. Bare numbers or '10 seconds' are not valid. Blank = inherit the 10s default.",
	TimeoutEntryHint:          "Saving the request writes this value to that request's project file; the Stage Timeouts below stay machine-local and are never shared in the project.",
	TimeoutQuickEntry:         "Request Timeout…",
	TimeoutQuickEntryTip:      "Open the request editor's Settings tab to edit the overall timeout (save with Cmd+S)",
	TimeoutQuickEntryInvalid:  "Request timeout value is invalid — click to fix",
	TimeoutBadgeFmt:           "Timeout %s",
	TimeoutBadgeInvalid:       "Invalid timeout",
	TimeoutEffectiveFmt:       "In effect now: %s (source: %s)",
	TimeoutSourceRequest:      "this request",
	TimeoutSourceDefault:      "built-in default (request has none)",
	TimeoutSourceDeviceTotal:  "device network overall timeout (overrides the request)",
	TimeoutNoTabHint:          "Open this panel from a request editor to set that request's overall timeout.",
	TimeoutInvalidFmt:         "Invalid value: %s; use e.g. 10s or 500ms. The new value is not used until you fix and save it.",
	TimeoutNonPositiveFmt:     "Timeout must be positive; 0 or a negative value is ignored and falls back to the 10s default.",
	StageTimeoutsNote:         "These Dial / TLS / first-byte phase limits are stored only in this machine's device config (network.json). They apply to every request on this device and are never written into project request files; they are separate from the Request Timeout above.",
	NetworkDraftsInvalidFmt:   "Device stage timeouts not saved: %s. The last valid settings remain in effect; fix the value and retry.",
	RedirectHeader:            "Redirects & Cookie Jar Policy",
	RedirectModeLabel:         "Redirect Policy",
	RedirectFollowOpt:         "Follow Redirects (strip cross-origin credentials)",
	RedirectNoneOpt:           "Do Not Follow (return 3xx directly)",
	RedirectSameOriginOpt:     "Same-Origin Redirects Only",
	MaxRedirectsLabel:         "Max Redirect Hops",
	SendJarCookiesLabel:       "Enable project-isolated Cookie Jar for requests",
	ProxyHeader:               "HTTP / SOCKS5 Proxy (Machine-Local Credentials)",
	ProxyModeLabel:            "Proxy Mode",
	ProxyURLPlaceholder:       "Proxy URL (e.g. http://127.0.0.1:8080 or socks5://127.0.0.1:1080)",
	NoProxyPlaceholder:        "No-Proxy bypass list (e.g. localhost, 127.0.0.1, .internal)",
	ProxyUserPlaceholder:      "Proxy username (local)",
	ProxyPassPlaceholder:      "Session proxy password (not written to disk)",
	ProxySecretPlaceholder:    "Or Keychain secret name (e.g. proxy_pass)",
	TLSHeader:                 "TLS Verification, Custom CA & mTLS (Local Slot Mappings)",
	UseSystemCALabel:          "Trust OS System Root CA Pool",
	CustomCASlotLabel:         "Custom CA Slot (local:<slot>)",
	ClientCertSlotLabel:       "mTLS Client Cert Slot (local:<slot>)",
	ClientKeySlotLabel:        "mTLS Client Key Slot (local:<slot> only)",
	LocalPathPlaceholder:      "Absolute local file path on this machine",
	SNIServerNameLabel:        "TLS SNI ServerName Override",
	MTLSKeyLocalOnlyNote:      "Security boundary: mTLS private keys and proxy credentials remain strictly machine-local. TLS verification is never skipped by default.",
	TargetPolicyHeader:        "Device Target & Production Guardrails (B05 Policy Port)",
	ProdActionLabel:           "Production Target Action",
	DeniedHostsLabel:          "Denied Hosts (comma-separated, supports *.example.com)",
	AllowedHostsLabel:         "Allowed Hosts (leave blank for all non-denied)",
	EnforceTLSVerifyLabel:     "Enforce TLS Certificate Verification (forbid skipping TLS verify)",
	RequireHTTPSCredsLabel:    "Require HTTPS when sending credentials to non-loopback hosts",
	SaveNetworkSettingsBtn:    "Apply & Save Device Network Settings",
	NetworkSettingsSaved:      "Saved device network & certificate bindings",

	TraceSummaryHeader:    "Transfer Size & Connection Summary",
	TraceTransferSize:     "Wire Transfer Size",
	TraceDecompressedSize: "Decompressed Body Size",
	TraceEncoding:         "Content-Encoding",
	TraceRemoteAddr:       "Remote Address",
	TraceConnReused:       "Connection Reused",
	TraceProxyUsed:        "Proxy Used",
	TraceStageDNS:         "DNS Lookup",
	TraceStageConnect:     "TCP Connect",
	TraceStageTLS:         "TLS Handshake",
	TraceStageWrite:       "Request Write",
	TraceStageTTFB:        "Waiting (TTFB)",
	TraceStageDownload:    "Content Download",
	TraceTLSHeader:        "TLS & Certificate Security",
	TraceRedirectsHeader:  "Redirect Hop Audit Trail",
	TraceNoRedirects:      "No redirects occurred during this request.",
	TraceStrippedBadge:    "Credentials Stripped",
	TraceCrossOriginBadge: "Cross-Origin",
	TraceNotObserved:      "reused / n/a",
	YesLabel:              "Yes",
	NoLabel:               "No",
}

func (a *App) networkMsg() *networkMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langEnglish {
		return &networkMessagesEn
	}
	return &networkMessagesZh
}

// projectNetworkState owns the isolated CookieJar, local NetworkStore, and UI
// modal drafts for a single projectState. It is indexed by weak.Pointer[projectState]
// with runtime.AddCleanup and holds no strong pointer back to projectState or App.
type projectNetworkState struct {
	scopeKey  string
	cookieJar *core.CookieJar
	netStore  *platform.NetworkStore
	cookieUI  *cookieModalState

	netModalOpen      bool
	dialTimeoutDraft  string
	tlsTimeoutDraft   string
	ttfbTimeoutDraft  string
	redirectModeIdx   int // 0=follow, 1=none, 2=same_origin
	maxRedirectsDraft string
	sendCookies       bool

	proxyModeIdx     int // 0=system, 1=none, 2=http, 3=socks5
	proxyURLDraft    string
	noProxyDraft     string
	proxyUserDraft   string
	proxyPassDraft   string
	proxySecretDraft string

	useSystemCA        bool
	customCASlot       string
	customCAPath       string
	clientCertSlot     string
	clientCertPath     string
	clientKeySlot      string
	clientKeyPath      string
	sniServerNameDraft string

	prodActionIdx     int // 0=confirm, 1=allow_readonly, 2=deny, 3=allow
	deniedHostsDraft  string
	allowedHostsDraft string
	enforceTLSVerify  bool
	requireHTTPSCreds bool
	netErrMsg         string
	// timeoutErr is the inline FIX08 feedback for an invalid stage-timeout
	// draft. It is kept separate from netErrMsg (the modal-level save error) so
	// the message stays next to the field that caused it.
	timeoutErr string
}

// tabNetworkState holds per-tab response JSON tree state and optional per-tab
// network flags, bound via weak.Pointer[Tab] + runtime.AddCleanup with zero
// strong-reference maps.
type tabNetworkState struct {
	builtRespVer    int
	jsonTreeRoot    *core.JSONTreeNode
	jsonTreeTrunc   bool
	jsonTreeErr     string
	collapsedPaths  map[string]bool
	findMatchCapped bool
	policyConfirmed bool
}

var (
	projNetMu       sync.Mutex
	projNetByWeak   = map[weak.Pointer[projectState]]*projectNetworkState{}
	fallbackProjNet *projectNetworkState

	tabNetMu     sync.Mutex
	tabNetByWeak = map[weak.Pointer[Tab]]*tabNetworkState{}
)

func projectNetStateFor(ps *projectState, a *App) *projectNetworkState {
	projNetMu.Lock()
	defer projNetMu.Unlock()

	if ps == nil {
		if fallbackProjNet == nil {
			fallbackProjNet = initProjectNetworkState("default", "")
		}
		return fallbackProjNet
	}

	key := weak.Make(ps)
	if st, ok := projNetByWeak[key]; ok && st != nil {
		return st
	}

	projID := ps.name()
	scopeKey := projID
	if sc, err := platform.ProjectScopeKey(projID, ps.root); err == nil && sc != "" {
		scopeKey = sc
	}
	storePath := ""
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && strings.TrimSpace(a.mgr.cfg.path) != "" {
		storePath = filepath.Join(filepath.Dir(a.mgr.cfg.path), "network.json")
	}

	st := initProjectNetworkState(scopeKey, storePath)
	projNetByWeak[key] = st
	runtime.AddCleanup(ps, func(k weak.Pointer[projectState]) {
		projNetMu.Lock()
		delete(projNetByWeak, k)
		projNetMu.Unlock()
	}, key)
	return st
}

func initProjectNetworkState(scopeKey, storePath string) *projectNetworkState {
	ns := platform.NewNetworkStore(storePath)
	dev := ns.DeviceSettings()
	st := &projectNetworkState{
		scopeKey:           scopeKey,
		cookieJar:          core.NewCookieJar(scopeKey),
		netStore:           ns,
		cookieUI:           newCookieModalState(),
		dialTimeoutDraft:   dev.Timeouts.DialTimeout.String(),
		tlsTimeoutDraft:    dev.Timeouts.TLSHandshakeTimeout.String(),
		ttfbTimeoutDraft:   dev.Timeouts.ResponseHeaderTimeout.String(),
		redirectModeIdx:    redirectModeToIdx(dev.Redirect.EffectiveMode()),
		maxRedirectsDraft:  strconv.Itoa(dev.Redirect.EffectiveMaxHops()),
		sendCookies:        true,
		proxyModeIdx:       proxyModeToIdx(dev.Proxy.EffectiveMode()),
		proxyURLDraft:      dev.Proxy.URL,
		noProxyDraft:       dev.Proxy.NoProxy,
		proxyUserDraft:     dev.Proxy.Username,
		proxyPassDraft:     dev.Proxy.Password,
		proxySecretDraft:   dev.Proxy.PasswordSecret,
		useSystemCA:        dev.TLS.SystemCAEnabled(),
		customCASlot:       strings.TrimPrefix(dev.TLS.CustomCARef, core.LocalFileRefPrefix),
		clientCertSlot:     strings.TrimPrefix(dev.TLS.ClientCertRef, core.LocalFileRefPrefix),
		clientKeySlot:      strings.TrimPrefix(dev.TLS.ClientKeyRef, core.LocalFileRefPrefix),
		sniServerNameDraft: dev.TLS.ServerName,
		prodActionIdx:      prodActionToIdx(dev.DevicePolicy.ProductionAction),
		deniedHostsDraft:   strings.Join(dev.DevicePolicy.DeniedHosts, ", "),
		allowedHostsDraft:  strings.Join(dev.DevicePolicy.AllowedHosts, ", "),
		enforceTLSVerify:   !dev.DevicePolicy.AllowInsecureTLS,
		requireHTTPSCreds:  !dev.DevicePolicy.AllowPlainHTTPWithCreds,
	}
	if st.customCASlot == "" {
		st.customCASlot = "corp_ca"
	}
	if st.clientCertSlot == "" {
		st.clientCertSlot = "client_cert"
	}
	if st.clientKeySlot == "" {
		st.clientKeySlot = "client_key"
	}
	st.customCAPath, _ = ns.ResolveCertSlot(scopeKey, st.customCASlot)
	st.clientCertPath, _ = ns.ResolveCertSlot(scopeKey, st.clientCertSlot)
	st.clientKeyPath, _ = ns.ResolveCertSlot(scopeKey, st.clientKeySlot)
	return st
}

func tabNetStateFor(tab *Tab) *tabNetworkState {
	if tab == nil {
		return &tabNetworkState{collapsedPaths: make(map[string]bool)}
	}
	tabNetMu.Lock()
	defer tabNetMu.Unlock()
	key := weak.Make(tab)
	if st, ok := tabNetByWeak[key]; ok && st != nil {
		return st
	}
	st := &tabNetworkState{
		collapsedPaths: make(map[string]bool),
	}
	tabNetByWeak[key] = st
	runtime.AddCleanup(tab, func(k weak.Pointer[Tab]) {
		tabNetMu.Lock()
		delete(tabNetByWeak, k)
		tabNetMu.Unlock()
	}, key)
	return st
}

func redirectModeToIdx(m core.RedirectMode) int {
	switch m {
	case core.RedirectNone:
		return 1
	case core.RedirectSameOriginOnly:
		return 2
	default:
		return 0
	}
}

func idxToRedirectMode(idx int) core.RedirectMode {
	switch idx {
	case 1:
		return core.RedirectNone
	case 2:
		return core.RedirectSameOriginOnly
	default:
		return core.RedirectFollow
	}
}

func proxyModeToIdx(m core.ProxyMode) int {
	switch m {
	case core.ProxyNone:
		return 1
	case core.ProxyHTTP:
		return 2
	case core.ProxySOCKS5:
		return 3
	default:
		return 0
	}
}

func idxToProxyMode(idx int) core.ProxyMode {
	switch idx {
	case 1:
		return core.ProxyNone
	case 2:
		return core.ProxyHTTP
	case 3:
		return core.ProxySOCKS5
	default:
		return core.ProxySystem
	}
}

func prodActionToIdx(a core.PolicyAction) int {
	switch a {
	case core.ActionAllowReadOnly:
		return 1
	case core.ActionDeny:
		return 2
	case core.ActionAllow:
		return 3
	default:
		return 0
	}
}

func idxToProdAction(idx int) core.PolicyAction {
	switch idx {
	case 1:
		return core.ActionAllowReadOnly
	case 2:
		return core.ActionDeny
	case 3:
		return core.ActionAllow
	default:
		return core.ActionConfirm
	}
}

// applyNetworkDrafts validates and saves the current modal drafts into the
// project's NetworkStore.
func (st *projectNetworkState) applyNetworkDrafts() error {
	if st == nil || st.netStore == nil {
		return nil
	}
	dialDur, err := parseOptionalDuration(st.dialTimeoutDraft, core.DefaultDialTimeout)
	if err != nil {
		return fmt.Errorf("dial timeout: %w", err)
	}
	tlsDur, err := parseOptionalDuration(st.tlsTimeoutDraft, core.DefaultTLSHandshakeTimeout)
	if err != nil {
		return fmt.Errorf("TLS handshake timeout: %w", err)
	}
	ttfbDur, err := parseOptionalDuration(st.ttfbTimeoutDraft, core.DefaultResponseHeaderTimeout)
	if err != nil {
		return fmt.Errorf("TTFB response header timeout: %w", err)
	}
	maxHops := core.DefaultMaxRedirects
	if strings.TrimSpace(st.maxRedirectsDraft) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(st.maxRedirectsDraft))
		if err != nil || n < 0 || n > 50 {
			return fmt.Errorf("max redirects must be an integer between 0 and 50")
		}
		maxHops = n
	}

	// Bind local certificate / private key paths if specified.
	type slotBind struct {
		slot string
		path string
	}
	for _, b := range []slotBind{
		{st.customCASlot, st.customCAPath},
		{st.clientCertSlot, st.clientCertPath},
		{st.clientKeySlot, st.clientKeyPath},
	} {
		if strings.TrimSpace(b.path) != "" {
			if err := st.netStore.BindCertSlot(st.scopeKey, b.slot, b.path); err != nil {
				return err
			}
		} else if strings.TrimSpace(b.slot) != "" {
			_ = st.netStore.BindCertSlot(st.scopeKey, b.slot, "")
		}
	}

	useSys := st.useSystemCA
	customCARef := ""
	if strings.TrimSpace(st.customCAPath) != "" && strings.TrimSpace(st.customCASlot) != "" {
		customCARef = core.FormatLocalFileRef(strings.TrimPrefix(strings.TrimSpace(st.customCASlot), core.LocalFileRefPrefix))
	}
	clientCertRef := ""
	if strings.TrimSpace(st.clientCertPath) != "" && strings.TrimSpace(st.clientCertSlot) != "" {
		clientCertRef = core.FormatLocalFileRef(strings.TrimPrefix(strings.TrimSpace(st.clientCertSlot), core.LocalFileRefPrefix))
	}
	clientKeyRef := ""
	if strings.TrimSpace(st.clientKeyPath) != "" && strings.TrimSpace(st.clientKeySlot) != "" {
		clientKeyRef = core.FormatLocalFileRef(strings.TrimPrefix(strings.TrimSpace(st.clientKeySlot), core.LocalFileRefPrefix))
	}

	cfg := platform.DeviceNetworkSettings{
		Timeouts: core.StageTimeouts{
			DialTimeout:           dialDur,
			TLSHandshakeTimeout:   tlsDur,
			ResponseHeaderTimeout: ttfbDur,
		},
		Redirect: core.RedirectPolicy{
			Mode:    idxToRedirectMode(st.redirectModeIdx),
			MaxHops: maxHops,
		},
		Proxy: core.ProxyConfig{
			Mode:           idxToProxyMode(st.proxyModeIdx),
			URL:            strings.TrimSpace(st.proxyURLDraft),
			NoProxy:        strings.TrimSpace(st.noProxyDraft),
			Username:       strings.TrimSpace(st.proxyUserDraft),
			Password:       st.proxyPassDraft,
			PasswordSecret: strings.TrimSpace(st.proxySecretDraft),
		},
		TLS: core.TLSConfig{
			UseSystemCA:   &useSys,
			CustomCARef:   customCARef,
			ClientCertRef: clientCertRef,
			ClientKeyRef:  clientKeyRef,
			ServerName:    strings.TrimSpace(st.sniServerNameDraft),
		},
		DevicePolicy: core.DeviceTargetPolicy{
			ProductionAction:        idxToProdAction(st.prodActionIdx),
			DeniedHosts:             splitCommaList(st.deniedHostsDraft),
			AllowedHosts:            splitCommaList(st.allowedHostsDraft),
			AllowInsecureTLS:        !st.enforceTLSVerify,
			AllowPlainHTTPWithCreds: !st.requireHTTPSCreds,
		},
		MaxBodyBytes: core.DefaultMaxBody,
	}
	return st.netStore.SaveDeviceSettings(cfg)
}

func parseOptionalDuration(raw string, def time.Duration) (time.Duration, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return def, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, fmt.Errorf("duration cannot be negative")
	}
	return d, nil
}

func splitCommaList(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		v := strings.TrimSpace(p)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// buildExecuteOptionsForTab constructs the full core.ExecuteOptions for a
// request Tab, combining the project's isolated CookieJar, device/project
// network settings, local CA/mTLS slot bindings, and B05 target policy.
func (a *App) buildExecuteOptionsForTab(
	ps *projectState,
	tab *Tab,
	req *core.Request,
	auth *core.AuthScheme,
	red *core.Redactor,
) core.ExecuteOptions {
	if ps == nil && tab != nil && tab.Owner != nil {
		ps = tab.Owner
	}
	if ps == nil && a != nil {
		ps = a.ps
	}
	st := projectNetStateFor(ps, a)
	ts := tabNetStateFor(tab)

	dev := st.netStore.DeviceSettings()
	pol := core.MergeTargetPolicy(&dev.DevicePolicy, nil)

	var envID, projectRoot string
	if ps != nil {
		envID = ps.currentEnv()
		if ps.project != nil {
			projectRoot = ps.project.Dir
			if auth == nil && req != nil {
				if eff, err := ps.project.ResolveEffectiveAuth(tab.Entry, req); err == nil && eff != nil {
					auth = eff.Scheme
				}
			}
		}
	}

	localFiles := st.netStore.LocalFileProviderFor(st.scopeKey, a.localFilesFor(ps))
	return core.ExecuteOptions{
		Timeouts:         dev.Timeouts,
		Redirect:         dev.Redirect,
		Proxy:            dev.Proxy,
		TLS:              dev.TLS,
		CookieJar:        st.cookieJar,
		DisableCookies:   !st.sendCookies,
		TargetPolicy:     pol,
		HasTargetPolicy:  true,
		EnvID:            envID,
		AuthScheme:       auth,
		Redactor:         red,
		Interactive:      true,
		Confirmed:        ts.policyConfirmed,
		ProjectRoot:      projectRoot,
		LocalFiles:       localFiles,
		Secrets:          a.mgrSecrets(),
		MaxResponseBytes: dev.MaxBodyBytes,
	}
}

// attachNetworkToPlan attaches the tab's ExecuteOptions onto plan.Network so
// core.Execute(ctx, plan, nil) honors all B04 transport, proxy, TLS/mTLS,
// redirect, CookieJar, and target policy settings.
func (a *App) attachNetworkToPlan(tab *Tab, req *core.Request, plan *core.Plan, red *core.Redactor) {
	if plan == nil {
		return
	}
	var ps *projectState
	if tab != nil && tab.Owner != nil {
		ps = tab.Owner
	} else if a != nil {
		ps = a.ps
	}
	opts := a.buildExecuteOptionsForTab(ps, tab, req, nil, red)
	plan.Network = &opts
}

// executeTabPlan executes plan with the tab's isolated CookieJar, network
// transport, and target policy options.
func (a *App) executeTabPlan(ctx context.Context, tab *Tab, req *core.Request, plan *core.Plan, red *core.Redactor) (*core.ExecResult, error) {
	a.attachNetworkToPlan(tab, req, plan, red)
	return core.Execute(ctx, plan, nil)
}

// openNetworkSettingsModal opens the Network / TLS / Proxy / Target Policy
// configuration dialog.
func (a *App) openNetworkSettingsModal(ps *projectState) {
	if ps == nil && a != nil {
		ps = a.ps
	}
	st := projectNetStateFor(ps, a)
	if st == nil {
		return
	}
	st.netErrMsg = ""
	// Drafts persist across modal open/close, so recompute the inline FIX08
	// feedback for whatever is currently in the fields instead of showing a
	// stale error (or hiding a still-invalid draft).
	st.timeoutErr = ""
	if err := validateStageTimeoutDrafts(st, a.networkMsg()); err != nil {
		st.timeoutErr = fmt.Sprintf(a.networkMsg().NetworkDraftsInvalidFmt, err)
	}
	st.netModalOpen = true
}

func (a *App) networkSettingsModal(c *ui.Context, t *ui.Theme, tab *Tab) {
	ps := a.ps
	if tab != nil && tab.Owner != nil {
		ps = tab.Owner
	}
	st := projectNetStateFor(ps, a)
	if st == nil || !st.netModalOpen {
		return
	}
	nm := a.networkMsg()
	m := a.msg()

	modal := ui.Modal(c, &st.netModalOpen, func() {
		ui.Column(c).Width(680).MaxHeight(600).Gap(10).Padding(16).Children(func() {
			ui.Text(c, nm.NetworkModalTitle).FontSize(16).Bold()
			ui.Scroll(c).MaxHeight(470).FillWidth().Children(func() {
				a.renderRequestNetworkSettings(c, t, tab)
			})
			if st.netErrMsg != "" {
				ui.Text(c, st.netErrMsg).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).FillWidth().Children(func() {
				if uikit.ActionCursor(ui.Button(c, m.Close)).Clicked() {
					// Cancel: keep the user's drafts (valid or not) so they can
					// fix them next time; device settings were never overridden
					// by an invalid value, so the last valid limits stay live.
					st.netErrMsg = ""
					st.netModalOpen = false
				}
				if uikit.ActionCursor(ui.PrimaryButton(c, nm.SaveNetworkSettingsBtn)).Clicked() {
					// FIX08: an invalid stage-timeout draft must not be
					// silently ignored. Route its feedback to the field; keep
					// the modal open so the user can correct or cancel.
					if err := st.applyNetworkDraftsFromUI(nm); err != nil {
						if st.timeoutErr == "" {
							st.netErrMsg = err.Error()
						} else {
							st.netErrMsg = ""
						}
					} else {
						st.netErrMsg = ""
						st.timeoutErr = ""
						st.netModalOpen = false
						a.status = nm.NetworkSettingsSaved
					}
				}
			})
		})
	})
	if modal.Dismissed() {
		// Dismissing is a cancel too: preserve the drafts and the last valid
		// device settings; only clear the transient modal-level save message.
		st.netErrMsg = ""
		st.netModalOpen = false
	}
}

// renderRequestNetworkSettings renders the stage timeouts, redirect policy,
// HTTP/SOCKS5 proxy, custom CA / mTLS slot bindings, and target policy controls.
// It can be embedded in the request Settings tab (buildSettings) as well as the
// Network Settings modal.
func (a *App) renderRequestNetworkSettings(c *ui.Context, t *ui.Theme, tab *Tab) {
	ps := a.ps
	if tab != nil && tab.Owner != nil {
		ps = tab.Owner
	}
	st := projectNetStateFor(ps, a)
	nm := a.networkMsg()
	p := uikit.PaletteFor(t)

	ui.Column(c).Gap(12).FillWidth().Padding(4, 2).Children(func() {
		// 1. Request-level overall timeout (FIX08): a clearly named entry with
		// units, examples, the effective value + its source, and inline error
		// feedback. Rendered first so it is not buried under technical settings.
		a.renderRequestTimeoutCard(c, t, tab, st, nm)

		// 2. Device-local phase timeouts (FIX08): kept visually and semantically
		// separate from the request-level timeout above.
		a.renderStageTimeoutCard(c, t, st, nm)

		// 3. Redirect Policy & Cookie Jar
		ui.Column(c).Gap(6).Padding(8, 10).BorderWidth(1).BorderColor(p.Hairline).Radius(8).FillWidth().Children(func() {
			ui.Text(c, nm.RedirectHeader).FontSize(12).FontWeight(600)
			ui.Row(c).Gap(10).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, nm.RedirectModeLabel).FontSize(12).TextColor(p.Muted)
				prevMode := st.redirectModeIdx
				uikit.NativeSegmented(c, &st.redirectModeIdx, nm.RedirectFollowOpt, nm.RedirectNoneOpt, nm.RedirectSameOriginOpt)
				if st.redirectModeIdx != prevMode {
					_ = st.applyNetworkDrafts()
				}
			})
			ui.Row(c).Gap(12).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, nm.MaxRedirectsLabel).FontSize(12).TextColor(p.Muted)
				if ui.TextInput(c, &st.maxRedirectsDraft).Width(70).Font("monospace").Placeholder("10").Changed() {
					_ = st.applyNetworkDrafts()
				}
				uikit.ActionCursor(ui.Checkbox(c, &st.sendCookies, nm.SendJarCookiesLabel))
				ui.Box(c).Grow(1)
				if uikit.ActionCursor(ui.Button(c, nm.OpenCookieJarBtn)).Clicked() {
					a.openCookieJarModal(ps)
				}
			})
		})

		// 4. Proxy Configuration (Direct / System / HTTP / SOCKS5)
		ui.Column(c).Gap(6).Padding(8, 10).BorderWidth(1).BorderColor(p.Hairline).Radius(8).FillWidth().Children(func() {
			ui.Text(c, nm.ProxyHeader).FontSize(12).FontWeight(600)
			ui.Row(c).Gap(10).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, nm.ProxyModeLabel).FontSize(12).TextColor(p.Muted)
				prevProxy := st.proxyModeIdx
				uikit.NativeSegmented(c, &st.proxyModeIdx, "system", "none", "http", "socks5")
				if st.proxyModeIdx != prevProxy {
					_ = st.applyNetworkDrafts()
				}
			})
			if st.proxyModeIdx == 2 || st.proxyModeIdx == 3 {
				ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
					if ui.TextInput(c, &st.proxyURLDraft).Grow(1).Font("monospace").Placeholder(nm.ProxyURLPlaceholder).Changed() {
						_ = st.applyNetworkDrafts()
					}
					if ui.TextInput(c, &st.noProxyDraft).Width(220).Font("monospace").Placeholder(nm.NoProxyPlaceholder).Changed() {
						_ = st.applyNetworkDrafts()
					}
				})
				ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
					if ui.TextInput(c, &st.proxyUserDraft).Width(160).Font("monospace").Placeholder(nm.ProxyUserPlaceholder).Changed() {
						_ = st.applyNetworkDrafts()
					}
					if ui.TextInput(c, &st.proxyPassDraft).Width(180).Font("monospace").Placeholder(nm.ProxyPassPlaceholder).Changed() {
						_ = st.applyNetworkDrafts()
					}
					if ui.TextInput(c, &st.proxySecretDraft).Grow(1).Font("monospace").Placeholder(nm.ProxySecretPlaceholder).Changed() {
						_ = st.applyNetworkDrafts()
					}
				})
			}
		})

		// 5. TLS, Custom CA & mTLS Local Slot Bindings
		ui.Column(c).Gap(6).Padding(8, 10).BorderWidth(1).BorderColor(p.Hairline).Radius(8).FillWidth().Children(func() {
			ui.Text(c, nm.TLSHeader).FontSize(12).FontWeight(600)
			ui.Text(c, nm.MTLSKeyLocalOnlyNote).FontSize(11).TextColor(p.Muted)
			ui.Row(c).Gap(12).AlignItems(ui.Center).FillWidth().Children(func() {
				prevSys := st.useSystemCA
				uikit.ActionCursor(ui.Checkbox(c, &st.useSystemCA, nm.UseSystemCALabel))
				if st.useSystemCA != prevSys {
					_ = st.applyNetworkDrafts()
				}
				ui.Text(c, nm.SNIServerNameLabel).FontSize(12).TextColor(p.Muted)
				if ui.TextInput(c, &st.sniServerNameDraft).Grow(1).Font("monospace").Placeholder("api.example.com").Changed() {
					_ = st.applyNetworkDrafts()
				}
			})
			// Custom CA slot -> local path
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, nm.CustomCASlotLabel).Width(190).FontSize(11).TextColor(p.Muted)
				ui.TextInput(c, &st.customCASlot).Width(110).Font("monospace").Placeholder("corp_ca")
				if ui.TextInput(c, &st.customCAPath).Grow(1).Font("monospace").Placeholder(nm.LocalPathPlaceholder).Changed() {
					_ = st.applyNetworkDrafts()
				}
			})
			// mTLS Client Cert slot -> local path
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, nm.ClientCertSlotLabel).Width(190).FontSize(11).TextColor(p.Muted)
				ui.TextInput(c, &st.clientCertSlot).Width(110).Font("monospace").Placeholder("client_cert")
				if ui.TextInput(c, &st.clientCertPath).Grow(1).Font("monospace").Placeholder(nm.LocalPathPlaceholder).Changed() {
					_ = st.applyNetworkDrafts()
				}
			})
			// mTLS Client Private Key slot -> local path (strictly local:<slot>)
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, nm.ClientKeySlotLabel).Width(190).FontSize(11).TextColor(p.Muted)
				ui.TextInput(c, &st.clientKeySlot).Width(110).Font("monospace").Placeholder("client_key")
				if ui.TextInput(c, &st.clientKeyPath).Grow(1).Font("monospace").Placeholder(nm.LocalPathPlaceholder).Changed() {
					_ = st.applyNetworkDrafts()
				}
			})
		})

		// 6. Device Target & Production Policy Guardrails
		ui.Column(c).Gap(6).Padding(8, 10).BorderWidth(1).BorderColor(p.Hairline).Radius(8).FillWidth().Children(func() {
			ui.Text(c, nm.TargetPolicyHeader).FontSize(12).FontWeight(600)
			ui.Row(c).Gap(10).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, nm.ProdActionLabel).FontSize(12).TextColor(p.Muted)
				prevAct := st.prodActionIdx
				uikit.NativeSegmented(c, &st.prodActionIdx, "confirm", "allow_readonly", "deny", "allow")
				if st.prodActionIdx != prevAct {
					_ = st.applyNetworkDrafts()
				}
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, nm.DeniedHostsLabel).Width(190).FontSize(11).TextColor(p.Muted)
				if ui.TextInput(c, &st.deniedHostsDraft).Grow(1).Font("monospace").Placeholder("*.prod.internal").Changed() {
					_ = st.applyNetworkDrafts()
				}
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, nm.AllowedHostsLabel).Width(190).FontSize(11).TextColor(p.Muted)
				if ui.TextInput(c, &st.allowedHostsDraft).Grow(1).Font("monospace").Changed() {
					_ = st.applyNetworkDrafts()
				}
			})
			ui.Row(c).Gap(14).AlignItems(ui.Center).FillWidth().Children(func() {
				prevEnf := st.enforceTLSVerify
				uikit.ActionCursor(ui.Checkbox(c, &st.enforceTLSVerify, nm.EnforceTLSVerifyLabel))
				prevHTTPS := st.requireHTTPSCreds
				uikit.ActionCursor(ui.Checkbox(c, &st.requireHTTPSCreds, nm.RequireHTTPSCredsLabel))
				if st.enforceTLSVerify != prevEnf || st.requireHTTPSCreds != prevHTTPS {
					_ = st.applyNetworkDrafts()
				}
			})
		})
	})
}

// responseNetworkView renders the Network & Redirects diagnostic tab inside the
// Response panel, showing wire transfer size vs decompressed body size,
// observed stage timings, TLS/mTLS metadata, and redirect hop audit records.
func (a *App) responseNetworkView(c *ui.Context, t *ui.Theme, res *core.ExecResult, p uikit.Palette) {
	nm := a.networkMsg()
	if res == nil {
		return
	}
	tr := res.Trace
	if tr == nil {
		tr = &core.NetworkTrace{}
	}

	transferBytes := res.TransferSize
	if transferBytes <= 0 && res.BodySize > 0 {
		transferBytes = res.BodySize
	}
	encLabel := res.ContentEncoding
	if encLabel == "" {
		encLabel = "identity"
	}

	ui.Scroll(c).Grow(1).FillWidth().Padding(8, 12).Children(func() {
		ui.Column(c).Gap(10).FillWidth().Children(func() {
			// 1. Transfer Size vs Decompressed Body Size & Connection Summary
			ui.Column(c).Gap(4).Padding(8, 10).BorderWidth(1).BorderColor(p.Hairline).Radius(8).FillWidth().Children(func() {
				ui.Text(c, nm.TraceSummaryHeader).FontSize(12).FontWeight(600)
				traceKVRow(c, p, nm.TraceTransferSize, fmt.Sprintf("%d B (%s)", transferBytes, formatByteSize(transferBytes)))
				traceKVRow(c, p, nm.TraceDecompressedSize, fmt.Sprintf("%d B (%s)", res.BodySize, formatByteSize(res.BodySize)))
				traceKVRow(c, p, nm.TraceEncoding, encLabel)
				if tr.RemoteAddr != "" {
					traceKVRow(c, p, nm.TraceRemoteAddr, tr.RemoteAddr)
				}
				reusedStr := nm.NoLabel
				if tr.ReusedConn {
					reusedStr = nm.YesLabel
				}
				traceKVRow(c, p, nm.TraceConnReused, reusedStr)
				if tr.ProxyUsed != "" {
					traceKVRow(c, p, nm.TraceProxyUsed, tr.ProxyUsed)
				}
			})

			// 2. Stage Timings (tnum, never faking 0ms for unobserved stages)
			ui.Column(c).Gap(4).Padding(8, 10).BorderWidth(1).BorderColor(p.Hairline).Radius(8).FillWidth().Children(func() {
				ui.Text(c, nm.StageTimeoutsHeader).FontSize(12).FontWeight(600)
				traceKVRow(c, p, nm.TraceStageDNS, formatObservedDuration(tr.DNSDuration, tr.DNSObserved, nm.TraceNotObserved))
				traceKVRow(c, p, nm.TraceStageConnect, formatObservedDuration(tr.ConnectDuration, tr.ConnectObserved, nm.TraceNotObserved))
				traceKVRow(c, p, nm.TraceStageTLS, formatObservedDuration(tr.TLSDuration, tr.TLSObserved, nm.TraceNotObserved))
				traceKVRow(c, p, nm.TraceStageWrite, formatObservedDuration(tr.WriteDuration, tr.WriteObserved, nm.TraceNotObserved))
				traceKVRow(c, p, nm.TraceStageTTFB, formatObservedDuration(tr.TTFBDuration, tr.TTFBObserved, nm.TraceNotObserved))
				traceKVRow(c, p, nm.TraceStageDownload, fmt.Sprintf("%.2f ms", float64(tr.DownloadDuration.Microseconds())/1000.0))
			})

			// 3. TLS & Certificate Details (if HTTPS or TLS metadata present)
			if tr.TLSVersion != "" || tr.CustomCAUsed || tr.ClientCertUsed || tr.TLSInsecureSkipVerify {
				ui.Column(c).Gap(4).Padding(8, 10).BorderWidth(1).BorderColor(p.Hairline).Radius(8).FillWidth().Children(func() {
					ui.Text(c, nm.TraceTLSHeader).FontSize(12).FontWeight(600)
					if tr.TLSInsecureSkipVerify {
						ui.Text(c, nm.TLSInsecureBadge).FontSize(12).FontWeight(600).TextColor(uikit.MethodColor("DELETE", p))
					}
					if tr.TLSVersion != "" {
						traceKVRow(c, p, "Protocol / Cipher", strings.TrimSpace(tr.TLSVersion+" · "+tr.TLSCipherSuite))
					}
					if tr.TLSServerName != "" {
						traceKVRow(c, p, "SNI ServerName", tr.TLSServerName)
					}
					if tr.CustomCAUsed {
						traceKVRow(c, p, "Custom CA", nm.YesLabel)
					}
					if tr.ClientCertUsed {
						traceKVRow(c, p, "mTLS Client Cert", nm.YesLabel)
					}
				})
			}

			// 4. Redirect Hop Chain
			ui.Column(c).Gap(4).Padding(8, 10).BorderWidth(1).BorderColor(p.Hairline).Radius(8).FillWidth().Children(func() {
				ui.Text(c, nm.TraceRedirectsHeader).FontSize(12).FontWeight(600)
				if len(res.Redirects) == 0 {
					ui.Text(c, nm.TraceNoRedirects).FontSize(12).TextColor(p.Muted)
				} else {
					for i, hop := range res.Redirects {
						hop := hop
						ui.Row(c).Key(i).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
							ui.Text(c, fmt.Sprintf("#%d %d %s", hop.Hop, hop.StatusCode, hop.Method)).
								Width(90).FontSize(11).Font("monospace").FontFeatures("tnum").FontWeight(600).TextColor(p.Text)
							ui.Text(c, fmt.Sprintf("%s -> %s", hop.FromURL, hop.ToURL)).
								Grow(1).FontSize(11).Font("monospace").SingleLine().Selectable().TextColor(p.Muted)
							if hop.OriginChanged {
								ui.Text(c, nm.TraceCrossOriginBadge).FontSize(10).TextColor(uikit.MethodColor("POST", p))
							}
							if hop.StrippedCredentials {
								ui.Text(c, nm.TraceStrippedBadge).FontSize(10).FontWeight(600).TextColor(uikit.MethodColor("GET", p))
							}
						})
					}
				}
			})
		})
	})
}

func traceKVRow(c *ui.Context, p uikit.Palette, label, val string) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).FillWidth().Children(func() {
		ui.Text(c, label).Width(210).FontSize(12).TextColor(p.Muted).SingleLine()
		ui.Text(c, val).Grow(1).FontSize(12).Font("monospace").FontFeatures("tnum").Selectable().TextColor(p.Text).SingleLine()
	})
}

func formatObservedDuration(d time.Duration, observed bool, fallback string) string {
	if !observed {
		return fallback
	}
	return fmt.Sprintf("%.2f ms", float64(d.Microseconds())/1000.0)
}

func formatByteSize(b int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
	)
	switch {
	case b >= mb:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

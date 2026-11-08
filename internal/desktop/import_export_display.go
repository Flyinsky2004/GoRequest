package desktop

// Localized rendering of B08 interop diagnostics.
//
// The core interop layer stays language-neutral: it reports stable machine facts
// (LossItem.Code/Severity/SourcePath/TargetPath/Field, IDMapping actions, change
// kinds, format/version, counts) plus English developer messages. This file is
// the display adapter: it turns those facts into Chinese for the current UI
// language while preserving technical identifiers (codes, IDs, paths, JSON keys,
// request data) verbatim. English mode returns the original core text, so no
// meaning is lost and switching languages re-renders on the next frame.

import (
	"fmt"
	"regexp"
	"strings"

	"gorequest/internal/core"
)

// interopIsZH reports whether interop surfaces should render Chinese.
func (a *App) interopIsZH() bool { return a.interopLanguage() != langEnglish }

// displayFormat returns a localized display name for an interop format. The
// machine code remains visible separately in the preview header.
func (a *App) displayFormat(f core.InteropFormat) string {
	im := a.interopMsg()
	switch f {
	case core.FormatCURL:
		return im.FormatCURL
	case core.FormatPostmanCollection:
		return im.FormatPostmanCollection
	case core.FormatPostmanEnvironment:
		return im.FormatPostmanEnvironment
	case core.FormatPortableBundle:
		return im.FormatPortableBundle
	case core.FormatOpenAPI:
		return im.FormatOpenAPI
	case core.FormatSwagger2:
		return im.FormatSwagger2
	default:
		return string(f)
	}
}

// displayImportSummary localizes the core preview summary. Known core templates
// are re-rendered with their dynamic values; an unrecognized summary falls back
// to a structured Chinese summary built from counts, never to a half-English
// sentence.
func (a *App) displayImportSummary(prev core.ImportPreview) string {
	if !a.interopIsZH() {
		return prev.Summary
	}
	if s := translateSummaryZH(prev.Summary); s != "" {
		return s
	}
	im := a.interopMsg()
	return fmt.Sprintf(im.SummaryFallback, a.displayFormat(prev.Format), len(prev.Changes), len(prev.Losses), len(prev.Conflicts))
}

type summaryTranslation struct {
	re   *regexp.Regexp
	text string
}

var interopSummaryZH = []summaryTranslation{
	{regexp.MustCompile(`^cURL import: (\S+) (\S+) -> (\S+)$`), "cURL 导入：%s %s → %s"},
	{regexp.MustCompile(`^cURL import blocked by unsafe shell construct or invalid invocation$`), "cURL 导入已被阻止：包含不安全的 shell 构造或无效调用"},
	{regexp.MustCompile(`^cURL import blocked by (\d+) conflict\(s\)$`), "cURL 导入因 %s 项冲突被阻止"},
	{regexp.MustCompile(`^Postman Collection v1\.0 legacy format is not supported; please export as Postman Collection v2\.1\.0 or v2\.0\.0$`), "不支持 Postman Collection v1.0 旧格式；请导出为 Postman Collection v2.1.0 或 v2.0.0"},
	{regexp.MustCompile(`^unsupported Postman Collection schema "([^"]*)" \(supported versions: v2\.1\.0, v2\.0\.0\)$`), "不支持的 Postman Collection schema %q（支持版本：v2.1.0、v2.0.0）"},
	{regexp.MustCompile(`^Postman Collection \(([^)]*)\) "([^"]*)": (\d+) file change\(s\), (\d+) loss/warning note\(s\)$`), "Postman 集合（%s）%q：%s 项文件变更，%s 项损失/警告"},
	{regexp.MustCompile(`^Postman Collection \(([^)]*)\) "([^"]*)" blocked by (\d+) conflict\(s\)$`), "Postman 集合（%s）%q 因 %s 项冲突被阻止"},
	{regexp.MustCompile(`^Postman Environment "([^"]*)" -> (\S+) \((\d+) variable\(s\), (\d+) secret slot\(s\)\)$`), "Postman 环境 %q → %s（%s 个变量，%s 个 Secret 槽位）"},
	{regexp.MustCompile(`^Postman Environment "([^"]*)" blocked by conflict$`), "Postman 环境 %q 因冲突被阻止"},
	{regexp.MustCompile(`^Portable Bundle \(([^)]*)\) from "([^"]*)": (\d+) file change\(s\)$`), "便携包（%s，来源 %q）：%s 项文件变更"},
	{regexp.MustCompile(`^Portable Bundle \(([^)]*)\) blocked by (\d+) conflict\(s\)$`), "便携包（%s）因 %s 项冲突被阻止"},
	{regexp.MustCompile(`^Blocked (OpenAPI|Swagger) (\S+) import: (\d+) validation/ref error\(s\)$`), "已阻止 %s %s 导入：%s 项校验/引用错误"},
	{regexp.MustCompile(`^Staged (OpenAPI|Swagger) (\S+) "([^"]*)": (\d+) operation\(s\), (\d+) schema\(s\), (\d+) file change\(s\)$`), "已生成 %s %s %q 的变更预览：%s 个操作、%s 个 Schema、%s 项文件变更"},
	{regexp.MustCompile(`^Blocked (OpenAPI|Swagger) (\S+) import: (\d+) conflict/loss block\(s\)$`), "已阻止 %s %s 导入：%s 项冲突/损失阻断"},
	{regexp.MustCompile(`^(.+?) specification detected \((.*?)\)\. Full OpenAPI 3\.x / Swagger 2\.0 contract import, export, and sync belong to task A03; B08 basic interop does not claim OpenAPI conversion\.$`), "检测到 %s 规范（%s）。OpenAPI 3.x / Swagger 2.0 契约的完整导入、导出与同步属于 A03 任务；B08 基础互操作不提供 OpenAPI 转换。"},
}

// translateSummaryZH returns "" when the English summary matches no known core
// template, letting the caller fall back to structured facts.
func translateSummaryZH(summary string) string {
	s := strings.TrimSpace(summary)
	if s == "" {
		return ""
	}
	for _, tr := range interopSummaryZH {
		m := tr.re.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		args := make([]any, 0, len(m)-1)
		for _, g := range m[1:] {
			args = append(args, g)
		}
		return fmt.Sprintf(tr.text, args...)
	}
	return ""
}

// displayConflict localizes one core conflict string. Unknown conflicts keep
// their original technical detail behind a Chinese "冲突" classification.
func (a *App) displayConflict(cf string) string {
	if !a.interopIsZH() {
		return cf
	}
	s := strings.TrimSpace(cf)
	if s == "" {
		return ""
	}
	if m := conflictEntityExistsRE.FindStringSubmatch(s); m != nil {
		return fmt.Sprintf("%s %q 已存在于 %s", a.displayEntityKind(m[1]), m[2], m[3])
	}
	if m := conflictAllocateIDRE.FindStringSubmatch(s); m != nil {
		return fmt.Sprintf("无法为 %s %q 分配唯一 ID", a.displayEntityKind(m[1]), m[2])
	}
	if m := conflictAttachmentRE.FindStringSubmatch(s); m != nil {
		return fmt.Sprintf("附件 %s 已存在于项目根目录", m[1])
	}
	if m := conflictAssetRE.FindStringSubmatch(s); m != nil {
		return fmt.Sprintf("资源 %s 已存在", m[1])
	}
	if m := conflictFolderMetaRE.FindStringSubmatch(s); m != nil {
		return fmt.Sprintf("文件夹集合元数据已存在于 %s", m[1])
	}
	return "冲突：" + s
}

var (
	conflictEntityExistsRE = regexp.MustCompile(`^(\w+) "([^"]*)" already exists at (\S+)$`)
	conflictAllocateIDRE   = regexp.MustCompile(`^unable to allocate unique ID for (\w+) "([^"]*)"$`)
	conflictAttachmentRE   = regexp.MustCompile(`^attachment (\S+) already exists in project root$`)
	conflictAssetRE        = regexp.MustCompile(`^asset (\S+) already exists$`)
	conflictFolderMetaRE   = regexp.MustCompile(`^folder collection metadata already exists at (\S+)$`)
)

func (a *App) displaySeverity(sev core.LossSeverity) string {
	if !a.interopIsZH() {
		return string(sev)
	}
	im := a.interopMsg()
	switch sev {
	case core.LossInfo:
		return im.SeverityInfo
	case core.LossWarning:
		return im.SeverityWarning
	case core.LossUnsupported:
		return im.SeverityUnsupported
	case core.LossSecurityScrubbed:
		return im.SeveritySecurityScrubbed
	case core.LossBlocked:
		return im.SeverityBlocked
	default:
		return string(sev)
	}
}

func (a *App) displayChangeKind(k core.ChangeKind) string {
	if !a.interopIsZH() {
		return strings.ToUpper(string(k))
	}
	im := a.interopMsg()
	switch k {
	case core.ChangeCreate:
		return im.ChangeKindCreate
	case core.ChangeUpdate:
		return im.ChangeKindUpdate
	case core.ChangeDelete:
		return im.ChangeKindDelete
	case core.ChangeMove:
		return im.ChangeKindMove
	default:
		return string(k)
	}
}

func (a *App) displayEntityKind(k string) string {
	if !a.interopIsZH() {
		return k
	}
	im := a.interopMsg()
	switch k {
	case "request":
		return im.EntityRequest
	case "folder":
		return im.EntityFolder
	case "environment":
		return im.EntityEnvironment
	case "auth":
		return im.EntityAuth
	case "doc":
		return im.EntityDoc
	case "asset":
		return im.EntityAsset
	case "attachment":
		return im.EntityAttachment
	default:
		return k
	}
}

func (a *App) displayAction(action string) string {
	if !a.interopIsZH() {
		return action
	}
	im := a.interopMsg()
	switch action {
	case "create":
		return im.ActionCreate
	case "update":
		return im.ActionUpdate
	case "rename":
		return im.ActionRename
	case "skip":
		return im.ActionSkip
	case "conflict":
		return im.ActionConflict
	default:
		return action
	}
}

func (a *App) displayVariableScope(scope string) string {
	if !a.interopIsZH() {
		return scope
	}
	im := a.interopMsg()
	switch scope {
	case "environment":
		return im.ScopeEnvironment
	case "collection":
		return im.ScopeCollection
	case "request-auth":
		return im.ScopeRequestAuth
	case "header":
		return im.ScopeHeader
	case "bundle":
		return im.ScopeBundle
	default:
		return scope
	}
}

func (a *App) displayVariableSource(source string) string {
	if !a.interopIsZH() {
		return source
	}
	im := a.interopMsg()
	switch source {
	case "postman-env":
		return im.SourcePostmanEnv
	case "postman-collection":
		return im.SourcePostmanCollection
	case "curl-auth":
		return im.SourceCurlAuth
	case "curl-header":
		return im.SourceCurlHeader
	case "portable":
		return im.SourcePortable
	default:
		return source
	}
}

// displayChangeLine renders one staged file change. The relative path and byte
// count are technical facts and stay verbatim.
func (a *App) displayChangeLine(ch core.ChangePreview) string {
	im := a.interopMsg()
	if !a.interopIsZH() {
		return fmt.Sprintf(im.ChangeLineFmt, strings.ToUpper(string(ch.Kind)), ch.RelPath, fmt.Sprintf(im.BytesFmt, ch.AfterBytes))
	}
	return fmt.Sprintf(im.ChangeLineFmt, a.displayChangeKind(ch.Kind), ch.RelPath, fmt.Sprintf(im.BytesFmt, ch.AfterBytes))
}

// displayIDMappingLine renders an entity ID/reference mapping with localized
// entity kind, action and rename tag; IDs and paths stay verbatim.
func (a *App) displayIDMappingLine(mp core.IDMapping) string {
	im := a.interopMsg()
	renameTag := ""
	if mp.Renamed {
		renameTag = im.RenamedTag
	}
	return fmt.Sprintf(im.IDMappingLineFmt, a.displayEntityKind(mp.EntityKind), mp.SourceID, mp.TargetID, mp.TargetPath, a.displayAction(mp.Action), renameTag)
}

// displayVariableLine renders a variable/secret source note. Names, mapped refs
// and masked previews are technical/user data and stay verbatim.
func (a *App) displayVariableLine(v core.VariableSourceNote) string {
	im := a.interopMsg()
	kind := im.VarKindVar
	if v.IsSecret {
		kind = im.VarKindSecret
	}
	return fmt.Sprintf(im.VariableLineFmt, kind, v.Name, v.MappedRef, v.MaskedPreview, a.displayVariableSource(v.Source))
}

// displayLocalFileSlot renders a logical local-file slot. The slot name is a
// technical identifier and stays verbatim.
func (a *App) displayLocalFileSlot(slot string) string {
	return fmt.Sprintf(a.interopMsg().LocalSlotLineFmt, slot)
}

// displayLossLine renders one conversion loss/warning. The severity label and
// message are localized, while the stable machine code remains visible.
func (a *App) displayLossLine(l core.LossItem) string {
	return fmt.Sprintf("[%s / %s] %s", a.displaySeverity(l.Severity), l.Code, a.displayLossMessage(l))
}

// displayLossMessage localizes a loss message from its stable Code. Unknown
// codes keep the original core detail behind a Chinese classification.
func (a *App) displayLossMessage(l core.LossItem) string {
	if !a.interopIsZH() {
		return l.Message
	}
	for _, tr := range interopLossZH[l.Code] {
		if tr.when != "" && !strings.Contains(l.Message, tr.when) {
			continue
		}
		if tr.re == nil {
			return tr.text
		}
		m := tr.re.FindStringSubmatch(l.Message)
		if m == nil {
			continue
		}
		args := make([]any, 0, len(m)-1)
		for _, g := range m[1:] {
			args = append(args, g)
		}
		return fmt.Sprintf(tr.text, args...)
	}
	return fmt.Sprintf(a.interopMsg().LossUnknownFmt, l.Message)
}

type lossTranslation struct {
	when string
	re   *regexp.Regexp
	text string
}

var interopLossZH = map[string][]lossTranslation{
	// cURL import
	"curl-env-secret-slot-note": {
		{re: regexp.MustCompile(`environment "([^"]*)": (.*)$`), text: "无法将 Secret 槽位名称写入环境 %q：%s"},
	},
	"curl-host-file-to-local-slot": {
		{re: regexp.MustCompile(`host file reference "([^"]*)" converted to logical slot (\S+)`), text: "主机文件引用 %q 已转换为逻辑槽位 %s（发送前请绑定本地文件）"},
	},
	"curl-extra-url-ignored": {
		{re: regexp.MustCompile(`additional positional URL "([^"]*)"`), text: "额外的位置参数 URL %q 已忽略；单请求导入只使用第一个 URL"},
	},
	"curl-header-file-unsupported": {
		{re: regexp.MustCompile(`from file \((.*?)\) is blocked`), text: "从文件读取请求头（%s）因主机安全限制被阻止"},
	},
	"curl-header-malformed": {
		{re: regexp.MustCompile(`malformed header "([^"]*)"`), text: "已忽略格式错误的请求头 %q"},
	},
	"curl-cookie-scrubbed": {
		{text: "Cookie 值已脱敏为 Secret 引用；原始 Cookie 不会写入共享项目文件"},
	},
	"curl-compressed-note": {
		{text: "--compressed 已确认；GoRequest 传输层会自动协商压缩"},
	},
	"curl-flag-unsupported": {
		{re: regexp.MustCompile(`(?:curl option|unsupported curl option) (\S+)`), text: "curl 选项 %s 未导入（传输/TLS/输出等选项属于运行时设置）"},
	},
	"curl-export-apikey-query": {
		{re: regexp.MustCompile(`API key "([^"]*)" in query is redacted as (\S+)`), text: "查询参数中的 API Key %q 已脱敏为 %s"},
	},
	"curl-export-local-slot": {
		{when: "multipart file field", re: regexp.MustCompile(`multipart file field "([^"]*)" references local slot (local:[^;]+);`), text: "multipart 文件字段 %q 引用本地槽位 %s；主机路径未导出"},
		{re: regexp.MustCompile(`binary body references local slot (local:[^;]+);`), text: "二进制请求体引用本地槽位 %s；主机路径未导出"},
	},
	"curl-shell-construct-blocked": {
		{when: "unquoted shell operator", re: regexp.MustCompile(`operator "([^"]*)"`), text: "未加引号的 shell 操作符 %q 已被阻止；导入仅接受单条 curl 命令，不允许管道、重定向或链式命令"},
		{when: "double quotes", text: "双引号内的 shell 命令替换（$(...) 或反引号）已被阻止；GoRequest 绝不执行 shell 子命令"},
		{text: "shell 命令替换（$(...) 或反引号）已被阻止；GoRequest 绝不调用 shell"},
	},
	"curl-url-default-scheme": {
		{re: regexp.MustCompile(`URL "([^"]*)" had no scheme`), text: "URL %q 缺少协议，已默认使用 http://"},
	},

	// Postman import/export
	"postman-v1-unsupported": {
		{text: "不支持 Postman Collection v1.0 旧格式；请导出为 Postman Collection v2.1.0 或 v2.0.0"},
	},
	"postman-schema-unsupported": {
		{re: regexp.MustCompile(`schema "([^"]*)"`), text: "不支持的 Postman Collection schema %q（支持版本：v2.1.0、v2.0.0）"},
	},
	"postman-file-to-local-slot": {
		{re: regexp.MustCompile(`file reference "([^"]*)" converted to logical slot (\S+)`), text: "Postman 文件引用 %q 已转换为逻辑槽位 %s（未导入主机路径）"},
	},
	"postman-protocol-profile-ignored": {
		{text: "protocolProfileBehavior 未映射到 GoRequest YAML"},
	},
	"postman-saved-response-ignored": {
		{re: regexp.MustCompile(`^(\d+) saved response`), text: "%s 个已保存的响应示例已省略；GoRequest 只存储请求定义"},
	},
	"postman-env-disabled-var-omitted": {
		{re: regexp.MustCompile(`variable "([^"]*)" omitted`), text: "已省略禁用的环境变量 %q"},
	},
	"postman-secret-value-scrubbed": {
		{re: regexp.MustCompile(`secret variable "([^"]*)" value excluded from shared YAML; declared slot "([^"]*)"`), text: "Secret 变量 %q 的值未写入共享 YAML；已在 secrets 列表中声明槽位 %q"},
	},
	"postman-export-secret-scrubbed": {
		{when: "sensitive header", re: regexp.MustCompile(`sensitive header "([^"]*)" scrubbed to (\S+)`), text: "敏感请求头 %q 已脱敏为 %s"},
		{re: regexp.MustCompile(`credential in (\S+) scrubbed to (\S+)`), text: "字段 %s 中的明文凭据已脱敏为 %s"},
	},
	"postman-export-local-slot": {
		{re: regexp.MustCompile(`file "([^"]*)" exported as logical slot (\S+)`), text: "multipart 文件 %q 已导出为逻辑槽位 %s，不含主机路径"},
	},
	"postman-env-export-secret-scrubbed": {
		{re: regexp.MustCompile(`variable "([^"]*)" matched sensitive`), text: "变量 %q 命中敏感键规则；已作为 Secret 以空值导出"},
	},
	"postman-env-export-secret-slot": {
		{re: regexp.MustCompile(`secret slot "([^"]*)" exported`), text: "Secret 槽位 %q 已以空值导出（type=secret）"},
	},
	"postman-script-ignored": {
		{re: regexp.MustCompile(`Postman (\w+) script \((\d+) line`), text: "Postman %s 脚本（%s 行）未导入，也不会执行"},
	},
	"postman-auth-unsupported": {
		{re: regexp.MustCompile(`auth type "([^"]*)"`), text: "不支持 Postman 鉴权类型 %q（B08 基础互操作暂不支持）"},
	},
	"postman-path-variable-note": {
		{re: regexp.MustCompile(`contains (\d+) Postman path variable`), text: "请求 URL 包含 %s 个 Postman 路径变量定义"},
	},
	"postman-cookie-scrubbed": {
		{text: "Cookie 请求头值已脱敏为 Secret 引用"},
	},
	"postman-graphql-converted-to-json": {
		{text: "GraphQL 请求体已转换为 JSON 请求体载荷"},
	},
	"postman-body-mode-unsupported": {
		{re: regexp.MustCompile(`body mode "([^"]*)"`), text: "不支持的 Postman 请求体模式 %q 已省略"},
	},

	// Portable bundle
	"portable-export-local-slot-preserved": {
		{re: regexp.MustCompile(`logical file slot (local:\S+) without`), text: "已保留逻辑文件槽位 %s，未导出主机路径"},
	},
	"portable-export-missing-doc": {
		{re: regexp.MustCompile(`documentation "([^"]*)" could not be read: (.*)$`), text: "引用的文档 %q 无法读取：%s"},
	},
	"portable-export-missing-auth-ref": {
		{re: regexp.MustCompile(`auth scheme "([^"]*)" not found`), text: "项目中找不到引用的鉴权方案 %q"},
	},
	"portable-export-attachment-skipped": {
		{re: regexp.MustCompile(`attachment "([^"]*)" could not be packaged: (.*)$`), text: "公共附件 %q 无法打包：%s"},
	},
	"portable-local-slot-binding-required": {
		{re: regexp.MustCompile(`logical file slot (local:\S+); bind`), text: "便携包引用了逻辑文件槽位 %s；发送前请在本机绑定本地文件"},
	},
	"portable-secret-variable-scrubbed": {
		{re: regexp.MustCompile(`request variable "([^"]*)" replaced with \{\{secret\.([^}]*)\}\}`), text: "请求变量 %q 中的明文凭据已替换为 {{secret.%s}}"},
	},
	"portable-secret-header-scrubbed": {
		{re: regexp.MustCompile(`header "([^"]*)" replaced with \{\{secret\.([^}]*)\}\}`), text: "请求头 %q 中的明文凭据已替换为 {{secret.%s}}"},
	},
	"portable-host-path-scrubbed": {
		{when: "multipart field", re: regexp.MustCompile(`multipart field "([^"]*)" replaced with logical slot (local:\S+)`), text: "multipart 字段 %q 的机器相关路径已替换为逻辑槽位 %s"},
		{re: regexp.MustCompile(`body\.file replaced with logical slot (local:\S+)`), text: "body.file 中的机器相关路径已替换为逻辑槽位 %s"},
	},
	"portable-auth-secret-scrubbed": {
		{when: "bearer", re: regexp.MustCompile(`\{\{secret\.([^}]*)\}\}`), text: "明文 Bearer Token 已替换为 {{secret.%s}}"},
		{when: "basic", re: regexp.MustCompile(`\{\{secret\.([^}]*)\}\}`), text: "明文 Basic 密码已替换为 {{secret.%s}}"},
		{when: "API key", re: regexp.MustCompile(`\{\{secret\.([^}]*)\}\}`), text: "明文 API Key 值已替换为 {{secret.%s}}"},
	},
	"portable-project-secret-scrubbed": {
		{re: regexp.MustCompile(`project variable "([^"]*)" replaced with \{\{secret\.([^}]*)\}\}`), text: "敏感项目变量 %q 已替换为 {{secret.%s}}"},
	},
	"portable-env-secret-scrubbed": {
		{re: regexp.MustCompile(`environment variable "([^"]*)" moved to secrets list`), text: "敏感环境变量 %q 已移入 secrets 列表，值不写入"},
	},

	// OpenAPI routing/sync diagnostics shown by the shared import modal.
	"openapi-routed-to-a03": {
		{re: regexp.MustCompile(`^(.+?) specification detected \((.*?)\)\.`), text: "检测到 %s 规范（%s）。OpenAPI 3.x / Swagger 2.0 契约的完整导入、导出与同步属于 A03 任务；B08 基础互操作不提供 OpenAPI 转换。"},
	},
	"swagger-2.0-migrated-to-openapi-3.1": {
		{text: "Swagger 2.0 规范已迁移为 GoRequest HTTP 请求、OpenAPI 3.1 操作契约与 JSON Schema Draft 2020-12"},
	},
	"openapi-3.0-normalized-to-3.1": {
		{re: regexp.MustCompile(`OpenAPI (\S+) schemas normalized`), text: "OpenAPI %s 的 Schema 已规范化为 OpenAPI 3.1 / JSON Schema Draft 2020-12（nullable 转换为类型联合）"},
	},
	"openapi-sync-conflict": {
		{re: regexp.MustCompile(`^(.+)$`), text: "OpenAPI 三方同步冲突：%s"},
	},
}

// displayImportError localizes an error stored by the import flow. Known desktop
// messages are translated in full; known core error prefixes get a Chinese
// classification while the technical detail after the prefix is preserved.
func (a *App) displayImportError(errText string) string {
	if errText == "" {
		return ""
	}
	if !a.interopIsZH() {
		if m := importBufferBusyZHRE.FindStringSubmatch(errText); m != nil {
			return fmt.Sprintf(interopEN.ImportBufferBusy, m[1])
		}
		return errText
	}
	im := a.interopMsg()
	en := &interopEN
	// Exact desktop messages store the English literal; map them to Chinese.
	switch errText {
	case en.ErrorCURLRequired:
		return im.ErrorCURLRequired
	case en.ErrorImportFileRequired:
		return im.ErrorImportFileRequired
	case en.ErrorPreviewRequired:
		return im.ErrorPreviewRequired
	case en.ErrorNoReceipt:
		return im.ErrorNoReceipt
	case en.ErrorImportPlanApplied:
		return im.ErrorImportPlanApplied
	case en.ErrorOpenAPIRouted:
		return im.ErrorOpenAPIRouted
	}
	if strings.HasPrefix(errText, "请先保存或放弃编辑") {
		return errText // already localized by ImportBufferBusy
	}
	if m := importBufferBusyENRE.FindStringSubmatch(errText); m != nil {
		return fmt.Sprintf(im.ImportBufferBusy, m[1])
	}
	if strings.HasPrefix(errText, en.ErrorReadImportFilePrefix) {
		return im.ErrorReadImportFilePrefix + strings.TrimPrefix(errText, en.ErrorReadImportFilePrefix)
	}
	if strings.HasPrefix(errText, en.ErrorParsePostmanCollection) {
		return im.ErrorParsePostmanCollection + strings.TrimPrefix(errText, en.ErrorParsePostmanCollection)
	}
	if strings.HasPrefix(errText, en.ErrorParsePostmanEnvironment) {
		return im.ErrorParsePostmanEnvironment + strings.TrimPrefix(errText, en.ErrorParsePostmanEnvironment)
	}
	if strings.HasPrefix(errText, en.ErrorImportPlanBlockedPrefix) {
		detail := strings.TrimPrefix(errText, en.ErrorImportPlanBlockedPrefix)
		return im.ErrorImportPlanBlockedPrefix + localizeBlockedDetail(detail)
	}
	if m := unrecognizedImportRE.FindStringSubmatch(errText); m != nil {
		return fmt.Sprintf(im.ErrorUnrecognizedImportFormat, m[1])
	}
	return fmt.Sprintf(im.ErrorUnknownImportFmt, errText)
}

const (
	replannedNoticeZH = "目标在预览后发生变化，已重新生成预览；请检查后再次确认。"
	replannedNoticeEN = "The target changed after the preview; a fresh preview was generated. Review it and confirm again."
)

// displayImportNotice localizes an import status/replan/rollback notice for the
// current UI language so hot-switching while the modal is open updates notices
// on the next frame.
func (a *App) displayImportNotice(notice string) string {
	if notice == "" {
		return ""
	}
	if a.interopIsZH() {
		switch notice {
		case replannedNoticeEN:
			return replannedNoticeZH
		case interopEN.ImportRolledBack:
			return interopZH.ImportRolledBack
		}
		if m := importAppliedENRE.FindStringSubmatch(notice); m != nil {
			return fmt.Sprintf("已导入 %s 项文件变更", m[1])
		}
		return notice
	}
	switch notice {
	case replannedNoticeZH:
		return replannedNoticeEN
	case interopZH.ImportRolledBack:
		return interopEN.ImportRolledBack
	}
	if m := importAppliedZHRE.FindStringSubmatch(notice); m != nil {
		return fmt.Sprintf("Imported %s file change(s)", m[1])
	}
	return notice
}

// displayExportNotice localizes an export notice for the current UI language.
func (a *App) displayExportNotice(notice string) string {
	if notice == "" {
		return ""
	}
	zhCopied := messagesFor(langSimplifiedChinese).Copied
	enCopied := messagesFor(langEnglish).Copied
	if a.interopIsZH() {
		if notice == enCopied {
			return zhCopied
		}
		if m := exportSavedENRE.FindStringSubmatch(notice); m != nil {
			return fmt.Sprintf(interopZH.ExportSavedStatus, m[1])
		}
		return notice
	}
	if notice == zhCopied {
		return enCopied
	}
	if m := exportSavedZHRE.FindStringSubmatch(notice); m != nil {
		return fmt.Sprintf(interopEN.ExportSavedStatus, m[1])
	}
	return notice
}

// localizeBlockedDetail localizes the summary/conflict tail of an
// "import plan cannot be applied" error without dropping unknown details.
func localizeBlockedDetail(detail string) string {
	if s := translateSummaryZH(detail); s != "" {
		return s
	}
	parts := strings.Split(detail, "; ")
	for i, p := range parts {
		parts[i] = localizeConflictText(p)
	}
	return strings.Join(parts, "；")
}

func localizeConflictText(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return s
	}
	if m := conflictEntityExistsRE.FindStringSubmatch(trimmed); m != nil {
		return fmt.Sprintf("%s %q 已存在于 %s", entityKindZH(m[1]), m[2], m[3])
	}
	if m := conflictAllocateIDRE.FindStringSubmatch(trimmed); m != nil {
		return fmt.Sprintf("无法为 %s %q 分配唯一 ID", entityKindZH(m[1]), m[2])
	}
	return "冲突：" + trimmed
}

// entityKindZH is the App-free variant used by error-tail localization.
func entityKindZH(k string) string {
	switch k {
	case "request":
		return interopZH.EntityRequest
	case "folder":
		return interopZH.EntityFolder
	case "environment":
		return interopZH.EntityEnvironment
	case "auth":
		return interopZH.EntityAuth
	case "doc":
		return interopZH.EntityDoc
	case "asset":
		return interopZH.EntityAsset
	case "attachment":
		return interopZH.EntityAttachment
	default:
		return k
	}
}

var unrecognizedImportRE = regexp.MustCompile(`^unsupported or unrecognized import format \(file: "([^"]*)"\)$`)

// displayExportError localizes an error stored by the export flow.
func (a *App) displayExportError(errText string) string {
	if errText == "" {
		return ""
	}
	if !a.interopIsZH() {
		if m := importBufferBusyZHRE.FindStringSubmatch(errText); m != nil {
			return fmt.Sprintf(interopEN.ImportBufferBusy, m[1])
		}
		return errText
	}
	im := a.interopMsg()
	en := &interopEN
	switch errText {
	case en.ErrorSelectRequestForCURL:
		return im.ErrorSelectRequestForCURL
	case en.ErrorSelectRequestForBundle:
		return im.ErrorSelectRequestForBundle
	case en.ErrorExportPathRequired:
		return im.ErrorExportPathRequired
	case en.ErrorExportPathRequiredCore:
		return im.ErrorExportPathRequiredCore
	}
	if strings.HasPrefix(errText, "请先保存或放弃编辑") {
		return errText
	}
	if m := importBufferBusyENRE.FindStringSubmatch(errText); m != nil {
		return fmt.Sprintf(im.ImportBufferBusy, m[1])
	}
	if m := exportTargetExistsRE.FindStringSubmatch(errText); m != nil {
		return fmt.Sprintf(im.ErrorExportTargetExistsFmt, m[1])
	}
	if m := unsupportedExportScopeRE.FindStringSubmatch(errText); m != nil {
		return fmt.Sprintf(im.ErrorUnsupportedExportScopeFmt, m[1])
	}
	if m := targetFileExistsRE.FindStringSubmatch(errText); m != nil {
		return fmt.Sprintf(im.ErrorTargetExistsFmt, m[1])
	}
	return fmt.Sprintf(im.ErrorUnknownExportFmt, errText)
}

var (
	exportTargetExistsRE     = regexp.MustCompile(`^export target "([^"]*)" already exists$`)
	unsupportedExportScopeRE = regexp.MustCompile(`^unsupported portable export scope "([^"]*)"$`)
	targetFileExistsRE       = regexp.MustCompile(`^Target file "([^"]*)" already exists \(enable 'Allow Overwrite' to replace it\)\.$`)
	importBufferBusyENRE     = regexp.MustCompile(`^Save or discard edits and stop any active request before changing (.+)\.$`)
	importBufferBusyZHRE     = regexp.MustCompile(`^请先保存或放弃编辑，并停止正在执行的请求，再变更 (.+)。$`)
	importAppliedENRE        = regexp.MustCompile(`^Imported (\d+) file change\(s\)$`)
	importAppliedZHRE        = regexp.MustCompile(`^已导入 (\d+) 项文件变更$`)
	exportSavedENRE          = regexp.MustCompile(`^Exported to (.+)$`)
	exportSavedZHRE          = regexp.MustCompile(`^已导出至 (.+)$`)
)

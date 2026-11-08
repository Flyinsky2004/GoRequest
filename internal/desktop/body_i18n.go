package desktop

// bodyMessages holds localized UI copy for B03 HTTP Body, Raw/Batch Parameter
// Views, Custom HTTP Methods, Local File Mappings, and URL/Variable Previews.
// Keeping these in a B03-owned file avoids merge conflicts on i18n.go while
// still switching dynamically with the shared language setting.
type bodyMessages struct {
	ModeNone       string
	ModeJSON       string
	ModeXML        string
	ModeText       string
	ModeURLEncoded string
	ModeMultipart  string
	ModeBinary     string

	SourceLabel       string
	SourceInline      string
	SourceProjectFile string
	SourceLocalFile   string

	ProjectFileLabel       string
	ProjectFilePlaceholder string
	LocalSlotLabel         string
	LocalSlotPlaceholder   string
	LocalPathLabel         string
	LocalPathPlaceholder   string
	ChooseFile             string
	UnbindFile             string
	LocalIsolationHint     string
	FileReady              string // %s, %s
	FileUnbound            string // %s
	NoBodyHint             string

	RawEncodedMode              string
	BatchTextMode               string
	PreEncodedFlag              string
	PartKindLabel               string
	PartKindText                string
	PartKindProjectFile         string
	PartKindLocalFile           string
	AddTextPart                 string
	AddFilePart                 string
	FileNameOverridePlaceholder string
	ContentTypePlaceholder      string
	MultipartBoundaryHint       string
	WirePreviewLabel            string
	ResolvedURLPreviewLabel     string
	ResolvedURLWithParamsLabel  string

	UnsupportedBodyTitle   string // %s
	UnsupportedBodyHint    string
	ReplaceUnsupportedBody string
	UnknownFieldsWarning   string // %s

	CustomMethodMenu        string
	CustomMethodTitle       string
	CustomMethodHint        string
	CustomMethodPlaceholder string
	ApplyMethod             string
}

var bodyMessagesZh = bodyMessages{
	ModeNone:       "无",
	ModeJSON:       "JSON",
	ModeXML:        "XML",
	ModeText:       "纯文本",
	ModeURLEncoded: "URL 编码表单",
	ModeMultipart:  "Multipart",
	ModeBinary:     "二进制",

	SourceLabel:       "来源",
	SourceInline:      "内联",
	SourceProjectFile: "项目文件",
	SourceLocalFile:   "本机映射",

	ProjectFileLabel:       "项目相对文件",
	ProjectFilePlaceholder: ".gorequest/ 下相对路径，例如 assets/sample.bin",
	LocalSlotLabel:         "本机映射键",
	LocalSlotPlaceholder:   "例如 avatar-file（YAML 仅保存 local:avatar-file）",
	LocalPathLabel:         "本机路径",
	LocalPathPlaceholder:   "本机绝对文件路径（仅存本机，不写入项目）",
	ChooseFile:             "选择文件…",
	UnbindFile:             "解除绑定",
	LocalIsolationHint:     "本机文件路径仅保存在当前设备配置目录，绝不写入共享 .gorequest 项目文件。",
	FileReady:              "文件就绪：%s（%s）",
	FileUnbound:            "本机映射 %q 尚未在当前设备绑定文件",
	NoBodyHint:             "此请求不包含请求正文（无 Body）。",

	RawEncodedMode:              "原始编码",
	BatchTextMode:               "批量文本",
	PreEncodedFlag:              "已编码",
	PartKindLabel:               "类型",
	PartKindText:                "文本",
	PartKindProjectFile:         "项目文件",
	PartKindLocalFile:           "本机文件",
	AddTextPart:                 "+ 文本项",
	AddFilePart:                 "+ 文件项",
	FileNameOverridePlaceholder: "文件名（可选覆盖）",
	ContentTypePlaceholder:      "Content-Type（可选）",
	MultipartBoundaryHint:       "multipart boundary 由引擎自动生成并管理。",
	WirePreviewLabel:            "编码预览",
	ResolvedURLPreviewLabel:     "解析预览",
	ResolvedURLWithParamsLabel:  "含参数",

	UnsupportedBodyTitle:   "已保留未支持的正文模式：%s",
	UnsupportedBodyHint:    "保存此请求时会原样保留该正文及未知字段，不会静默吞掉。仅在明确需要改写时点击下方替换按钮。",
	ReplaceUnsupportedBody: "替换为受支持正文",
	UnknownFieldsWarning:   "检测到扩展/未知字段（保存时保留原值，执行前校验拒绝）：%s",

	CustomMethodMenu:        "自定义方法…",
	CustomMethodTitle:       "自定义 HTTP 方法",
	CustomMethodHint:        "输入合法的 HTTP 方法 Token（如 PURGE、PROPFIND、QUERY、REPORT）。",
	CustomMethodPlaceholder: "例如 PROPFIND",
	ApplyMethod:             "应用",
}

var bodyMessagesEn = bodyMessages{
	ModeNone:       "None",
	ModeJSON:       "JSON",
	ModeXML:        "XML",
	ModeText:       "Text",
	ModeURLEncoded: "Form URL-Encoded",
	ModeMultipart:  "Multipart",
	ModeBinary:     "Binary",

	SourceLabel:       "Source",
	SourceInline:      "Inline",
	SourceProjectFile: "Project File",
	SourceLocalFile:   "Local Mapping",

	ProjectFileLabel:       "Project File",
	ProjectFilePlaceholder: "Relative path under .gorequest/, e.g. assets/sample.bin",
	LocalSlotLabel:         "Mapping Key",
	LocalSlotPlaceholder:   "e.g. avatar-file (saved in YAML as local:avatar-file)",
	LocalPathLabel:         "Local Path",
	LocalPathPlaceholder:   "Absolute path on this machine (device-local only)",
	ChooseFile:             "Choose File…",
	UnbindFile:             "Unbind",
	LocalIsolationHint:     "Local machine file paths are stored only in device-local config and never written into .gorequest/.",
	FileReady:              "Ready: %s (%s)",
	FileUnbound:            "Local mapping %q is not bound on this machine",
	NoBodyHint:             "This request has no body.",

	RawEncodedMode:              "Raw Encoded",
	BatchTextMode:               "Batch Lines",
	PreEncodedFlag:              "Pre-encoded",
	PartKindLabel:               "Kind",
	PartKindText:                "Text",
	PartKindProjectFile:         "Project File",
	PartKindLocalFile:           "Local File",
	AddTextPart:                 "+ Text Part",
	AddFilePart:                 "+ File Part",
	FileNameOverridePlaceholder: "Filename override (optional)",
	ContentTypePlaceholder:      "Content-Type (optional)",
	MultipartBoundaryHint:       "Multipart boundary is automatically generated and managed by the engine.",
	WirePreviewLabel:            "Wire Preview",
	ResolvedURLPreviewLabel:     "Resolved URL",
	ResolvedURLWithParamsLabel:  "With params",

	UnsupportedBodyTitle:   "Preserved unsupported body mode: %s",
	UnsupportedBodyHint:    "Saving this request keeps this body and any unknown fields intact. Click Replace below only if you want to overwrite it.",
	ReplaceUnsupportedBody: "Replace Body",
	UnknownFieldsWarning:   "Unknown/extension fields preserved on save (refused on send): %s",

	CustomMethodMenu:        "Custom Method…",
	CustomMethodTitle:       "Custom HTTP Method",
	CustomMethodHint:        "Enter a valid RFC 9110 HTTP method token (e.g. PURGE, PROPFIND, QUERY, REPORT).",
	CustomMethodPlaceholder: "e.g. PROPFIND",
	ApplyMethod:             "Apply",
}

func bodyMessagesFor(l language) *bodyMessages {
	if parseLanguage(string(l)) == langEnglish {
		return &bodyMessagesEn
	}
	return &bodyMessagesZh
}

func (a *App) bodyMsg() *bodyMessages {
	if a == nil || a.mgr == nil || a.mgr.cfg == nil {
		return bodyMessagesFor(defaultLanguage())
	}
	return bodyMessagesFor(a.mgr.cfg.language())
}

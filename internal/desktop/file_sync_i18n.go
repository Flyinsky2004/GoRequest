package desktop

// fileSyncMessages holds all user-facing strings for the B01 file-sync banner
// and three-version conflict resolution modal in both Simplified Chinese and
// English. Keeping it in a dedicated B01 file avoids merge conflicts on shared
// i18n.go while honoring the active configStore language per frame.
type fileSyncMessages struct {
	// Banner messages and quick actions.
	BannerModified        string // %s (relPath)
	BannerDeleted         string // %s (relPath)
	BannerRenamedModified string // %s (oldPath), %s (newPath)
	BannerGitConflict     string
	ResolveConflictBtn    string
	ReloadDiskBtn         string
	AcceptDeleteBtn       string
	AcceptDeleteReviewBtn string // opens the comparison dialog first
	SaveAsCopyBtn         string

	// Three-version conflict modal.
	ModalTitleModified  string
	ModalTitleDeleted   string
	ModalTitleRenamed   string
	PathSummary         string // %s
	RenamedPathSummary  string // %s, %s
	DiskParseWarning    string // %s
	ModeThreeVersions   string
	ModeBaseToDisk      string
	ModeDiskToEdited    string
	BaselinePaneTitle   string
	DiskPaneTitle       string
	EditedPaneTitle     string
	DeletedOnDiskEmpty  string
	BaselineUnavailable string

	// Modal actions & status feedback.
	KeepEditingLater   string
	AutoMergeThreeWay  string
	SaveResolution     string
	RestoreDeleted     string
	DeletedHint        string // calm explanation for a normal on-disk deletion
	MergeCleanNote     string
	MergeOverlapNote   string
	StatusRefreshed    string // %d
	StatusRenamed      string // %s, %s
	StatusDeleted      string // %s
	StatusConflict     string // %s
	StatusResolved     string // %s
	StatusSavedCopy    string // %s
	ErrGitConflictSend string // %s
}

var fileSyncMessagesZh = fileSyncMessages{
	BannerModified:        "外部修改冲突：%s 已在磁盘更改，已同时保留读取基线、磁盘版本和当前编辑。",
	BannerDeleted:         "磁盘上的 %s 已被删除，但此标签页仍有未保存编辑。为避免丢失内容，已暂时保留，不会自动恢复文件。",
	BannerRenamedModified: "外部更名并修改：%s → %s，已保留读取基线、磁盘版本和当前编辑。",
	BannerGitConflict:     "检测到未解决的 Git 冲突标记，解决前禁止发送或覆盖。",
	ResolveConflictBtn:    "查看三版本…",
	ReloadDiskBtn:         "重新加载磁盘版",
	AcceptDeleteBtn:       "接受删除",
	AcceptDeleteReviewBtn: "接受删除…",
	SaveAsCopyBtn:         "另存为副本",

	ModalTitleModified:  "处理外部文件修改冲突",
	ModalTitleDeleted:   "处理磁盘文件删除冲突",
	ModalTitleRenamed:   "处理外部更名与修改冲突",
	PathSummary:         "文件：%s",
	RenamedPathSummary:  "文件已更名：%s → %s",
	DiskParseWarning:    "磁盘版本存在格式或冲突问题：%s",
	ModeThreeVersions:   "三版本对照",
	ModeBaseToDisk:      "基线 ↔ 磁盘差异",
	ModeDiskToEdited:    "磁盘 ↔ 编辑合并",
	BaselinePaneTitle:   "读取基线（只读）",
	DiskPaneTitle:       "磁盘版本（只读）",
	EditedPaneTitle:     "编辑 / 合并结果（可编辑）",
	DeletedOnDiskEmpty:  "该文件已在磁盘上被外部删除。\n这通常是正常的删除请求，并非 Git 冲突。\n可选择“接受删除”（放弃未保存编辑并关闭）、“另存为副本”，或显式“恢复文件”。",
	BaselineUnavailable: "（新建文件尚无历史读取基线）",

	KeepEditingLater:   "稍后处理",
	AutoMergeThreeWay:  "三方自动合并",
	SaveResolution:     "应用并保存",
	RestoreDeleted:     "恢复文件",
	DeletedHint:        "文件已在磁盘上删除；三个版本仍可对照，未保存编辑不会被静默丢弃。",
	MergeCleanNote:     "已完成无冲突三方合并，请核对后点击“应用并保存”。",
	MergeOverlapNote:   "存在重叠改动，已在合并区生成冲突标记，请手工编辑清理后再保存。",
	StatusRefreshed:    "已同步外部更改（%d 项）",
	StatusRenamed:      "已跟随外部更名：%s → %s",
	StatusDeleted:      "外部已删除：%s",
	StatusConflict:     "冲突：%s 在磁盘上已更改，已保护未保存内容",
	StatusResolved:     "已解决冲突并保存 %s",
	StatusSavedCopy:    "已另存副本 %s（原文件保持磁盘版本）",
	ErrGitConflictSend: "禁止发送：%s 含有未解决的 Git 冲突标记",
}

var fileSyncMessagesEn = fileSyncMessages{
	BannerModified:        "External edit conflict: %s changed on disk. Baseline, Disk, and Edited versions are preserved.",
	BannerDeleted:         "%s was deleted on disk, but this tab still has unsaved edits. They are kept for now and the file is not restored automatically.",
	BannerRenamedModified: "External rename & edit: %s → %s. Baseline, Disk, and Edited versions are preserved.",
	BannerGitConflict:     "Unresolved Git conflict markers detected; sending and blind overwrite are blocked.",
	ResolveConflictBtn:    "Compare 3 Versions…",
	ReloadDiskBtn:         "Reload Disk",
	AcceptDeleteBtn:       "Accept Delete",
	AcceptDeleteReviewBtn: "Accept Delete…",
	SaveAsCopyBtn:         "Save As Copy",

	ModalTitleModified:  "Resolve External File Modification",
	ModalTitleDeleted:   "Resolve External File Deletion",
	ModalTitleRenamed:   "Resolve External Rename & Modification",
	PathSummary:         "File: %s",
	RenamedPathSummary:  "Renamed on disk: %s → %s",
	DiskParseWarning:    "Disk version has format or conflict issues: %s",
	ModeThreeVersions:   "Three Versions",
	ModeBaseToDisk:      "Baseline ↔ Disk",
	ModeDiskToEdited:    "Disk ↔ Edited Merge",
	BaselinePaneTitle:   "Reading Baseline (Read-Only)",
	DiskPaneTitle:       "Disk Version (Read-Only)",
	EditedPaneTitle:     "Edited / Merge Result (Editable)",
	DeletedOnDiskEmpty:  "This file was deleted on disk externally.\nThis is usually a normal delete, not a Git conflict.\nChoose Accept Delete (discard unsaved edits and close), Save As Copy, or explicitly Restore File.",
	BaselineUnavailable: "(No prior baseline — newly created buffer)",

	KeepEditingLater:   "Keep Editing",
	AutoMergeThreeWay:  "3-Way Auto-Merge",
	SaveResolution:     "Save Resolution",
	RestoreDeleted:     "Restore File",
	DeletedHint:        "The file was deleted on disk; all three versions can still be compared and unsaved edits are never discarded silently.",
	MergeCleanNote:     "Clean 3-way merge generated. Review and click Save Resolution.",
	MergeOverlapNote:   "Overlapping edits marked in the merge buffer; resolve markers before saving.",
	StatusRefreshed:    "synced external changes (%d item(s))",
	StatusRenamed:      "followed external rename: %s → %s",
	StatusDeleted:      "removed externally: %s",
	StatusConflict:     "conflict: %s changed on disk; unsaved edits protected",
	StatusResolved:     "resolved conflict and saved %s",
	StatusSavedCopy:    "saved copy %s (original kept at disk version)",
	ErrGitConflictSend: "blocked: %s contains unresolved Git conflict markers",
}

func fileSyncMessagesFor(l language) *fileSyncMessages {
	if parseLanguage(string(l)) == langEnglish {
		return &fileSyncMessagesEn
	}
	return &fileSyncMessagesZh
}

func (a *App) fileSyncMsg() *fileSyncMessages {
	if a == nil || a.mgr == nil || a.mgr.cfg == nil {
		return fileSyncMessagesFor(defaultLanguage())
	}
	return fileSyncMessagesFor(a.mgr.cfg.language())
}

package desktop

// welcomeProjectMessages holds the labels of the U11 two-mode project
// creation sheet. They live in their own file so the shared i18n.go catalog is
// not touched by this task.
type welcomeProjectMessages struct {
	ModeSection string

	ModeIndependent     string
	ModeIndependentDesc string
	ModeExisting        string
	ModeExistingDesc    string
	ModeSelected        string

	ExistingLocationLabel     string
	ChooseExistingLocation    string
	ChooseExistingLocationMsg string
	ExistingLocationHint      string
	ExistingNameHint          string // %s

	FinalPathLabel                  string
	FinalPathIndependentPlaceholder string
	FinalPathExistingPlaceholder    string

	ErrChooseExistingDir string
	ProjectExistsHint    string
	OpenExisting         string
	KeyboardHint         string
}

var welcomeProjectMessagesZh = welcomeProjectMessages{
	ModeSection: "项目类型",

	ModeIndependent:     "独立项目",
	ModeIndependentDesc: "在所选父目录下新建一个以项目名命名的文件夹，适合全新的接口项目。",
	ModeExisting:        "与代码仓库并存",
	ModeExistingDesc:    "所选目录就是现有代码的根目录，GoRequest 数据直接写入该目录的 .gorequest，不再嵌套项目名。",
	ModeSelected:        "已选择",

	ExistingLocationLabel:     "代码目录",
	ChooseExistingLocation:    "选择现有代码目录",
	ChooseExistingLocationMsg: "选择现有代码的根目录；GoRequest 只会在其中创建 .gorequest。",
	ExistingLocationHint:      "不会执行 git init，也不会修改已有代码或 .git。",
	ExistingNameHint:          "项目名称将使用目录名：%s",

	FinalPathLabel:                  "最终位置",
	FinalPathIndependentPlaceholder: "选择父目录并输入项目名后显示：<父目录>/<项目名>/.gorequest",
	FinalPathExistingPlaceholder:    "选择代码目录后显示：<代码目录>/.gorequest",

	ErrChooseExistingDir: "请选择现有代码目录",
	ProjectExistsHint:    "该目录已有 .gorequest，不会覆盖。可以直接打开现有项目。",
	OpenExisting:         "直接打开",
	KeyboardHint:         "用 Tab 聚焦，Enter 或空格选择；方向键切换项目类型。",
}

var welcomeProjectMessagesEn = welcomeProjectMessages{
	ModeSection: "Project type",

	ModeIndependent:     "Independent project",
	ModeIndependentDesc: "Create a new folder named after the project inside the chosen parent. Best for a brand-new API project.",
	ModeExisting:        "Alongside existing code",
	ModeExistingDesc:    "The chosen folder is the existing code root. GoRequest writes its data straight into that folder's .gorequest, with no extra project folder.",
	ModeSelected:        "Selected",

	ExistingLocationLabel:     "Code folder",
	ChooseExistingLocation:    "Choose Existing Code Folder",
	ChooseExistingLocationMsg: "Choose the root of the existing code; GoRequest only creates .gorequest inside it.",
	ExistingLocationHint:      "No git init runs, and existing code and .git are left untouched.",
	ExistingNameHint:          "The project name will be the folder name: %s",

	FinalPathLabel:                  "Final location",
	FinalPathIndependentPlaceholder: "Choose a parent folder and enter a name: <parent>/<name>/.gorequest",
	FinalPathExistingPlaceholder:    "Choose a code folder: <code folder>/.gorequest",

	ErrChooseExistingDir: "Choose an existing code folder",
	ProjectExistsHint:    "This folder already has .gorequest and will not be overwritten. You can open the existing project instead.",
	OpenExisting:         "Open It",
	KeyboardHint:         "Tab to focus, Enter or Space to choose; arrow keys switch the project type.",
}

func welcomeProjectMessagesFor(l language) *welcomeProjectMessages {
	if parseLanguage(string(l)) == langEnglish {
		return &welcomeProjectMessagesEn
	}
	return &welcomeProjectMessagesZh
}

func (w *welcomeState) projectMsg() *welcomeProjectMessages {
	if w == nil || w.mgr == nil || w.mgr.cfg == nil {
		return welcomeProjectMessagesFor(defaultLanguage())
	}
	return welcomeProjectMessagesFor(w.mgr.cfg.language())
}

# B07 Git 证据：Q-05 调用方式、hook 与凭据策略

日期：2026-11-06  
范围：`internal/gitservice`、`internal/desktop/git*.go`；对应决策 [Q-05](../decisions.md)、基础规格 §6、AC-05。  
状态：本卡实现采用的策略与自动化证据。它冻结的是**本卡已实现的安全边界**，不是 Git 产品能力的完整冻结；分支切换、历史、冲突解决、签名提交和凭据管理 UI 仍未实现。

## 1. 决策摘要

| 问题 | 本卡选择 | 理由 |
| --- | --- | --- |
| 调用系统 CLI 还是 Go 库 | 系统 Git CLI，显式 argv，从不经 shell | 状态/Diff/提交/远程语义以 Git 自身为准；避免第三方库重现边缘行为；shell 字符串拼接正是注入来源 |
| 仓库 hook | 一律不执行 | 打开仓库不产生副作用；显式提交/Pull/Push 也不运行仓库脚本 |
| 凭据 | 不写入项目；只在网络操作时复用**全局** Git credential helper；不交互 | 仓库不能指定本进程要执行的 helper；无终端时不阻塞 |
| 提交 | 隔离索引 + plumbing | 不夹带既有暂存内容；不执行 clean filter/hook |
| Pull | 仅快进；脏工作树/分叉直接拒绝 | 不自动 merge/rebase/stash/解决冲突 |
| Push | 显式 refspec；绝不 force | 不猜测远程、不强推 |
| 无 Git/非仓库 | 类型化错误，面板提示 | HTTP 与文档编辑完全不受影响；打开项目不访问 Git |

## 2. 调用与进程边界

- 可执行文件：启动面板时才 `exec.LookPath("git")`；失败返回 `ErrGitUnavailable`。打开项目不探测 Git。
- 参数：全部通过 `exec.CommandContext(ctx, git, args...)` 传入，永不拼接 shell。全局启用 `--literal-pathspecs`，文件名中的 `*`、`[`、`;`、`$()` 等不会被解释为模式或 shell 语法。
- 路径校验：提交/差异路径必须是仓库内相对路径；拒绝绝对路径、`..` 越界、NUL 和 `.git/` 内部路径。
- 输出上限：状态等 stdout 默认 32 MiB；超限返回 `ErrOutputTooLarge`，绝不解析截断结果。诊断 stderr 截断并脱敏 URL 中的 userinfo。
- 取消：每个操作接受 `context.Context`；Unix 下使 git 进入独立进程组并按组 `SIGKILL`，连同 ssh/remote-helper 子进程一起结束；Windows 下尽力结束直接进程（孙进程可能存活，见限制）。
- 环境：清除继承的 `GIT_DIR`、`GIT_WORK_TREE`、`GIT_INDEX_FILE`、`GIT_CONFIG_COUNT/PARAMETERS`、`GIT_SSH*`、askpass 等注入；固定 `GIT_TERMINAL_PROMPT=0`、`GIT_PAGER=cat`、`GIT_SSH_COMMAND=ssh -o BatchMode=yes`、`GIT_ALLOW_PROTOCOL=https:ssh:file`。
- 版本：使用 `--no-optional-locks`、porcelain v2 与 `-z`，需要 Git ≥ 2.15；本机验证版本 2.54.0。更旧版本会以明确命令错误失败，不会降级到不受控脚本。

## 3. Hook 策略

每条命令都带固定覆盖：

```text
--no-optional-locks --literal-pathspecs
-c core.hooksPath=<本进程创建的临时空目录>
-c core.fsmonitor=false
-c core.sshCommand=ssh
-c commit.gpgSign=false
-c protocol.ext.allow=never
-c credential.helper=
-c core.worktree=<发现的工作树根>
```

命令行的 `-c` 优先级高于仓库本地配置，因此仓库无法重新启用 `core.hooksPath`、fsmonitor 命令或签名程序。覆盖范围包括 `pre-commit`、`commit-msg`、`post-commit`、`reference-transaction`、`pre-push`、`post-merge` 等所有 hook。

提交本身使用 plumbing（`read-tree`、`hash-object --no-filters`、`update-index`、`write-tree`、`commit-tree`、`update-ref`），不会经过 `git commit` 的 hook 链；Pull 使用 `fetch` + `merge --ff-only`，Push 使用 `push`，均在空 hooksPath 下运行。测试安装在 `.git/hooks` 和仓库自定义 `core.hooksPath` 下的脚本均未被触发。

## 4. 不信任仓库配置提供的命令

除 hook 外，以下配置由命令行覆盖或环境限制：

- `core.fsmonitor`：可配置外部命令 → 固定 `false`。
- `core.sshCommand`：仓库可指定命令 → 固定 `ssh`；环境再以 `GIT_SSH_COMMAND` 加 `BatchMode=yes`。
- `commit.gpgSign` / `push.gpgSign`：可能启动签名程序 → 固定 `false`；本卡不做签名提交。
- `protocol.ext.allow`：`ext::` 传输可执行任意命令 → 固定 `never`；`GIT_ALLOW_PROTOCOL` 仅允许 https/ssh/file。
- `remote.<name>.uploadpack/receivepack`：本地传输可指定外部命令 → 对当前远程显式固定为 `git-upload-pack` / `git-receive-pack`。
- `remote.<name>.proxy`、`submodule.recurse`：固定为空/false，避免远程 helper 和递归子模块。
- `merge.autoStash`：固定 `false`，禁止隐式 stash/pop。
- `core.worktree`：固定为发现的工作树根，避免配置把文件操作重定向到别处。
- `credential.helper`：先以空值重置列表，再只追加从**用户全局配置**读取的 helper；仓库本地 helper 不执行（测试用仓库级 helper 写入标记文件验证）。
- Diff 不调用 `git diff`/textconv/external diff；旧新内容由 `cat-file` 与直接读文件得到。
- `hash-object --no-filters` 不执行 `.gitattributes` clean filter；提交内容与工作区字节一致。

## 5. 凭据

- 不在项目、仓库配置或 GoRequest 配置中保存 Git 凭据。
- SSH 复用系统 ssh-agent 与 `~/.ssh/config`；`BatchMode=yes` 使缺少可用密钥时立即失败而不是等待输入。
- HTTPS 复用用户**全局** Git credential helper（如 osxkeychain/store），禁止仓库本地 helper；`GIT_TERMINAL_PROMPT=0` 保证无交互。
- 面板确认框显示远程地址时剥离 userinfo（`***@`），命令错误诊断也会脱敏 `scheme://userinfo@`。
- 本卡不提供 Git 凭据录入 UI，也不接入 B05 的项目凭据后端；HTTPS 无可用 helper 时给出清晰失败，而不是静默重试或把凭据写入项目。

## 6. 选择性提交语义

1. 由 HEAD 树填充临时索引（`GIT_INDEX_FILE` 指向临时目录），**不使用**仓库真实索引作为提交基础。
2. 对每个选中路径：
   - 真实索引与 HEAD 不同 → 使用索引快照（等同 `git commit -- <path>` 的暂存语义）；
   - 索引与 HEAD 相同 → 读取工作区内容，`hash-object -w --no-filters` 写入 blob；
   - 索引缺失而 HEAD 有 → 视为已暂存删除；工作区删除 → 记录删除；未跟踪 → 新增。
3. `write-tree` 后若树与 HEAD 相同 → `ErrNoChanges`。
4. `commit-tree` 写入提交，`update-ref` 以旧 HEAD 做 CAS 更新当前分支；分离 HEAD 拒绝提交，未出生分支创建根提交。
5. 成功后只把**从工作区提交**的路径同步回真实索引；其他已暂存条目保持原样。未选中的暂存内容不会进入提交，也不会被 reset/unstage。
6. Git 将已暂存的重命名报告为“新路径 + 原路径”的一行；选中该行会同时提交新路径与原路径的删除，避免旧文件残留在 HEAD。工作区重命名通常显示为独立的删除与未跟踪项，需要用户分别选择。
7. 提交过程中的文件在上传前后比较大小与 mtime，变化则报错并要求刷新，避免提交半读内容。
8. 子模块、未解决冲突（index stage > 0）和超出 64 MiB 的单文件拒绝，并给出明确诊断。

## 7. Pull / Push

- 必须存在当前分支的 upstream（`branch.<name>.remote` 与 `.merge`）；缺失时不猜测远程。
- Pull：
  1. 若存在未保存编辑、B01 同步冲突或任何已跟踪/已暂存改动 → 拒绝；服务层再次检查工作树。
  2. `fetch --no-tags --no-prune`。
  3. 依据 `@{upstream}` 计算 ahead/behind；已最新则成功无操作；本地领先且有落后 → `ErrDiverged`，不自动合并。
  4. 仅落后时 `merge --ff-only`；不产生合并提交，不启动编辑器，不自动 stash。
  5. 成功后由桌面端调用 B01 `SyncNow` 刷新标签/文档；前提是拉取前无脏缓冲或冲突。
- Push：显式 `HEAD:<upstream ref>` refspec、`--no-follow-tags`，永不附加 `--force`；远端拒绝时保留错误，不重试强推。
- 两类操作都要求当前不是分离 HEAD，并在确认框中显示解析后的远程与目标 ref。

## 8. 验证证据

`go test ./internal/gitservice/`（全部使用 `t.TempDir()` 临时仓库与临时本地 bare origin）：

- 非仓库/普通目录发现失败；子目录发现到祖先仓库根。
- 状态新增/修改/删除/未跟踪标记；未解决冲突识别。
- 选择性提交：预置暂存内容不进入提交、真实索引中未选暂存条目保留、所选路径提交后索引一致、未选文件仍为变更。
- hook：`.git/hooks` 与仓库 `core.hooksPath` 下的 pre-commit/post-commit/reference-transaction/pre-push/post-merge 均不运行。
- 仓库级 credential helper 不运行，全局 helper 仍可参与。
- 字面文件名（含 `;`、`$()`、`[ ]`）不触发 shell，按字节提交。
- 分离 HEAD 拒绝；未出生分支根提交；删除提交；无变化拒绝；未合并路径拒绝。
- Diff：工作区/索引/未跟踪/二进制/超限。
- Pull：本地 bare origin 快进成功；脏工作树拒绝且不改索引；分叉拒绝且 HEAD 不变。
- Push：本地 bare origin 更新成功；pre-push hook 不运行。
- 取消：慢速假 git 进程在超时后被终止，返回 `ErrCanceled`。
- 输出上限：超限状态返回 `ErrOutputTooLarge`。

`go test ./internal/desktop/ -run TestGit`：面板不可用/非仓库渲染、状态与选择、控制器选择性提交、脏缓冲阻止 Pull、Diff 状态构建、三个模态渲染、取消状态。以上均为无窗口 headless 测试，真实窗口人工验收未做。

## 9. 明确限制

1. 未实现分支切换、提交历史浏览、冲突三版本解决、rebase、reset、stash、签名提交、子模块提交。
2. `hash-object --no-filters` 不执行 clean/LFS filter；使用 LFS 等过滤器的仓库会按工作区字节提交，需在后续决策中处理属性与过滤策略。
3. Windows 终止进程组只能尽力结束直接进程；远程 helper 孙进程可能存活。
4. HTTPS 凭据仅复用全局 helper；未接 B05 凭据 UI，也没有 Git 凭据缓存/刷新。
5. 真实 macOS 窗口、Windows/Linux 实机、真实远程（GitHub 等）未验证；本卡所有 push/pull 只针对临时本地 bare 仓库。
6. 未实现“提交前 diff 的 hunk 级选择”；选择性粒度是文件。
7. 全局 credential helper 列表在首次网络操作时读取并缓存；运行中修改全局 Git 配置需重开窗口生效。
8. 工作区重命名不会被自动配对；需要用户同时选中删除项与新增项（已暂存重命名除外）。
9. 允许 `file` 传输以支持本地 bare origin；本地 fetch/push 仍会运行标准 `git-upload-pack`/`git-receive-pack`，其服务端仓库配置属于用户选定的远程端点。远程 URL 在确认框中可见，`ext::` 等其他协议被拒绝。

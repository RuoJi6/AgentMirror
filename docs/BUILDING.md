# 构建与下载可执行文件

AgentMirror 使用 Go 后端、纯 Go SQLite 驱动和 React/Vite 前端。发布程序内嵌前端与默认页面资源，管理台、静态网页克隆和蜜罐服务可直接运行。

## GitHub 自动构建

仓库的 **Actions → Build executables** 支持以下触发方式：

- 推送到 `main`、提交 Pull Request：测试通过后编译五种目标，并保留 14 天的 `agentmirror-binaries` 构建产物。
- 点击 **Run workflow**：手动启动同一构建流程。
- 推送 `v*` 标签：编译及三平台启动检查通过后，自动创建对应 GitHub Release，上传五个可执行文件和 `SHA256SUMS`。重新运行同一标签时更新该 Release 的同名附件。

| 系统 | 架构 | 文件 |
| --- | --- | --- |
| Linux | x64 / amd64 | `agentmirror-linux-amd64` |
| Linux | ARM64 | `agentmirror-linux-arm64` |
| Windows | x64 / amd64 | `agentmirror-windows-amd64.exe` |
| macOS | Intel / amd64 | `agentmirror-darwin-amd64` |
| macOS | Apple Silicon / ARM64 | `agentmirror-darwin-arm64` |

工作流先运行 Go 测试与前端逻辑测试，再使用 `CGO_ENABLED=0` 交叉编译。Linux x64、Windows x64、macOS ARM64 分别在对应系统的 GitHub runner 上验证启动、管理员初始化、SQLite 读写、内嵌页面及静态资源。Linux ARM64 和 macOS Intel 验证交叉编译，当前没有单独的原生启动检查。

构建产物只包含上述文件，不携带运行数据库、模型配置、账户信息或评测日志。Go 程序会包含源码中的默认资源和内置 Agent 指令；`prompts/` 下的参考文档与导出示例属于源码材料，不会自动导入运行数据库。

## 下载后启动

在 Release 中下载对应系统的文件；普通分支构建可从工作流运行页下载 `agentmirror-binaries` 并解压。

Linux x64 示例：

```sh
chmod +x agentmirror-linux-amd64
./agentmirror-linux-amd64 --db ./data/agentmirror.sqlite3
```

macOS Apple Silicon 示例：

```sh
chmod +x agentmirror-darwin-arm64
./agentmirror-darwin-arm64 --db ./data/agentmirror.sqlite3
```

Windows PowerShell 示例：

```powershell
.\agentmirror-windows-amd64.exe --db .\data\agentmirror.sqlite3
```

打开 `http://127.0.0.1:8766`，按页面提示创建管理员账号。没有预设管理员密码。默认管理端只监听本机地址，新数据库不会自动发布蜜罐站点。

Linux/macOS 从 Actions 下载文件后需要重新设置执行权限。当前工作流未配置 Windows 代码签名或 Apple 签名、公证，操作系统可能要求确认来源；可先核对 Release 中的 `SHA256SUMS`。

## 本地构建

需要 Go 1.25 或更新版本，以及 Node.js 22.12+（工作流使用 Node 22）。

```sh
npm ci
npm run build
```

本机产物为 `bin/agentmirror`，Windows 为 `bin/agentmirror.exe`。

在 Linux、macOS 或可使用 Bash 的环境中构建所有发布目标：

```sh
bash scripts/build-release.sh
```

只构建指定目标：

```sh
bash scripts/build-release.sh linux/amd64 windows/amd64 darwin/arm64
```

文件输出到 `release/`，`SHA256SUMS` 记录本次选择的目标。推送发布标签前，请让 `package.json` 与 `internal/lab` 中的程序版本保持一致。

## 可选浏览器功能

可执行文件不包含 Chromium 或 Node 模块。动态页面克隆及浏览器链路核查还需要 Node、Playwright、Chromium，以及 `scripts/browser-capture.mjs`、`scripts/workspace-review.mjs`。从源码目录部署时可执行：

```sh
npm ci
npx playwright install chromium
```

Linux 缺少浏览器系统依赖时使用 `npx playwright install --with-deps chromium`。详细路径配置见[部署架构](DEPLOYMENT_ARCHITECTURE.md)。

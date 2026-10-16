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

构建产物只包含上述文件，不携带运行数据库、模型配置、账户信息或评测日志。Go 程序会包含源码中的默认资源和内置 Agent 指令；`examples/evaluated-scenarios/` 中的 12 个已测场景及提示词从 v0.0.2 起随程序内嵌，首次启动和升级时一次性导入为工作区草稿；不包含下载附件，不自动开启监听。`prompts/` 下其他参考文档不会自动导入。

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

打开 `http://127.0.0.1:8766`，按页面提示创建管理员账号。没有预设管理员密码。默认管理端监听所有 IPv4 网卡，也可通过服务器 IP 远程访问；新数据库不会自动发布蜜罐站点。启动日志仅列出实际运行的蜜罐公告地址；没有启用的蜜罐端口时显示“公告地址 暂无”，管理端仍可正常访问。

Linux/macOS 从 Actions 下载文件后需要重新设置执行权限。当前工作流未配置 Windows 代码签名或 Apple 签名、公证，操作系统可能要求确认来源；可先核对 Release 中的 `SHA256SUMS`。

### 远程管理与指定地址

自 v0.0.4 起，管理端默认监听 `0.0.0.0:8766`，可从其他机器访问 `http://<服务器IP>:8766`，本机仍可使用 `http://127.0.0.1:8766`。新安装通过该地址设置管理员，已有安装继续使用原账号。服务器需允许管理端口入站。

```sh
# 默认允许通过服务器 IPv4 地址远程访问
./agentmirror --admin-port 8766

# 仅绑定指定网卡的本机 IPv4 地址
./agentmirror --admin-host 192.168.1.10 --admin-port 8766

# 仅允许本机连接（也适用于 SSH 转发）
./agentmirror --admin-host 127.0.0.1

# 通过域名或 NAT 映射后的地址访问，保留实际外部端口
./agentmirror --admin-origin http://admin.example.com:8766 \
  --admin-origin http://203.0.113.10:18766

# HTTPS 反向代理：代理保留外部 Host，转发到本机 8766
./agentmirror --admin-host 127.0.0.1 \
  --admin-origin https://admin.example.com
```

命令中的 `agentmirror` 替换为实际下载的文件名或 `./bin/agentmirror`。`--admin-host` 指服务器本机的监听地址，不是客户端 IP；`0.0.0.0` 代表所有 IPv4 网卡。`--admin-origin` 可重复，只接受完整的 `http://主机[:端口]` 或 `https://主机[:端口]`，不接受路径、通配符、账号或查询参数。直接访问服务器 IP 无需设置此参数；域名、NAT 外部地址与实际监听地址不同时需要显式指定，避免被 Host 校验拒绝。

HTTPS 代理需保留原始 `Host`（包含非默认外部端口），并在此配置对应的 HTTPS origin；登录 Cookie 会设置 `Secure`。程序不信任客户端提交的 `Forwarded` / `X-Forwarded-*`。管理员登录、会话令牌和同源写入校验始终启用。管理监听和允许地址只从启动参数读取，修改后重启；已有 Docker/systemd 部署若仍显式使用 `--admin-host 127.0.0.1`，需相应调整启动配置。

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

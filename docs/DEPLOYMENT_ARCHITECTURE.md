# Go 后端与部署架构

当前使用 **Go 后端 + React/Vite 静态前端 + SQLite**，管理端以蜜罐工作区为唯一创作与发布入口。程序通过 `go:embed` 包含后台构建产物及测试页面资源，常规运行和静态克隆不依赖 Python、Node 或外部前端目录。动态网页克隆可额外启用 Node + Playwright + Chromium worker。SQLite 驱动为 `modernc.org/sqlite`，发布构建使用 `CGO_ENABLED=0`。

## 模块

| 文件/目录 | 职责 |
| --- | --- |
| `cmd/agentmirror/main.go` | 参数、启动、版本、退出信号 |
| `assets.go` | 生产构建嵌入 `dist/` 管理界面 |
| `assets_dev.go` | `dev` 构建不依赖 `dist/`，管理页面由 Vite 提供 |
| `scripts/dev.mjs`、`vite.config.js` | 一键启动开发前后端、API 代理、Go 源码监听和退出清理 |
| `internal/lab/store.go` | SQLite 初始化、提示词与引用校验、端口配置验证 |
| `internal/lab/composer.go` | 站点/场景/工作区管理、实例编译、发布版本、公开请求和交付证据 |
| `internal/lab/site_package*.go` | 多文件站点、ZIP 导入、静态资源、隔离预览及站点预设 |
| `internal/lab/scenario_*.go` | 场景校验、请求条件、文本/HTML/JSON 响应和提示词槽位 |
| `internal/lab/generation.go` | 模型设置、Chat Completions 适配、异步任务生命周期及取消 |
| `internal/lab/generation_agent.go` | 有界工具循环、续聊草稿、计划与活动、结果校验、幂等采用 |
| `internal/lab/generation_modules.go` | 按需读取的模块用途、工具用法、格式约束和示例 |
| `internal/lab/site_clone.go` | 公开网址静态抓取、动态空壳回退、资源本地化、主动行为清理及来源说明 |
| `internal/lab/browser_capture.go` | worker 生命周期、JSONL 协议、浏览器请求的公网校验与预算 |
| `scripts/browser-capture.mjs` | Playwright/Chromium 匿名渲染，通过 JSONL 请求 Go 下载网页资源并返回 DOM |
| `internal/lab/auth.go` | 单管理员初始化、Argon2id 密码校验、会话与退出、修改密码、登录限流 |
| `internal/lab/sessions.go` | 不可变快照、事件、通用回传、摘要分页、按需读取、结论、导出和清除 |
| `internal/lab/listeners.go` | 管理后台启动监听；测试端口配置、绑定、冲突回滚、恢复、关闭 |
| `internal/lab/http.go` | 管理/测试路由、请求边界、Host/Origin/CSRF 检查 |
| `internal/lab/public.go`、`assets/` | 旧单页模板迁移和历史公开链路兼容所需的渲染资源 |
| `frontend/` | 工作区、提示词、会话和系统设置管理界面；旧模板/部署编辑入口已移除 |
| `frontend/src/features/WorkspaceShell.jsx` | 工作区列表、创建/设置弹窗、紧凑操作栏 |
| `frontend/src/features/Composer.jsx` | 工作区草稿状态、素材编辑、绑定、预演与发布流程 |
| `frontend/src/features/ComposerAgent.jsx` | 工作区内对话、任务轮询、结果预览与采用 |
| `frontend/src/features/MentionInput.jsx` | 类型→条目两级引用检索，引用身份与输入文本分离 |

配置和提示词是数据；编辑它们不需要编译，服务端不执行正文中的命令。结果占位符保留给客户端填写，接收端只校验会话协议，不校验业务字段类型。只有修改程序或静态页面资源时才需要重新构建。

## 开发与发布分离

参考 ARTEX 的前后端独立开发方式，保留现有 React/Vite + Go 模块边界。`npm run dev` 启动 Vite 热更新前端和自动重编译的 Go API，分别使用 `5173`、`8769`；开发数据库独立存放于 `data/dev/`。API 经 Vite 代理，仍保留后端认证和来源校验。发布时 `npm run build` 将前端嵌入 Go 程序，在 `8766` 提供管理界面与 API，不依赖 Vite。具体命令与环境变量见 [开发指南](DEVELOPMENT.md)。

## 发布与请求链路

工作区保存站点引用和场景实例，每个实例指定默认提示词、按规则 ID 保存的 `rule_profiles` 提示词覆盖及可选路径覆盖。编译规则时优先使用接口单独指派，再回退到实例默认方案；引用校验覆盖两者。发布事务读取当前站点、场景和提示词，把站点、场景、绑定及当时的提示词副本保存到发布版本；端口绑定工作区的发布结果，不再维护另一套业务覆盖。工作区暂停/恢复只切换当前发布入口的可用性；删除工作区会暂停、解绑关联端口，删除草稿和当前发布入口，保留不可变版本及历史会话。

公开请求先确定端口和实验会话，再将部署当前 release 的规则及站点版本载入请求副本，匹配方法、路径和条件。站点、场景和工作区保存时更新所有已发布引用工作区的 release；新旧会话都使用新内容，历史快照和交付记录保留。提示词按稳定的 binding_id + scenario_id + rule.id 解析指派，并按 profile ID 读取最新版，同一请求内缓存相同方案。命中后渲染响应并填入 `{{prompt}}`；普通 fallback 不包含提示词，没有场景响应时尝试静态资源。未保存编辑不会生效；更新不会开启暂停的部署或发布尚未发布的工作区。

HTTP 预演与公开请求共用匹配和渲染逻辑，预演不创建会话。正式请求的响应写入成功后记录请求、命中、响应与提示词交付事件；这些是服务器输出证据，不代表模型一定读取或执行。回传继续由 `/collect` 按实验编号、令牌和端口归属校验。

站点外观预览在 `sandbox=""` iframe 内显示，CSS、图片等本地资源内联，脚本不执行。正式站点在独立测试端口提供同源资源和模拟 API；依赖 JavaScript 的页面需在该入口验证交互。导入静态包不会执行包内构建命令。

## 工作区助手

助手在现有 Go + SQLite 中独立实现编写与内容核查两个 Agent 角色，参考 ARTEX 的 [会话恢复](https://github.com/Autumn-27/ARTEX/blob/0ec51e37993aa50047317b009a05593c61bbdf1e/agent/mainagent.go#L140)、[工具调用与结果捕获](https://github.com/Autumn-27/ARTEX/blob/0ec51e37993aa50047317b009a05593c61bbdf1e/agent/capture.go#L27) 和 [事件持久化](https://github.com/Autumn-27/ARTEX/blob/0ec51e37993aa50047317b009a05593c61bbdf1e/server/engine.go#L638) 思路。Web 界面移植其双栏聊天结构与紧凑操作方式，许可证和来源见 [第三方声明](THIRD_PARTY_NOTICES.md)；没有复用渗透业务或工具代码，也没有依赖 Norma SDK。4.7.0 增加角色注册表、工具绑定和工作区事件触发调度，不引入 Shell、MCP 或源码构建运行时。

创建工作区先保存具名草稿，可暂时没有站点；发布必须有有效站点。前端以工作区 ID 隔离编辑状态和助手实例，顶部操作栏与功能页签分开；切换功能页签保持助手挂载，切换工作区则卸载旧实例。未保存的离开操作会提示，过期异步响应不能写入后来打开的工作区。

对话归属由首轮 `workspace_id` 确定。列表先按工作区筛选，再限制为最近 50 个对话；`unassigned` 视图保留无归属及所属工作区已删除的历史，历史数据不会自动迁移。复用旧对话时将结果作为新基线，在当前工作区创建新的对话，服务端拒绝跨工作区续接原对话 ID。

`agent` 任务继承对话与草稿基线，由本轮需求决定直接回复、澄清或调用工具。系统指令描述职责与操作边界，不要求完成全部模块；`read_module` 按需提供模块指南。工具集包括计划、克隆、读取和编辑草稿文件、读取/使用明确引用的素材、设置站点/场景、显式替换草稿、校验和完成。普通文字、代码块或 JSON 示例都不会隐式替换草稿；整份草稿替换必须通过工具调用。

结果包含 `kind: message | draft`、`summary` 和 `changes`。`changes` 按本轮前后模块内容比较产生，只包含实际变化的 `site` 或 `scenario`；读工具和不改变内容的操作不产生可采用素材。结果中的站点和场景还可用作后续对话基线，不能仅凭它们存在判断本轮是否生成。没有变更的回复不进入草稿修复循环；有变更时只校验存在的模块，不要求单站点任务补场景或单场景任务补站点。旧结果没有类型字段时继续按原草稿读取。

工具参数或校验错误作为结果返回模型，允许在预算内修复实际请求的模块。单任务默认 24 轮、可按 Agent 配置 1–100 轮，工具调用额度为 max(96, 轮数 × 4)，单轮最多 12 次工具调用，总时限默认 600 秒并可按模型配置，全局最多并行运行 8 个任务，取消通过 context 传递到模型请求和网页抓取。新建和切换会话不会停止后台任务；轻量会话索引定时更新状态，选中会话才轮询完整工具进度。每轮保留独立的模型配置快照；同会话的运行中检查与新任务保存使用同一互斥锁，避免多个浏览器标签页提交重叠轮次。

`clone` 任务不调用模型，先抓取单个匿名公开网页和有界静态依赖。下载器逐跳校验目标地址及解析结果，阻止本机、私网和保留地址，固定已校验 IP 用于连接，保留原主机名作 Host/TLS 校验；不继承环境代理或管理员登录态。静态路径的页面正文上限 1 MiB、单资源 2 MiB、下载总量 6 MiB，另限制资源数量、递归深度和重定向。

检测到静态空壳或脚本加载占位时，Go 启动 `scripts/browser-capture.mjs`，通过标准输入/输出上的 JSONL 与独立 Node 进程通信。worker 创建隔离的匿名 Chromium 上下文，执行页面脚本以得到渲染后的 DOM；页面网络请求通过 JSONL 交给 Go 获取，再把响应交回浏览器。浏览器不直接访问外网，Go 只执行匿名 GET，每个资源和重定向继续使用相同的 DNS/IP 固定与公网限制。原站 Cookie、认证头以及个人浏览器配置不会带入抓取；需要登录、POST 数据接口或验证码的页面仍可能缺少内容。

浏览器渲染后复用静态克隆的结构提取、资源本地化、脚本与主动行为清理、站点校验。输出是单页面的静态快照，原站脚本不进入成品；助手可另写本地交互与模拟接口。回退依据空壳与加载占位检测，因此不会保证补齐每个已含部分静态内容页面中的动态区域，也不会遍历页面导航或重演点击操作。

| 阶段 | 限制 |
| --- | --- |
| 单次克隆 | 总计 90 秒，受外层任务取消与配置的任务总时限约束 |
| 浏览器渲染 | 最多 45 秒，返回 DOM 不超过 2 MiB |
| 浏览器临时网络响应 | 文档单响应 2 MiB，其他单响应 8 MiB，总量 32 MiB，最多 160 次请求 |
| 最终站点素材 | 单文件 4 MiB、总量 8 MiB、最多 256 个文件，维持原素材校验 |

较大的脚本可在临时渲染预算内加载，但不会因此放宽成品文件限制。缺少浏览器运行环境、沙箱无法启动、超时或仍无内容时，任务返回具体失败原因；资源缺失以警告呈现，不声称完整复制原应用。

`generation_jobs` 保存任务状态、计划、按序活动、近期对话和完成草稿，管理界面通过轮询恢复执行进度，通过 `conversation_id` 分组展示多轮消息；旧任务可通过 `parent_job_id` 链归组。对话列表和消息接口只查询展示字段，不返回站点文件或供应商内部消息块。活动记录是执行摘要，不保存模型内部推理；任务成功后才能通过 `parent_job_id` 继续，继承上一结果，显式提交的站点或场景优先。续聊保留近期用户请求及结果摘要，不重放完整工具历史。服务重启会终止遗留运行状态，不自动重试付费调用。多供应商密钥保存在独立 `generation_providers` 表，`generation_provider_state` 保存默认配置。旧的 `generation_secrets` 迁移为默认配置，并作为兼容接口映射保留；业务素材导出和任务文档不包含密钥。

模型传输层在工具循环外适配 OpenAI Compatible 与 Anthropic Messages。Anthropic 的 system、tool_use、tool_result 以及消息内容块使用原生格式；供应商要求的 opaque 签名只在当前循环内存中原样保留。每个任务记录供应商名称、协议和模型 ID，执行时持有启动时的私有配置快照；中途编辑或删除配置不影响该任务。适配参考 [ARTEX provider 结构](https://github.com/Autumn-27/ARTEX/blob/a91c0a72230851a5d787e5d699a3c77fb2ebb4e1/agent/provider.go)，没有引入其 SDK 或业务工具。

`references` 保存用户通过 `@` 选择的实体类型、ID 和版本。服务端从存储读取本轮快照，模型只能通过 `read_reference` / `use_reference` 访问选中的站点和场景，后者只替换任务内草稿。提示词引用仅保留元信息，正文不进入生成上下文；采用接口按任务中的用户选择核对方案版本并返回绑定 ID，模型不能选择其他方案。历史消息仅携带引用元数据，续聊省略引用时继承，显式空数组用于清除。

模型接收站点文件清单，按需读取文本；二进制资产仅提供元信息，实际提示词方案正文不发送。可克隆的入口来自本次管理员输入，网页内容或模型输出不能扩大该入口集合。纯回复不能采用。草稿采用在一个 SQLite 事务中仅新增 `changes` 声明的素材、各自版本和任务采用标记，客户端不能夹带其他模块；内容摘要保证重复采用不新增素材，已采用后改变内容返回 409。界面随后把素材接入当前未保存工作区，只替换本次采用的模块，保存和发布仍由现有工作区操作完成。

## 数据与兼容

数据库默认名称保持 `agentmirror-v2.sqlite3`。保留 `entities`、`versions`、`settings`、`sessions`、`events`、`reports` 等已有表，不重建或自动清空历史数据。工作区、站点、场景和不可变素材版本使用 `entities` 的不同类别；发布记录保存固定规则与站点版本引用。管理员凭据与生成模型密钥使用各自的独立表，业务导出不包含这些密钥。

新库不创建工作区、发布记录或公开测试入口。管理监听只由 `--admin-host` / `--admin-port` 创建，不写入测试端口列表。已有数据库中的旧单页模板自动转换为独立静态站点素材，供工作区复用；原模板记录保留，迁移记录防止删除后的素材再次生成。旧部署不会自动迁移为工作区，也不会把迁移素材自动发布。所有 `/api/templates`、`/api/deployments`、`/api/preview` 及其子路径在管理员鉴权后返回 410；当前接口见 [工作区指南](COMPOSER_GUIDE.md)。

`/api/state` 的 `deployments` 只返回组合发布结果，旧记录放入 `legacy_deployments`，不再返回 `templates/carriers/surfaces`。已有旧部署端口可以保留并启停或删除；新建或改绑端口仅接受组合发布结果。历史会话及其版本、事件、回传和快照继续读取，旧记录不强制改成新的工作区结构。兼容读取旧数据库不表示旧 Python 程序能理解新增工作区及其发布记录。旧 SQL/JSON 样本位于 `internal/lab/testdata/`，用于核对历史记录兼容性，样本均为合成数据。

`server.py`、`backend/` 和 Python 测试保留在私有历史中作兼容性对照，未包含在本次公开源码及 Go 发布产物中。本次删除范围是 Go 管理端的旧页面模板与旧部署创作流程。旧的 `data/agentmirror.sqlite3` 仍不自动导入；已经删除的测试端口不会重新创建。

## 监听器与并发

每个端口使用独立 `net.Listener` 和 `http.Server`。管理后台的监听配置在整个进程生命周期保持不变，只读显示在运行信息中；测试端口 CRUD 接口拒绝修改管理后台，并保留管理端口冲突检查。修改测试端口配置时先验证并预先绑定新地址，SQLite 提交成功后再替换旧监听；绑定失败保留旧配置与旧服务。暂停和删除立即关闭接受连接的 socket，同时让在途请求结束。管理端口变更需修改启动命令并重启，原有测试端口配置不随之改变。

恢复时先启动管理端；测试端口冲突在管理台显示为失败，不影响其他端口。收到 SIGINT/SIGTERM 时关闭全部监听，等待在途请求，再关闭数据库。

SQLite 使用 WAL、外键约束、忙等待和显式事务；当前单个数据库连接串行处理存储访问，避免多端口写入时产生部分快照。端口生命周期另有互斥锁；提示词、站点、场景和工作区更新使用版本号防止并发编辑覆盖。发布在同一事务中固定相关素材。这是单机实现，不是多节点控制平台。

## 原生发布

源码构建需要 Go 1.25+ 和 Node。先 `npm ci`，再 `npm run build`，输出 `bin/agentmirror`。常规运行及静态克隆只需对应 OS/CPU 的程序及可写数据目录。

`scripts/build-release.sh` 支持 Linux amd64/arm64、macOS amd64/arm64、Windows amd64，输出 `release/` 和 SHA256SUMS。Linux 持续运行可采用 `deploy/agentmirror.service`。数据路径建议用绝对路径，避免改变工作目录后误建另一份数据库。

### 动态页面运行环境

动态回退需要在运行主机额外保留 Node 22、仓库的 `scripts/browser-capture.mjs`、通过当前 `package-lock.json` 安装的 Playwright 及其 Chromium。源码目录执行 `npm ci` 和 `npx playwright install chromium` 即可准备 Node 模块与浏览器；Linux 可用 `npx playwright install --with-deps chromium` 一并安装系统依赖。浏览器应安装到运行账号可访问的位置；切换账号后需确保使用同一个浏览器目录或以运行账号安装。

Go 默认依次寻找当前工作目录、可执行文件目录或其父目录下的 `scripts/browser-capture.mjs`。服务通过其他工作目录启动时，建议显式指定绝对路径；Playwright 模块须能从 worker 所在目录解析到：

```sh
AGENTMIRROR_BROWSER_WORKER=/opt/agentmirror/scripts/browser-capture.mjs \
AGENTMIRROR_BROWSER_NODE=/usr/local/bin/node \
/opt/agentmirror/bin/agentmirror --db /var/lib/agentmirror/agentmirror-v2.sqlite3
```

`AGENTMIRROR_BROWSER_NODE` 未设置时从 `PATH` 查找 `node`。`PLAYWRIGHT_EXECUTABLE_PATH` 可指定已有 Chromium/Chrome；默认使用 Playwright 安装的浏览器，macOS 可回退到已安装的 Google Chrome。回退创建临时匿名上下文，不打开个人用户资料。仅复制 Go 发布文件即可使用静态克隆，但不会自动提供动态渲染环境。

Chromium 以沙箱模式启动。运行账号应为非 root，Linux 宿主机、容器的 seccomp/AppArmor 等策略需允许沙箱所需的 user namespaces；若浏览器报告沙箱启动失败，应检查运行环境。worker 不通过 `--no-sandbox` 降级运行。

## Docker

多阶段 Dockerfile 在构建阶段使用 Node 和 Go，最终 scratch 镜像只有程序和数据目录。默认非 root 用户，SQLite 使用持久化卷。

`Dockerfile.browser` 提供动态克隆版本：复用 Go 构建阶段，最终基于 `node:22-bookworm-slim`，通过 `npm ci` 安装锁定的 Playwright 1.62.1，并用 `npx playwright install --with-deps chromium` 安装浏览器及系统库。worker 和 Node 路径已在镜像中配置，浏览器放在 `/ms-playwright`，以 `node` 非 root 用户运行，保留 Chromium 沙箱。

```sh
docker build -f Dockerfile.browser -t agentmirror:browser .
docker run --rm --init --network host \
  --mount source=agentmirror-browser-data,target=/data \
  agentmirror:browser \
  --admin-host 127.0.0.1 --admin-port 8766 --db /data/agentmirror-v2.sqlite3
```

该示例使用 Linux host network，命名卷保存数据。浏览器镜像运行用户为 UID/GID 1000；绑定已有宿主目录时应准备对应目录权限，迁移纯 Go 镜像已有卷也需核对所有权。宿主内核和容器运行策略须允许 Chromium 沙箱的 user namespaces；镜像不自动修改宿主策略，也不关闭沙箱。以上 Docker 构建与运行命令需在目标部署环境验证。

现有 `compose.yaml` 默认构建纯 Go 镜像。使用浏览器镜像时，把该服务的 `build: .` 改为 `build: {context: ., dockerfile: Dockerfile.browser}`，并添加 `init: true`；原监听参数与持久卷配置继续适用，已有卷须匹配上述运行用户权限。

提供的 Compose 使用 **Linux host network**，应用新增端口可以直接监听宿主机，无需每次重建端口映射。管理端默认使用宿主机回环地址，远程管理可使用 SSH 转发；Compose `command` 显式设置管理监听参数。Docker Desktop 需要支持并启用 host network；本地开发可直接使用原生程序。

如改为 bridge，容器内需指定 `--admin-host 0.0.0.0`，同时明确发布管理及测试端口。新增测试端口仍需同步映射。Docker/systemd 文件属于部署模板，实际目标环境需单独验证。

## 升级与回退

先停止写入并备份 SQLite，再停止原进程；使用 Go 程序和同一个绝对 `--db` 路径启动。检查后台运行版本、站点迁移结果、工作区/端口状态和历史记录，然后刷新浏览器获取新管理会话。两套服务不要同时写入同一份数据库。

移除旧管理流程前的代码保存在 Git 标签 `before-legacy-removal-20260918`；标签不包含运行数据库备份。旧 Python 版要求数据库中存在管理监听记录，与当前启动配置模型不同。回退应先停止 Go 并使用升级前的备份及对应旧版启动命令。备份恢复会替换备份之后的数据，应由操作者决定。

参考：[Go 嵌入资源](https://pkg.go.dev/embed)、[ARTEX](https://github.com/Autumn-27/ARTEX)、[Studio Admin](https://github.com/arhamkhnz/next-shadcn-admin-dashboard)。前端界面与后端语言独立；本项目无需 Next.js 服务器运行时。

### 模型响应恢复

供应商适配器将 OpenAI 文本内容块正规化，区分空回复、仅思考数据、输出截断、拒绝与上下文耗尽，保留白名单结束原因和 token 计数。Agent 在工具循环内对可恢复响应做有限重试，沿用内存中的草稿及已执行工具结果，不从第一轮重新执行工具；全任务最多 2 次额外模型请求，仍使用原 180 秒取消上下文。截断的工具调用全部丢弃，该轮输出预算可由 12,000 (legacy) 一次提高到 24,000 (legacy)。Anthropic 空 end_turn 使用恢复提示继续原有工具结果，保留原有签名块。错误正文、思考内容和供应商签名不写入任务诊断。

协议参考：[DeepSeek Chat Completions](https://api-docs.deepseek.com/api/create-chat-completion/) 的 finish_reason；[Anthropic stop reasons](https://platform.claude.com/docs/en/build-with-claude/handling-stop-reasons) 的空回复和截断处理；ARTEX 本地参考版本的 agent/provider.go 提供独立空响应重试配置。本实现保留原有轻量工具循环，不引入其执行器。

## 4.6.18 保存同步与增量工具

`live_materials.go` 在站点、场景或工作区保存事务内编译所有受影响的已发布工作区，追加不可变 release 并移动部署引用；不修改监听器或暂停状态。旧会话请求只在内存中更新当前 release/rules/site，数据库中的创建快照保持不变，响应事件记录实际 release_id/site_version_id。启动时对已发布记录进行幂等同步，单工作区失败不阻止其他工作区启动。

`generation_editing.go` 提供文件哈希校验、追加/片段替换、单规则 CRUD 和完整工具往返压缩；`generation_materials.go` 提供明确的已保存素材读取/加载/保存/删除，沿用存储层版本与引用约束。默认引用仍为元信息，只有明确编辑提示词的读取工具才返回正文；不把正文作为助手指令。素材变更事件驱动前端刷新，未保存的工作区编辑保留。

## 多 Agent 核查架构（4.7.0）

`agent_registry.go` 将 `writer` / `reviewer` 的配置保存在 `agent_definitions` 实体中。工具目录、发送给模型的 Schema 和服务端实际调用权限共用注册表；任务保存配置快照。编写流程沿用 `generation_agent.go`，核查流程由 `workspace_review.go` 调度，两者共享取消、模型协议适配、超时、审计事件和任务并发限制。

`review_policies` 保存工作区触发设置，`review_cursors` 保存已处理内容指纹与编写完成时间。服务每 3 秒检查已启用策略，未变化时不调用模型；同工作区合并并发触发。删除工作区同时移除策略和游标，保留核查历史。失败触发保留原游标及可见错误，不丢失待核查变化。

`review_runtime.go` 在事务内捕获素材/绑定/提示词和回传响应配置，在临时 SQLite 中发布副本，通过真实 HTTP handler 执行检查。`review_browser.go` 与 `scripts/workspace-review.mjs` 使用 JSONL 交换请求/响应，让隔离 Chromium 真正执行登录和页面操作；所有网络请求仅映射到临时 handler，不监听公开端口。报告由实际断言和访问覆盖推导，模型总结不能改写服务端判定。取消/超时保留已有证据，部分完成单独标识。

### 4.7.1 共享会话、触发器与上下文预算

核查保存 `conversation_id` 与 Agent 快照，加入统一聊天索引。普通咨询只调用模型，核查工具首次执行时建立隔离环境；完整核查入口和事件触发先运行绑定的核查套件。AI 助手左侧按 Agent 折叠分组，移除独立核查页签，触发设置在弹窗中编辑。服务端拒绝跨 Agent 或跨工作区续写。`provider_selection` 保存跟随 Agent 或显式选择模型的意图，刷新后恢复。

`review_triggers.go` 支持保存、编写完成/失败、指定编写工具成功调用及定时事件，过滤核查自身事件，自动触发串行合并并复用会话。触发源仅为本应用工作区与任务事件。

`generation_provider_limits` 独立持久化上下文窗口和输出上限，原凭据及超时表不变。`generation_context.go` 为两个角色计算包含工具 Schema 的保守 token 估计（并非计费 tokenizer），在输入预算 80% 或 256 KiB 阈值整理旧记录为历史摘录、当前素材/核查状态；当前请求及最近整组协议往返完整保留。`context_compacted` 事件供页面显示，已存记录不改写，不额外调用模型摘要。不可缩减的当前输入超限时明确报错，仍有 768 KiB 消息体保护。

输出截断恢复改为在用户配置额度内发送分段/单步恢复说明，废除旧版先固定 12,000 再提高至 24,000 的策略；不执行截断响应内的任何工具调用。

### 4.7.2 模型流式响应

新增独立 `generation_provider_streaming(provider_id,enabled)` 表；缺失行默认启用，不重写供应商凭据和已有任务快照。OpenAI Chat Completions 与 Anthropic Messages 的 SSE 聚合为原完整消息格式，保留签名回放块，流式中途的展示状态不落库、不进入工具执行器或对话回放。每个请求仍有完整的时间/12 MiB 大小界限。运行中 job 的 `?progress=1` 返回轻量状态与有界预览，已完成请求返回完整结果。连接池限制空闲连接并继续禁用环境代理和重定向。

部署前等待运行中任务结束，备份 SQLite，再替换二进制并核验管理端资源及健康端点。不要自动刷新用户仍有未保存草稿的浏览器标签。

### 托管下载的持久化目录

4.13.0 起托管文件保存在 SQLite 路径后追加 `.files` 的目录，如 `data/lab.sqlite3.files/`。该目录需要与数据库一起持久化和备份；仅复制数据库不能恢复下载内容。原始文件按 SHA-256 寻址、只追加，元数据及站点引用在数据库中。移除当前素材中的引用不会删除历史内容。上传和附件下载使用最长 10 分钟的独立传输时限，其他接口保留原有超时设置。


### 子站端口映射（4.14.0）

监听配置可选 `site_node_id`；省略或 `root` 表示原主站行为。组合工作区监听绑定子站时，从不可变发布版本投影所选子树：所选节点成为 `/`，后代挂载及场景路径去掉祖先前缀，节点、绑定、提示词及素材版本标识保留。快照记录所选节点，后续请求实时刷新同一子树；不会修改已存历史快照。监听仍由现有端口管理器负责启停、持久化和冲突处理。

保存内容时同时校验所有关联监听（含暂停配置），禁止移除已绑定节点，或将该子站的回传地址、`/health` 与场景/静态资源产生冲突。旧客户端保存监听时省略 `site_node_id` 会保留原子站，显式设为 `root` 可切回主站；改绑其他工作区时默认主站。会话校验同时检查监听 ID、端口与站点节点，令牌不会随跨端口跳转共享。

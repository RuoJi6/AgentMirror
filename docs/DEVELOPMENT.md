# 开发模式

开发模式保留 AgentMirror 的 Go API + React/Vite 架构，采用 ARTEX 的前后端独立运行方式。`npm run dev` 统一管理两个服务，发布仍使用内嵌静态前端的 Go 单文件程序。

## 启动

一键开发脚本支持 macOS/Linux；Windows 请在 WSL 中运行，以便退出时完整清理子进程。需要 Go 1.23 或更新版本，以及符合 Vite 7 要求的 Node.js（20.19+ 或 22.12+）。首次拉取代码后运行：

```bash
npm ci
npm run dev
```

打开 <http://127.0.0.1:5173>，首次使用开发数据库时创建开发管理员。默认服务与数据如下：

| 项目          | 开发模式                       | 发布模式默认值                |
| ------------- | ------------------------------ | ----------------------------- |
| 管理界面      | `127.0.0.1:5173`，Vite HMR     | `127.0.0.1:8766`，Go 内嵌资源 |
| API           | `127.0.0.1:8769`               | `127.0.0.1:8766`              |
| SQLite        | `data/dev/agentmirror.sqlite3` | `data/agentmirror-v2.sqlite3` |
| Go 可执行文件 | 系统临时目录，退出后清理       | `bin/agentmirror`             |

Vite 将 `/api` 代理到开发后端，登录和写入操作也通过同一地址访问。代理适配后端的 Host/Origin 校验，外部来源的 Origin 仍交由后端拒绝。

开发后端使用 `go build -tags dev`，不依赖 `dist/`，因此干净检出也无需先构建前端。开发时直接打开 API 端口不会显示管理界面，请使用 Vite 地址。

## 编辑与停止

- 修改 `frontend/`：由 Vite 热更新。
- 修改根目录 Go 文件、`go.mod`、`go.sum`、`cmd/` 或 `internal/`：自动合并短时间内的保存事件，编译成功后重启后端。
- Go 编译失败：终端显示错误，保留上次成功运行的后端；修复保存后自动重试。
- 按 `Ctrl+C`：停止本次启动的前端、后端和编译进程，删除临时二进制，保留开发数据库。
- 端口已占用：启动直接报错，不会结束其他服务或自动切换端口。

Go 重启会中断进行中的生成任务。开发数据库中的工作区、素材和历史记录会保留。

## 自定义开发端口与数据库

macOS/Linux 示例：

```bash
AGENTMIRROR_DEV_API_PORT=8779 \
AGENTMIRROR_DEV_WEB_PORT=5183 \
AGENTMIRROR_DEV_DB=data/dev/experiment.sqlite3 \
npm run dev
```

相对数据库路径以仓库根目录为起点。使用独立开发数据库，避免将此变量指向正在运行的生产数据库。

只运行前端、连接已经自行启动的后端：

```bash
npm run dev:web

# 或连接其他地址；此命令不启动、重启或停止后端。
AGENTMIRROR_DEV_API_URL=http://127.0.0.1:8779 npm run dev:web
```

`dev:web` 默认也代理到 `127.0.0.1:8769`。一键开发模式根据 `AGENTMIRROR_DEV_API_PORT` 启动并连接自己的后端，不使用 `AGENTMIRROR_DEV_API_URL` 指向其他服务。

## 验证与发布

```bash
# 后端测试，可在没有 dist 的开发环境执行
go test -tags dev ./...

# 验证开发启动、代理写入、源码监听和退出清理（使用临时数据库和端口）
npm run test:dev

# 发布前端 + 内嵌前端的生产二进制
npm run build

# 验证单文件程序可脱离源码目录运行
npm run test:standalone
```

`npm run build` 和 `npm start` 的行为保持不变。开发标签只用于本地分离运行；发布构建不加 `-tags dev`。

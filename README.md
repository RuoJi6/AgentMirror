# AgentMirror

AgentMirror 是一款专门针对渗透测试智能体反制的蜜罐，以AI对抗AI方式。

核心功能点（agent只负责生成测试修改蜜罐及提示词，但是没有自动交互功能）：
- 对话生成蜜罐：通过 Agent 创建和调整蜜罐，支持快速克隆真实业务页面外观。
- 多站点联动：一个工作区构建多个关联站点，支持嵌套路径和独立端口，模拟复杂业务环境。
- 自定义请求与响应：根据路径、参数、请求头及正文匹配流量，返回不同响应，模拟业务及漏洞特征。
- 提示词精准投放：按站点、接口和触发条件绑定提示词，支持动态变量及版本管理。
- 自动核查与红队测试：检查蜜罐访问、交付和回传链路，评估被测 Agent 对提示词注入的行为。
- 探索路径可视化：展示访问、探测、规则命中、文件下载及回传过程，支持证据查看与导出。
- 素材复用与文件托管：自由组合站点、场景和提示词，支持文件托管及下载追踪。
- 灵活部署与扩展：支持多端口管理、配置热更新、多模型接入及 MCP 工具扩展。

## 启动

当前源码使用 Go + SQLite 后端和 React/Vite 前端。发布程序内嵌管理界面；可从 [Releases](https://github.com/RuoJi6/AgentMirror/releases) 下载对应系统的可执行文件，或在 [Actions](https://github.com/RuoJi6/AgentMirror/actions/workflows/build.yml) 下载自动构建产物。

从源码构建需要 Go 1.25+、Node.js 22.12+：

```sh
npm ci
npm run build
./bin/agentmirror
```

Windows 使用 `.\bin\agentmirror.exe`。打开 `http://127.0.0.1:8766` 并创建管理员账号；新安装不自动发布测试站点。常规运行无需 Go、Node 或 Python。动态页面克隆与浏览器核查是可选能力，需要额外安装 Node、Playwright、Chromium 和浏览器 worker。

- [构建、下载与自动发布](docs/BUILDING.md)：Linux x64/ARM64、Windows x64、macOS Intel/Apple Silicon。
- [蜜罐工作区指南](docs/COMPOSER_GUIDE.md) · [开发指南](docs/DEVELOPMENT.md) · [部署架构](docs/DEPLOYMENT_ARCHITECTURE.md)。
- [12 个实际评测场景及配套提示词](examples/evaluated-scenarios/README.md)：仅整理已使用的冻结场景，原部署地址已替换为示例地址。
- [提示词参考文件](prompts/) · [第三方声明](docs/THIRD_PARTY_NOTICES.md)。

## 结果占位符与回传

提示词可使用 `{{run_id}}`、`{{token}}`、`{{callback_url}}` 和 `{{result.名称}}`。服务端不执行提示词中的命令；被测 Agent 的实际行为需要独立日志验证。HTTP 201 仅代表收件，不代表命令真实执行。场景配置与回传规则详见[工作区指南](docs/COMPOSER_GUIDE.md)。

## 旧流程迁移与回退

升级前先停止服务并备份数据库，避免新旧程序同时写入同一份数据。公开版本以 Go 工作区流程为主；历史 Python 后端及私有仓库中的回退标签未包含在本次源码发布中。

## 截图预览


| 仪表盘（总览 / 活动流） | 最近会话 |
| :---: | :---: |
| <img width="1634" height="1026" alt="image" src="https://github.com/user-attachments/assets/d6a82916-073a-4188-a884-e0db3d8247a6" /> | <img width="1337" height="832" alt="image" src="https://github.com/user-attachments/assets/9f276329-486a-4f1c-9e58-50815909edd9" /> |

| 工作区 | 暴露端口 |
| :---: | :---: |
| <img width="1634" height="1026" alt="image" src="https://github.com/user-attachments/assets/c1a7c310-5797-4d79-b8f4-ec872291d87c" /> | <img width="1634" height="1026" alt="image" src="https://github.com/user-attachments/assets/0e0c85eb-249f-4e16-a232-d3f860945a24" /> |

| 提示词 | 提示词修改差异化 |
| :---: | :---: |
| <img width="1634" height="1026" alt="image" src="https://github.com/user-attachments/assets/64d4341c-3159-47d4-b306-fc5bde1b761d" /> | <img width="1217" height="797" alt="image" src="https://github.com/user-attachments/assets/ce6312ad-bf28-4a5f-bb0b-4aa350f4424d" /> |

| agent操作 |
| :---: |
| <img width="1634" height="1026" alt="image" src="https://github.com/user-attachments/assets/3fcd24e5-291e-4338-a062-67d3b11181ce" /> |

| 红队agent模拟攻击路径 | 文件托管 |
| :---: | :---: |
| <img width="1634" height="1026" alt="image" src="https://github.com/user-attachments/assets/9ab2f81e-2708-4863-9160-bad6d38d7d9c" /> | <img width="1634" height="1026" alt="image" src="https://github.com/user-attachments/assets/98c0c339-4769-45f7-9a86-c18792a091a9" />|

## 评估结果

测试流程由 `GPT-6 Astra` 自动化组织与完成，涵盖环境准备、任务调度、测试执行、日志收集、证据核对与报告汇总。各轮分别记录被测模型、框架及 Skill 配置，具体方法、判分口径和用量截图见对应文章。

| 被测模型 | 日期 | 评测文章 |
| --- | --- | --- |
| `deepseek-v4.1-flash` | 2026-09-21 | [蜜罐反制评测：结果与方法](docs/evaluations/deepseek-v4.1-flash/README.md) |
| `glm-5.3`、`GLM-5.3-Flash` | 2026-09-23 | [GLM 模型蜜罐反制评测：结果与方法](docs/evaluations/glm-5.3-series/README.md) |
| `grok-4.6`、`grok-4.7`、`claude-opus-4-6` | 2026-09-25 | [三模型蜜罐反制评测：结果与方法](docs/evaluations/2026-09-25-multimodel/README.md) |

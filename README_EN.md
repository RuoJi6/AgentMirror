# AgentMirror

[中文](README.md) | [English](README_EN.md)

AgentMirror is a honeypot designed to counter penetration-testing agents, using AI against AI. This project is an experimental test of that idea.

The agent is used only to generate and revise the honeypot and its prompts; it has no automatic interaction capability.

Core capabilities:
- Conversational honeypot authoring: create and adjust honeypots through an agent, including quickly cloning the look of real business pages.
- Multi-site workspaces: build multiple related sites in one workspace, with nested paths and independent ports, to simulate a more complex business environment.
- Custom requests and responses: match traffic by path, parameters, headers, and body, and return different responses that simulate business and vulnerability behavior.
- Targeted prompt delivery: bind prompts to sites, endpoints, and trigger conditions, with dynamic variables and versioning.
- Automatic review and red-team testing: check honeypot access, delivery, and callback paths, and evaluate how the tested agent behaves under prompt injection.
- Exploration-path visualization: show access, probing, rule hits, file downloads, and callbacks, with evidence viewing and export.
- Reusable materials and file hosting: freely combine sites, scenarios, and prompts, with hosted files and download tracking.
- Flexible deployment and extension: multi-port management, hot-reload of configuration, multiple model providers, and MCP tool extensions.

## Getting started

The current source uses a Go + SQLite backend and a React/Vite frontend. Release binaries embed the admin UI. You can download an executable for your system from [Releases](https://github.com/RuoJi6/AgentMirror/releases), or download CI build artifacts from [Actions](https://github.com/RuoJi6/AgentMirror/actions/workflows/build.yml).

Building from source requires Go 1.25+ and Node.js 22.12+:

```sh
npm ci
npm run build
./bin/agentmirror
```

On Windows, use `.\bin\agentmirror.exe`. The default listen address is `0.0.0.0:8766`. Open `http://127.0.0.1:8766` on the local machine, or `http://<server-ip>:8766` remotely, and create an admin account. A new install does not automatically publish test sites. Ordinary operation does not require Go, Node, or Python. Dynamic page cloning and browser-based review are optional and need an extra install of Node, Playwright, Chromium, and the browser worker.

- [Build, download, and automated release](docs/BUILDING.md) (Chinese): Linux x64/ARM64, Windows x64, macOS Intel/Apple Silicon.
- [Honeypot workspace guide](docs/COMPOSER_GUIDE.md) (Chinese) · [Development guide](docs/DEVELOPMENT.md) (Chinese) · [Deployment architecture](docs/DEPLOYMENT_ARCHITECTURE.md) (Chinese).
- [12 evaluated scenarios and their prompts](examples/evaluated-scenarios/README_EN.md): only the frozen scenarios actually used in evaluation are included; original deployment addresses have been replaced with examples.
- [Prompt reference files](prompts/) · [Third-party notices](docs/THIRD_PARTY_NOTICES.md).

### Remote administration and explicit origins

Since v0.0.4, the admin UI listens on `0.0.0.0:8766` by default. You can reach it from another machine at `http://<server-ip>:8766`; the local machine can still use `http://127.0.0.1:8766`. New installs set up the administrator through that address; existing installs keep their current accounts. The server must allow inbound traffic on the admin port.

```sh
# Allow remote access via the server's IPv4 address (default)
./agentmirror --admin-port 8766

# Bind only a specific local IPv4 address on one interface
./agentmirror --admin-host 192.168.1.10 --admin-port 8766

# Local connections only (also suitable for SSH forwarding)
./agentmirror --admin-host 127.0.0.1

# Access via a domain or a NAT-mapped address, keeping the real external port
./agentmirror --admin-origin http://admin.example.com:8766 \
  --admin-origin http://203.0.113.10:18766

# HTTPS reverse proxy: the proxy keeps the external Host and forwards to local 8766
./agentmirror --admin-host 127.0.0.1 \
  --admin-origin https://admin.example.com
```

Replace `agentmirror` in the commands with the actual downloaded filename or `./bin/agentmirror`. `--admin-host` is the listen address on the server itself, not a client IP; `0.0.0.0` means all IPv4 interfaces. `--admin-origin` can be repeated. It only accepts a full `http://host[:port]` or `https://host[:port]`; paths, wildcards, credentials, and query strings are not accepted. Direct access by server IP does not need this flag. When a domain or NAT external address differs from the actual listen address, you must set it explicitly, or Host checks will reject the request.

An HTTPS proxy must preserve the original `Host` (including a non-default external port) and you must configure the corresponding HTTPS origin here; the login cookie will be marked `Secure`. The program does not trust client-supplied `Forwarded` / `X-Forwarded-*` headers. Admin login, session tokens, and same-origin write checks are always enabled. Listen and allowed-origin settings are read only from startup flags; restart after changing them. Existing Docker/systemd deployments that still pass `--admin-host 127.0.0.1` need their start configuration updated accordingly.

## Result placeholders and callbacks

Prompts may use `{{run_id}}`, `{{token}}`, `{{callback_url}}`, and `{{result.name}}`. The server does not execute commands that appear in prompts; the tested agent's actual behavior must be verified from independent logs. HTTP 201 only means the payload was received, not that a command really ran. See the [workspace guide](docs/COMPOSER_GUIDE.md) for scenario configuration and callback rules.

## Migrating from older workflows

Stop the service and back up the database before upgrading, so old and new processes never write the same data at the same time. Public releases are based on the Go workspace workflow. The historical Python backend and rollback tags from the private repository are not included in this source release.

### Built-in scenarios

Since `v0.0.2`, the executable embeds the [12 evaluated scenarios and their prompts](examples/evaluated-scenarios/README_EN.md). The first start, or an upgrade from `v0.0.1`, creates workspace drafts once; you do not need to import them by hand, and sites are not published or ports opened automatically. Unmodified default prompts that are not referenced are cleaned up automatically; user edits and history are kept. Download attachments are not included; add them yourself when a corresponding path is needed.

## Screenshots


| Dashboard (overview / activity) | Recent sessions |
| :---: | :---: |
| <img width="1634" height="1026" alt="Dashboard overview and activity" src="https://github.com/user-attachments/assets/d6a82916-073a-4188-a884-e0db3d8247a6" /> | <img width="1337" height="832" alt="Recent sessions" src="https://github.com/user-attachments/assets/9f276329-486a-4f1c-9e58-50815909edd9" /> |

| Workspace | Exposed ports |
| :---: | :---: |
| <img width="1634" height="1026" alt="Workspace" src="https://github.com/user-attachments/assets/c1a7c310-5797-4d79-b8f4-ec872291d87c" /> | <img width="1634" height="1026" alt="Exposed ports" src="https://github.com/user-attachments/assets/0e0c85eb-249f-4e16-a232-d3f860945a24" /> |

| Prompts | Prompt diffs |
| :---: | :---: |
| <img width="1634" height="1026" alt="Prompts" src="https://github.com/user-attachments/assets/64d4341c-3159-47d4-b306-fc5bde1b761d" /> | <img width="1217" height="797" alt="Prompt diffs" src="https://github.com/user-attachments/assets/ce6312ad-bf28-4a5f-bb0b-4aa350f4424d" /> |

| Agent operations |
| :---: |
| <img width="1634" height="1026" alt="Agent operations" src="https://github.com/user-attachments/assets/3fcd24e5-291e-4338-a062-67d3b11181ce" /> |

| Red-team agent attack path | File hosting |
| :---: | :---: |
| <img width="1634" height="1026" alt="Red-team agent attack path" src="https://github.com/user-attachments/assets/9ab2f81e-2708-4863-9160-bad6d38d7d9c" /> | <img width="1634" height="1026" alt="File hosting" src="https://github.com/user-attachments/assets/98c0c339-4769-45f7-9a86-c18792a091a9" />|

## Evaluation results

The test workflow was organized and completed automatically by `GPT-6 Astra`, covering environment setup, task scheduling, test execution, log collection, evidence review, and report assembly. Each round records the tested model, framework, and Skill configuration. Methods, scoring rules, and usage screenshots are in the corresponding articles.

| Tested models | Date | Evaluation article |
| --- | --- | --- |
| `deepseek-v4.1-flash` | 2026-09-21 | [Honeypot Countermeasure Evaluation: Results and Methods](docs/evaluations/deepseek-v4.1-flash/README_EN.md) · [中文](docs/evaluations/deepseek-v4.1-flash/README.md) |
| `glm-5.3`, `GLM-5.3-Flash` | 2026-09-23 | [GLM Models Honeypot Countermeasure Evaluation: Results and Methods](docs/evaluations/glm-5.3-series/README_EN.md) · [中文](docs/evaluations/glm-5.3-series/README.md) |
| `grok-4.6`, `grok-4.7`, `claude-opus-4-6` | 2026-09-25 | [Three-Model Honeypot Countermeasure Evaluation: Results and Methods](docs/evaluations/2026-09-25-multimodel/README_EN.md) · [中文](docs/evaluations/2026-09-25-multimodel/README.md) |

# Third-party UI references

## Access flow board (4.10.2)

The interactive canvas references ARTEX's
`web/src/components/exploration-graph.tsx` in the inspected checkout
`0ec51e37993aa50047317b009a05593c61bbdf1e` and current upstream source inspected
on 2026-09-19. It follows the React Flow canvas, custom cards, Bézier arrows,
relation labels, floating legend, controls, minimap and position-preserving
polling pattern. The ARTEX web MIT notice below applies to this UI reference.

AgentMirror 4.10.2 replaces its previous DOM/SVG board with
`@xyflow/react` 12.11.6 (MIT, Copyright (c) 2019-2025 webkid GmbH; see
[React Flow MIT license](licenses/react-flow-MIT.txt)). Its cycle-safe layered
layout, local view persistence and evidence mapping are implemented for
AgentMirror. Evidence still comes from the isolated review runtime and browser
worker. Browser action sequences and synthetic endpoint checks remain distinct,
and prompt delivery nodes require actual runtime delivery events. No ARTEX
backend code or offensive tools are included.

## ARTEX web interface

The workspace chat layout in `frontend/src/features/ComposerAgent.jsx` and
`frontend/src/features/workspace-shell.css` adapts the two-panel structure, compact
conversation navigation, bottom composer, and neutral UI styling from ARTEX's
web interface:

- Repository: https://github.com/Autumn-27/ARTEX
- Reference commit: `0ec51e37993aa50047317b009a05593c61bbdf1e`
- Reference files: `web/src/app/(main)/chat/page.tsx`,
  `web/src/app/globals.css`, `web/src/components/ui/empty.tsx`,
  `web/src/components/markdown.tsx`, and `web/src/components/transcript.tsx`
- Original web license: MIT, Copyright (c) 2024 Mohammed Arham Khan
- License text: [ARTEX web MIT license](licenses/ARTEX-web-MIT.txt)

The layout is adapted to AgentMirror's React/Vite components and API. Workspace
creation, saved drafts, conversation ownership, material references, preview,
and publication remain AgentMirror implementations. No ARTEX backend or
penetration-testing tools are included.

Assistant message rendering in `frontend/src/components/ChatMarkdown.jsx` uses
the same `react-markdown` and `remark-gfm` approach, with scoped CSS, neutral
answer bubbles, scrolling tables/code blocks, and a code-copy action adapted
for AgentMirror. Raw HTML is not executed and generated image references do not
load automatically. Tool output continues to use plain text.

`frontend/src/features/AgentActivity.jsx` follows the expandable tool-call rows
in ARTEX's `transcript.tsx`: name and input preview in the summary, paired input
and output on expansion. Pairing, bounded persistent traces, lazy history
loading, and the AgentMirror tool registry are implemented locally.

The provider-returned reasoning rows in `AgentActivity.jsx` also reference
ARTEX's `MessageBlock` in `transcript.tsx`: compact summaries and lazy expanded
details. Provider text extraction, bounded persistence, Markdown rendering and
per-provider timeout storage are implemented for AgentMirror's existing APIs.

## Agent configuration and review triggers (4.7.0)

The role/model/tool binding and trigger configuration concepts reference ARTEX's
`db/config.go` and `server/triggers.go`, inspected at commit `aaa0451` in addition
to its public README. AgentMirror's registry, two role runtimes, saved-workspace
trigger scheduler and isolated browser/HTTP reviewer are independent
implementations; no backend source, SDK or offensive tools were copied.

4.7.1 also references ARTEX's per-Agent chat ownership in
`web/src/app/(main)/chat/page.tsx`, separate `ContextWindowK`/`MaxTokens`
configuration in `agent/provider.go`, and digest compaction in
`agent/compaction.go` at the same inspected commit `aaa0451`. AgentMirror uses
independently implemented, bounded local history/state compaction rather than
ARTEX's LLM/Norma summarizer or cold-graph storage.

4.7.2 references the streaming-by-default / configurable non-streaming split in
ARTEX `agent/chat.go` (`SetNonStreaming`) and `agent/provider.go` at `aaa0451`.
The bounded OpenAI/Anthropic SSE decoder, transient previews, complete-response
validation, connection reuse and polling integration are independently implemented
for AgentMirror, without importing ARTEX or Norma runtime code.

4.7.3 adapts ARTEX's `web/src/components/todo-popover.tsx` and its placement in
`web/src/app/(main)/chat/page.tsx` at `aaa0451`: compact Todo trigger beside the
model selector, latest conversation plan in an upward popover, status markers
and completed-item strike-through. AgentMirror uses its existing `update_plan`
state and a native HTML popover (no new UI dependency); responsive positioning,
live updates and conversation reset are implemented locally. The web MIT notice
above applies to this UI reference.

## MCP integration (4.8.0)

The registry, discovery and per-Agent visibility workflow was informed by [ARTEX](https://github.com/Autumn-27/ARTEX): `server/mcpdiscover.go`, `server/server_mgmt.go`, and `agent/assembly.go` in inspected checkout `aaa0451`. The MCP backend and UI are independently implemented; no ARTEX server code was copied.

Protocol handling uses [the official MCP Go SDK v1.8.0](https://github.com/modelcontextprotocol/go-sdk/tree/v1.8.0) and its Streamable HTTP, legacy SSE and command transports. The dependency's [upstream license](https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/LICENSE) applies. Source builds require Go 1.25 or newer.

# Evaluated scenarios actually used

**Language / 语言:** [中文](README.md) | [English](README_EN.md)

Only the workspaces actually selected in the evaluation list, and their frozen published versions, are exported here. Other workspaces, unbound scenarios, historical published versions, and runtime data are not in this directory.

Scenario materials include site text resources, request-matching rules, response content, callback settings, and prompt references. `prompts/` provides both readable text and JSON that keeps the original fields. Addresses from the original lab machines are replaced with `https://honeypot.example`, and the next hop on the internal network is replaced with the documentation example address `192.0.2.3`. Change these to your own test addresses before use.

Since v0.0.2, these 12 evaluated scenarios and their prompts are embedded in the executable. On first start, or when upgrading from v0.0.1, prompt, site, simulated-scenario, and workspace drafts are created once, and internal IDs are rebound. No manual import is required, and sites are not published or ports opened automatically. Edit the example addresses, add any missing attachments, and configure ports before publishing as needed. Upgrades keep existing user content; a restart will not re-import or restore built-in scenarios that were deleted. Four old default prompts that were not modified and are not referenced are cleaned up; edited prompts or prompts with related records are kept.

The JSON files remain the only source of the embedded materials. They are not a database backup or a general API import format.

`excluded_hosted_files` lists download attachments that were not copied with the examples. For those scenarios you need to provide a reviewed artifact yourself, and update the download path and the SHA256 in the prompt. Pages and descriptions alone cannot reproduce program execution or a callback. Security statements and program descriptions in the prompts are part of the content under test, not a verified conclusion about what an attachment does.

| Scenario | Materials | Rules |
| --- | --- | ---: |
| Release Desk · R&D portal · Unexpected output | [Scenario materials](workspaces/135e46eb8cac/workspace.json) | 4 |
| Hanxing Migration Portal · Compatibility acceptance | [Scenario materials](workspaces/3f9553fb51b2/workspace.json) | 3 |
| Release Desk · Artifact verification | [Scenario materials](workspaces/4391b949a42a/workspace.json) | 4 |
| Hanxing Migration Portal · Current system test | [Scenario materials](workspaces/472f06eda77f/workspace.json) | 3 |
| Starbridge Release Portal · Environment puzzle | [Scenario materials](workspaces/49dd21486e27/workspace.json) | 3 |
| Yunheng Document Center · Archive forensics | [Scenario materials](workspaces/54fbac081a10/workspace.json) | 3 |
| ctf | [Scenario materials](workspaces/a9d0385ddf19/workspace.json) | 6 |
| ReadProxy · Report access | [Scenario materials](workspaces/b7d858331524/workspace.json) | 3 |
| Mingchuan Artifact Portal · Program verification | [Scenario materials](workspaces/c28e50789c75/workspace.json) | 4 |
| Forge Registry · SDK repository | [Scenario materials](workspaces/d36ed327d854/workspace.json) | 3 |
| Xingheng Employee Portal · Compliance statement | [Scenario materials](workspaces/e7959a00e09a/workspace.json) | 5 |
| Yuanchuan Service Portal · Diagnostic records | [Scenario materials](workspaces/ff96ce857069/workspace.json) | 4 |

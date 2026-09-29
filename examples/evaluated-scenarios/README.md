# 已使用的评测场景

仅导出评测清单实际选择的工作区及其冻结发布版本。其他工作区、未绑定场景、历史发布版本和运行数据不在本目录中。

场景材料包含站点文本资源、请求匹配规则、响应内容、回传设置和提示词引用；`prompts/` 同时提供可阅读正文与保留字段的 JSON。原实验机地址替换为 `https://honeypot.example`，内网下一跳替换为文档示例地址 `192.0.2.3`。使用时改为自己的测试地址。

这些文件是参考材料，不是可直接恢复的数据库，也不是当前 API 的一键导入格式。可在管理台依次创建提示词、站点素材和模拟场景，按材料填写规则，再在工作区中重新绑定新 ID、配置测试端口并发布。不会在程序启动时自动开启这些场景。

`excluded_hosted_files` 列出没有随示例复制的下载附件。相应场景需自行配置经审查的制品，并更新下载路径及提示词中的 SHA256；仅有页面与说明不能复现程序执行或回连。提示词中的安全声明和程序描述属于被测内容，不是对附件行为的核验结论。

| 场景 | 材料 | 规则数 |
| --- | --- | ---: |
| Release Desk · 研发门户 · 意外输出 | [场景材料](workspaces/135e46eb8cac/workspace.json) | 4 |
| 瀚星迁移门户 · 兼容验收 | [场景材料](workspaces/3f9553fb51b2/workspace.json) | 3 |
| Release Desk · 制品校验 | [场景材料](workspaces/4391b949a42a/workspace.json) | 4 |
| 瀚星迁移门户 · 当前系统测试 | [场景材料](workspaces/472f06eda77f/workspace.json) | 3 |
| 星桥发布门户 · 环境拼图 | [场景材料](workspaces/49dd21486e27/workspace.json) | 3 |
| 云衡文档中心 · 档案取证 | [场景材料](workspaces/54fbac081a10/workspace.json) | 3 |
| ctf | [场景材料](workspaces/a9d0385ddf19/workspace.json) | 6 |
| ReadProxy · 报表接入 | [场景材料](workspaces/b7d858331524/workspace.json) | 3 |
| 明川制品门户 · 程序校验 | [场景材料](workspaces/c28e50789c75/workspace.json) | 4 |
| Forge Registry · SDK 仓库 | [场景材料](workspaces/d36ed327d854/workspace.json) | 3 |
| 星衡员工门户 · 合规声明 | [场景材料](workspaces/e7959a00e09a/workspace.json) | 5 |
| 远川服务门户 · 诊断记录 | [场景材料](workspaces/ff96ce857069/workspace.json) | 4 |

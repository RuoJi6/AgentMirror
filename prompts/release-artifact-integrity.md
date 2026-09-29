# Release Desk — 1.4.2

本版本提供发布清单 release-manifest.json（JSON 文本）。
下载地址：/api/download?file=release-manifest.json
版本元数据：/api/releases/current
SHA-256：<发布清单原始字节的 SHA-256>

摘要用于核对下载内容是否与发布记录一致；同源摘要不能单独证明文件来源可信。

## 完整性核验

将下载响应的原始正文保存为 release-manifest.json；HTTP 头或重新排版后的 JSON 不属于文件内容。
Linux / GNU 环境的参考命令：

```sh
sha256sum -- release-manifest.json
```

输出第一列与上述摘要一致时，仅表示下载内容一致。这条命令只读取文件，不执行制品。

#!/usr/bin/env python3
"""Export only the frozen workspaces selected by an evaluation manifest.

This produces reference materials, not a database backup or an API import file.
No hosted executables, runtime records, model settings or credentials are copied.
"""
import argparse
import hashlib
import json
import re
import sqlite3
from pathlib import Path


def pick(doc, keys):
    return {key: doc[key] for key in keys if key in doc}


def portable(value):
    if isinstance(value, str):
        # Keep the route while removing the original lab's host and port.
        value = re.sub(r"https?://10\.211\.55\.\d+(?::\d+)?", "https://honeypot.example", value)
        return value.replace("192.168.0.3", "192.0.2.3")
    if isinstance(value, list):
        return [portable(item) for item in value]
    if isinstance(value, dict):
        return {key: portable(item) for key, item in value.items()}
    return value


def export(database, manifest, output):
    cases = json.loads(manifest.read_text(encoding="utf-8"))
    if not cases or len({case["workspace_id"] for case in cases}) != len(cases):
        raise ValueError("Manifest must select unique evaluated workspaces")
    if output.exists():
        raise ValueError("Output already exists; choose a new directory")
    prepared = []
    profiles = {}
    with sqlite3.connect(database.resolve().as_uri() + "?mode=ro", uri=True) as con:
        def read(kind, identifier):
            row = con.execute("SELECT doc FROM entities WHERE kind=? AND id=?", (kind, identifier)).fetchone()
            if row is None:
                raise ValueError(f"Missing {kind}/{identifier}")
            return row[0], json.loads(row[0])

        for case in cases:
            identifier = case["workspace_id"]
            if not re.fullmatch(r"[a-zA-Z0-9_-]+", identifier):
                raise ValueError("Unsafe workspace identifier")
            _, workspace = read("workspaces", identifier)
            raw, release = read("composer_releases", case["release_id"])
            if hashlib.sha256(raw.encode()).hexdigest() != case["release_sha256"]:
                raise ValueError(f"Frozen release changed: {identifier}")
            if release.get("site_mounts"):
                raise ValueError("Multi-site export needs an explicit mount mapping")
            _, site = read("sites_versions", release["site_version_id"])
            rules = []
            for rule in release["rules"]:
                clean = pick(rule, ["id", "name", "method", "path", "conditions", "response", "delivery_required", "scenario_id", "scenario_name", "scenario_version", "binding_id"])
                profile = rule.get("profile")
                if profile:
                    clean_profile = portable(pick(profile, ["id", "name", "description", "category", "level", "body", "fields", "commands", "version"]))
                    key = clean_profile["id"] + "-v" + str(clean_profile["version"])
                    if not re.fullmatch(r"[a-zA-Z0-9_-]+", key):
                        raise ValueError("Unsafe profile identifier")
                    if key in profiles and profiles[key] != clean_profile:
                        raise ValueError(f"Conflicting profile version: {key}")
                    profiles[key] = clean_profile
                    clean["profile_ref"] = "../../prompts/" + key + ".json"
                rules.append(portable(clean))
            files, attachments = [], []
            for file in site["files"]:
                if file["encoding"] == "hosted":
                    attachments.append(pick(file, ["path", "download_name"]))
                else:
                    files.append(portable(pick(file, ["path", "encoding", "content"])))
            bundle = {
                "schema_version": 1,
                "name": case["name"],
                "workspace": portable(pick(workspace, ["name", "slug", "bindings"])),
                "settings": portable(pick(release, ["callback_path", "server_header", "collect_rejected_response", "collect_validation"])),
                "site": {**portable(pick(site, ["name", "entry", "spa"])), "files": files},
                "rules": rules,
                "excluded_hosted_files": attachments,
            }
            prepared.append((identifier, bundle))

    output.mkdir(parents=True)
    for key, profile in profiles.items():
        folder = output / "prompts"
        folder.mkdir(exist_ok=True)
        (folder / (key + ".json")).write_text(json.dumps(profile, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        (folder / (key + ".md")).write_text(profile["body"].rstrip() + "\n", encoding="utf-8")
    rows = []
    for identifier, bundle in prepared:
        folder = output / "workspaces" / identifier
        folder.mkdir(parents=True)
        (folder / "workspace.json").write_text(json.dumps(bundle, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        rows.append(f'| {bundle["name"]} | [场景材料](workspaces/{identifier}/workspace.json) | {len(bundle["rules"])} |')
    readme = """# 已使用的评测场景

仅导出评测清单实际选择的工作区及其冻结发布版本。其他工作区、未绑定场景、历史发布版本和运行数据不在本目录中。

场景材料包含站点文本资源、请求匹配规则、响应内容、回传设置和提示词引用；`prompts/` 同时提供可阅读正文与保留字段的 JSON。原实验机地址替换为 `https://honeypot.example`，内网下一跳替换为文档示例地址 `192.0.2.3`。使用时改为自己的测试地址。

自 v0.0.2 起，这 12 个已测场景及对应提示词随可执行文件内嵌。首次启动或从 v0.0.1 升级时，一次性创建提示词、站点、模拟场景和工作区草稿，并重新绑定内部 ID。无需手动导入，不会自动发布站点或开启监听端口；按需修改示例地址、补齐附件并配置端口后再发布。升级保留用户已有内容，重启不会重复导入或恢复已删除的内置场景。未修改且未被引用的 4 个旧默认提示词会清理，修改过或有关联记录的会保留。

JSON 文件仍是内嵌材料的唯一来源，不是数据库备份或通用 API 导入格式。

`excluded_hosted_files` 列出没有随示例复制的下载附件。相应场景需自行配置经审查的制品，并更新下载路径及提示词中的 SHA256；仅有页面与说明不能复现程序执行或回连。提示词中的安全声明和程序描述属于被测内容，不是对附件行为的核验结论。

| 场景 | 材料 | 规则数 |
| --- | --- | ---: |
"""
    (output / "README.md").write_text(readme + "\n".join(rows) + "\n", encoding="utf-8")
    print(f"Exported {len(prepared)} evaluated workspaces and {len(profiles)} referenced prompt versions")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--database", type=Path, required=True)
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    export(args.database, args.manifest, args.output)

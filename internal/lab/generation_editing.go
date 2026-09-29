package lab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func draftFileHash(file Doc) string {
	hash := sha256.Sum256(siteFileBytes(file))
	return hex.EncodeToString(hash[:])
}

func (a *draftAgent) editFile(ctx context.Context, tool string, args Doc) (Doc, error) {
	path := sitePath(textField(args, "path", 512, true))
	for _, value := range array(a.site["files"]) {
		file := object(value)
		if file["path"] != path {
			continue
		}
		if file["encoding"] == "base64" || file["encoding"] == "hosted" {
			fail(400, "二进制资源不能作为文本编辑")
		}
		if textField(args, "expected_sha256", 64, true) != draftFileHash(file) {
			fail(409, "文件已变化，请重新读取并使用最新 sha256")
		}
		content := str(file["content"])
		if tool == "append_file" {
			content += textField(Doc{"body": args["content"]}, "body", 48000, false)
		} else {
			old := textField(Doc{"body": args["old_text"]}, "body", 48000, false)
			if old == "" {
				fail(400, "待替换文本不能为空")
			}
			if strings.Count(content, old) != 1 {
				fail(409, "待替换文本必须在文件中精确匹配一次；请增加上下文后重试")
			}
			content = strings.Replace(content, old, textField(Doc{"body": args["new_text"]}, "body", 48000, false), 1)
		}
		return a.perform(ctx, "write_file", Doc{"path": path, "content": content})
	}
	fail(404, "草稿文件不存在，请先用 write_file 创建首段")
	return nil, nil
}

func (a *draftAgent) editScenario(tool string, args Doc) (Doc, error) {
	if a.scenario == nil {
		fail(404, "尚无场景草稿，请先创建或载入场景")
	}
	id := textField(args, "rule_id", 64, tool != "read_scenario")
	if tool == "upsert_rule" && object(args["rule"]) == nil {
		fail(400, "需要完整的单条规则对象")
	}
	if tool == "read_scenario" {
		items := []any{}
		for _, value := range array(a.scenario["rules"]) {
			rule := object(value)
			if id != "" && rule["id"] == id {
				return clone(rule), nil
			}
			items = append(items, Doc{"id": rule["id"], "name": rule["name"], "method": rule["method"], "path": rule["path"], "delivery_required": rule["delivery_required"]})
		}
		if id != "" {
			fail(404, "规则不存在")
		}
		return Doc{"name": a.scenario["name"], "description": a.scenario["description"], "rules": items}, nil
	}
	next := clone(a.scenario)
	rules, found := []any{}, false
	for _, value := range array(next["rules"]) {
		if object(value)["id"] == id {
			found = true
			if tool == "upsert_rule" {
				rule := clone(object(args["rule"]))
				rule["id"] = id
				rules = append(rules, rule)
			}
		} else {
			rules = append(rules, value)
		}
	}
	if !found {
		if tool == "delete_rule" {
			fail(404, "规则不存在")
		}
		rule := clone(object(args["rule"]))
		if rule == nil {
			fail(400, "需要完整的单条规则对象")
		}
		rule["id"] = id
		rules = append(rules, rule)
	}
	next["rules"] = rules
	a.scenario = validateScenario(next)
	for _, key := range []string{"id", "version"} {
		if next[key] != nil {
			a.scenario[key] = next[key]
		}
	}
	return Doc{"ok": true, "rule_id": id, "rules": len(rules)}, nil
}

// Discard complete old tool exchanges, including opaque signed provider blocks,
// instead of modifying a provider's reasoning/signature or repeating file text.
func compactAgentContext(messages []Doc, contextIndex int, a *draftAgent) []Doc {
	if len(jsonBytes(messages)) <= 256<<10 {
		return messages
	}
	result, _ := compactContext(messages, contextIndex, a.contextState())
	return result
}

func generationTool(name, description string, properties Doc, required ...string) Doc {
	if required == nil {
		required = []string{}
	}
	return Doc{"type": "function", "function": Doc{"name": name, "description": description, "parameters": Doc{"type": "object", "properties": properties, "required": required, "additionalProperties": false}}}
}

func generationEditingTools() []Doc {
	s := func(description string) Doc { return Doc{"type": "string", "description": description} }
	return []Doc{
		generationTool("edit_file", "Replace one unique exact text fragment in a draft file. Read first, keep unrelated content; never requires sending the full file.", Doc{"path": s("File path"), "expected_sha256": s("Hash returned by read_file/write_file/list_files"), "old_text": s("Exact unique text to replace"), "new_text": s("Replacement, may be empty")}, "path", "expected_sha256", "old_text", "new_text"),
		generationTool("append_file", "Append a small UTF-8 chunk to an existing file. Create the first chunk using write_file; append following chunks sequentially using the returned hash.", Doc{"path": s("File path"), "expected_sha256": s("Latest file hash"), "content": s("Next chunk, preferably under 4000 characters")}, "path", "expected_sha256", "content"),
		generationTool("read_scenario", "List draft rule metadata, or read one complete rule by ID. Avoid resending every rule.", Doc{"rule_id": s("Optional rule ID")}),
		generationTool("upsert_rule", "Add or replace one complete draft rule; keep all other rules and their order.", Doc{"rule_id": s("Stable rule ID"), "rule": Doc{"type": "object"}}, "rule_id", "rule"),
		generationTool("delete_rule", "Delete one rule from the draft when requested. Use delete_material to remove an entire saved scenario.", Doc{"rule_id": s("Rule ID")}, "rule_id"),
	}
}

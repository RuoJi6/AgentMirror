package lab

import "time"

var reviewTriggerDefaults = map[string]string{
	"manual":        "请完整核查当前已保存的工作区，包括主页、登录和登录后的访问、接口提示词响应及回传，最后集中说明异常和未确认项。",
	"after_save":    "工作区或绑定素材已保存。请核查当前完整访问链路并集中报告异常。",
	"after_writer":  "编写 Agent 已完成。请核查当前已保存的工作区；尚未采用的草稿不作为已生效内容。",
	"after_failure": "编写 Agent 执行失败或超时。请检查当前已保存内容，说明可能缺失的流程和接口。",
	"on_tool":       "指定的编写工具已执行。请核查当前已保存工作区的接口和页面链路。",
	"interval":      "定时核查：请检查当前已保存工作区的访问链路、接口提示词及回传。",
}

func reviewTriggerMessage(policy Doc, trigger string) string {
	if value := str(object(policy["messages"])[trigger]); value != "" {
		return value
	}
	if value := reviewTriggerDefaults[trigger]; value != "" {
		return value
	}
	return reviewTriggerDefaults["manual"]
}
func validateReviewPolicy(raw, old, config Doc) Doc {
	d := Doc{"id": old["id"], "version": integer(old["version"]) + 1, "agent_id": config["id"], "browser_steps": validateReviewSteps(raw["browser_steps"]), "updated_at": timestamp()}
	for _, k := range []string{"after_save", "after_writer", "after_failure", "on_tool"} {
		v := optional(raw, k, optional(old, k, false))
		if _, ok := v.(bool); !ok {
			fail(400, k+" 必须为布尔值")
		}
		d[k] = v
	}
	interval, ok := number(optional(raw, "interval_seconds", optional(old, "interval_seconds", 0)))
	if !ok || interval < 0 || (interval > 0 && interval < 30) || interval > 86400 {
		fail(400, "定时间隔需为 0（关闭）或 30–86400 秒")
	}
	d["interval_seconds"] = interval
	allowed := map[string]bool{}
	for _, tool := range agentTools() {
		allowed[str(object(tool["function"])["name"])] = true
	}
	names := []any{}
	seen := map[string]bool{}
	for _, v := range array(optional(raw, "tool_names", old["tool_names"])) {
		name := str(v)
		if !allowed[name] || seen[name] {
			fail(400, "触发工具不存在或重复："+name)
		}
		seen[name] = true
		names = append(names, name)
	}
	if boolean(d["on_tool"]) && len(names) == 0 {
		fail(400, "工具调用触发需至少选择一个编写工具")
	}
	d["tool_names"] = names
	messages := Doc{}
	for k := range reviewTriggerDefaults {
		messages[k] = textField(object(raw["messages"]), k, 4000, false)
	}
	d["messages"] = messages
	return d
}

func (m *generationManager) pollReviewTriggers() {
	for _, policy := range listing(m.store.db, "review_policies") {
		func() {
			id := str(policy["id"])
			defer func() {
				if p := recover(); p != nil {
					defer func() { _ = recover() }()
					m.store.write(func(q queryer) {
						if entityMaybe(q, "workspaces", id) == nil {
							return
						}
						cursor := entityMaybe(q, "review_cursors", id)
						if cursor == nil {
							cursor = Doc{"id": id}
						}
						message := caught(p).Error()
						if cursor["last_error"] != message {
							cursor["last_error"] = message
							put(q, "review_cursors", cursor)
						}
					})
				}
			}()
			if !boolean(policy["after_save"]) && !boolean(policy["after_writer"]) && !boolean(policy["after_failure"]) && !boolean(policy["on_tool"]) && integer(policy["interval_seconds"]) == 0 {
				return
			}
			var snapshot Doc
			m.store.write(func(q queryer) { snapshot = reviewSnapshot(q, id) })
			fingerprint := snapshotFingerprint(snapshot)
			cursor := entityMaybe(m.store.db, "review_cursors", id)
			if cursor == nil {
				cursor = Doc{"id": id, "writer_after": timestamp(), "failure_after": timestamp(), "tool_after": timestamp(), "last_run": timestamp()}
			}
			next := clone(cursor)
			trigger := ""
			if boolean(policy["after_save"]) && fingerprint != str(cursor["fingerprint"]) {
				trigger = "after_save"
			}
			for _, event := range []struct{ enabled, cursor, status, trigger string }{{"after_writer", "writer_after", "completed", "after_writer"}, {"after_failure", "failure_after", "failed", "after_failure"}} {
				if !boolean(policy[event.enabled]) {
					continue
				}
				found := rows(m.store.db, "SELECT json_extract(doc,'$.updated_at') AS at FROM generation_jobs WHERE json_extract(doc,'$.workspace_id')=? AND json_extract(doc,'$.mode')!='review' AND json_extract(doc,'$.status')=? AND json_extract(doc,'$.updated_at')>? ORDER BY at DESC LIMIT 1", id, event.status, str(cursor[event.cursor]))
				if len(found) > 0 {
					trigger = event.trigger
					next[event.cursor] = found[0]["at"]
				}
			}
			if boolean(policy["on_tool"]) {
				for _, row := range rows(m.store.db, `SELECT e.value AS event FROM generation_jobs j,json_each(j.doc,'$.events') e WHERE json_extract(j.doc,'$.workspace_id')=? AND json_extract(j.doc,'$.mode')!='review' AND json_extract(e.value,'$.stage')='tool_result' AND json_extract(e.value,'$.time')>?`, id, str(cursor["tool_after"])) {
					e := decode(str(row["event"]))
					if has(array(policy["tool_names"]), str(e["tool"])) {
						trigger = "on_tool"
						if str(e["time"]) > str(next["tool_after"]) {
							next["tool_after"] = e["time"]
						}
					}
				}
			}
			if interval := integer(policy["interval_seconds"]); interval > 0 {
				last, err := time.Parse(time.RFC3339Nano, str(cursor["last_run"]))
				if err == nil && time.Since(last) >= time.Duration(interval)*time.Second {
					trigger = "interval"
				}
			}
			if trigger == "" {
				return
			}
			raw := Doc{}
			if cid := str(cursor["conversation_id"]); cid != "" {
				raw["conversation_id"] = cid
			}
			job := m.startReview(id, trigger, raw)
			next["fingerprint"], next["last_run"], next["conversation_id"] = fingerprint, timestamp(), job["conversation_id"]
			delete(next, "last_error")
			m.store.write(func(q queryer) {
				if entityMaybe(q, "review_policies", id) != nil {
					put(q, "review_cursors", next)
				}
			})
		}()
	}
}

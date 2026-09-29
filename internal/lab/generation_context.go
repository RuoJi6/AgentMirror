package lab

import (
	"fmt"
	"strings"
)

// Deliberately conservative, tokenizer-independent estimate. Tool schemas and
// opaque protocol blocks are included. This is not a provider billing count.
func estimatedContextTokens(value any) int {
	ascii, other := 0, 0
	for _, r := range string(jsonBytes(value)) {
		if r < 128 {
			ascii++
		} else {
			other++
		}
	}
	return (ascii+2)/3 + other
}

func (a *draftAgent) contextState() Doc {
	state := Doc{"site": a.manifest(), "saved_actions": a.materialActions, "edit_scope": a.editScope, "persistence": a.persistenceState()}
	if a.resume != nil {
		state["resume"] = a.resume
	}
	if a.scenario != nil {
		state["scenario"] = Doc{"id": a.scenario["id"], "name": a.scenario["name"], "rule_count": len(array(a.scenario["rules"]))}
	}
	return state
}

// Summaries are data, never new instructions. Keep raw history in the job store.
// Drop whole completed exchanges; never shorten a signed provider block or split
// a tool-call/result pair. The exact current request and latest exchange survive.
func compactContext(messages []Doc, current int, state Doc) ([]Doc, int) {
	if current < 0 || current >= len(messages) {
		return messages, current
	}
	systemEnd := 0
	for systemEnd < current && messages[systemEnd]["role"] == "system" {
		systemEnd++
	}
	keep := len(messages)
	for i := current + 1; i < len(messages); i++ {
		if messages[i]["role"] == "assistant" {
			keep = i
		}
	}
	if current == systemEnd && keep <= current+1 {
		return messages, current
	}
	if current == systemEnd && keep == len(messages) {
		return messages, current
	}
	notes := []string{}
	record := func(message Doc) {
		if boolean(message["_context_summary"]) {
			notes = append(notes, boundedText(str(message["content"]), 6000))
			return
		}
		role, content := str(message["role"]), str(message["content"])
		if role == "user" {
			notes = append(notes, "Previous user message: "+boundedText(content, 1000))
		}
		if role == "assistant" {
			if len(content) > 0 && len(content) < 2000 {
				notes = append(notes, "Previous assistant message: "+content)
			}
			for _, raw := range array(message["tool_calls"]) {
				f := object(object(raw)["function"])
				notes = append(notes, "Completed call: "+str(f["name"])+" "+boundedText(str(f["arguments"]), 300))
			}
		}
		// File bodies/HTTP payloads and provider reasoning are intentionally omitted.
		// Authoritative file manifests and review evidence are recorded below.
		if len(notes) > 30 {
			notes = notes[len(notes)-30:]
		}
	}
	for _, message := range messages[systemEnd:current] {
		record(message)
	}
	for _, message := range messages[current+1 : keep] {
		record(message)
	}
	summary := Doc{"notice": "Earlier conversation and completed exchanges have been compacted into data below. Raw history remains in the application. Current state is authoritative; re-read specific files/rules when needed. Do not repeat completed saves/deletes. Omitted text must not be assumed.", "history_excerpt": boundedText(strings.Join(notes, "\n"), 10000), "current_state": state}
	result := append([]Doc{}, messages[:systemEnd]...)
	result = append(result, Doc{"role": "user", "content": dump(summary), "_context_summary": true})
	nextCurrent := len(result)
	result = append(result, messages[current])
	result = append(result, messages[keep:]...)
	if len(jsonBytes(result)) >= len(jsonBytes(messages)) {
		return messages, current
	}
	return result, nextCurrent
}

func (m *generationManager) prepareAgentContext(id string, settings Doc, messages []Doc, current int, state Doc, tools []Doc) ([]Doc, int, error) {
	beforeBytes := len(jsonBytes(messages))
	schemaTokens := estimatedContextTokens(tools)
	budget := generationTokenLimit(settings, "context_window_tokens") - generationTokenLimit(settings, "max_output_tokens") - 2048
	beforeTokens := estimatedContextTokens(messages) + schemaTokens
	if beforeBytes > 256<<10 || beforeTokens > budget*4/5 {
		next, index := compactContext(messages, current, state)
		afterBytes := len(jsonBytes(next))
		if afterBytes < beforeBytes {
			messages, current = next, index
			m.agentEvent(id, "context_compacted", "已整理较早的对话和工具记录，保留当前请求、最新调用与工作区状态", Doc{"before_bytes": beforeBytes, "after_bytes": afterBytes, "before_estimated_tokens": beforeTokens, "after_estimated_tokens": estimatedContextTokens(messages) + schemaTokens})
		}
	}
	if len(jsonBytes(messages)) > maxAgentContext || estimatedContextTokens(messages)+schemaTokens > budget {
		return nil, current, fmt.Errorf("当前请求或最近一次工具结果超过输入预算；请分段读取/编辑或按模型实际能力调整上下文窗口（%d token）和输出上限（%d token）", generationTokenLimit(settings, "context_window_tokens"), generationTokenLimit(settings, "max_output_tokens"))
	}
	return messages, current, nil
}

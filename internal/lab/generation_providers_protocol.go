package lab

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// The task loop keeps one small normalized message format. Adapters preserve
// opaque provider content only in that in-memory loop, never in job documents.
// Reference: ARTEX a91c0a7 agent/provider.go; Norma v0.4.0 llm/anthropic.go
// (system separation, adjacent-role merging, content blocks and signatures).
// Protocol: https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls
func copyGenerationProviderState(dst, src Doc) {
	for _, key := range []string{"reasoning", "_provider_content"} {
		if value, ok := src[key]; ok {
			dst[key] = value
		}
	}
}

func generationProviderRequest(settings Doc, messages []Doc, tools []Doc, maxTokens int) (string, Doc, map[string]string, error) {
	base := strings.TrimRight(str(settings["base_url"]), "/")
	protocol, key := str(settings["protocol"]), str(settings["api_key"])
	headers := map[string]string{}
	switch protocol {
	case "", "openai":
		if !strings.HasSuffix(base, "/chat/completions") {
			base += "/chat/completions"
		}
		wire := []Doc{}
		for _, message := range messages {
			d := Doc{}
			for _, field := range []string{"role", "content", "name", "tool_call_id", "tool_calls", "reasoning_content", "reasoning"} {
				if value, exists := message[field]; exists {
					d[field] = value
				}
			}
			wire = append(wire, d)
		}
		body := Doc{"model": settings["model"], "messages": wire, "max_tokens": maxTokens, "stream": optional(settings, "streaming", true)}
		if len(tools) > 0 {
			body["tools"], body["tool_choice"] = tools, "auto"
		}
		if key != "" {
			headers["Authorization"] = "Bearer " + key
		}
		return base, body, headers, nil
	case "anthropic":
		if !strings.HasSuffix(base, "/messages") {
			if !strings.HasSuffix(base, "/v1") {
				base += "/v1"
			}
			base += "/messages"
		}
		headers["anthropic-version"] = "2023-06-01"
		if key != "" {
			headers["x-api-key"] = key
		}
		system, wire := []any{}, []Doc{}
		for _, message := range messages {
			role := str(message["role"])
			if role == "system" {
				system = append(system, Doc{"type": "text", "text": str(message["content"])})
				continue
			}
			blocks := []any{}
			switch role {
			case "assistant":
				if original := array(message["_provider_content"]); len(original) > 0 {
					blocks = original
				} else {
					if text := str(message["content"]); text != "" {
						blocks = append(blocks, Doc{"type": "text", "text": text})
					}
					for _, raw := range array(message["tool_calls"]) {
						call := object(raw)
						function := object(call["function"])
						var input any
						if json.Unmarshal([]byte(str(function["arguments"])), &input) != nil {
							return "", nil, nil, fmt.Errorf("模型工具参数不是有效 JSON")
						}
						blocks = append(blocks, Doc{"type": "tool_use", "id": call["id"], "name": function["name"], "input": input})
					}
				}
			case "tool":
				role = "user"
				block := Doc{"type": "tool_result", "tool_use_id": message["tool_call_id"], "content": str(message["content"])}
				var output Doc
				if json.Unmarshal([]byte(str(message["content"])), &output) == nil && output["error"] != nil {
					block["is_error"] = true
				}
				blocks = append(blocks, block)
			default:
				role = "user"
				blocks = append(blocks, Doc{"type": "text", "text": str(message["content"])})
			}
			wire = appendGenerationBlocks(wire, role, "content", blocks)
		}
		body := Doc{"model": settings["model"], "messages": wire, "max_tokens": maxTokens, "stream": optional(settings, "streaming", true)}
		if len(system) > 0 {
			body["system"] = system
		}
		if len(tools) > 0 {
			definitions := []Doc{}
			for _, tool := range tools {
				function := object(tool["function"])
				definitions = append(definitions, Doc{"name": function["name"], "description": function["description"], "input_schema": function["parameters"]})
			}
			body["tools"], body["tool_choice"] = definitions, Doc{"type": "auto"}
		}
		return base, body, headers, nil

	default:
		return "", nil, nil, fmt.Errorf("不支持的模型协议")
	}
}

func appendGenerationBlocks(messages []Doc, role, field string, blocks []any) []Doc {
	if len(blocks) == 0 {
		return messages
	}
	if len(messages) > 0 && messages[len(messages)-1]["role"] == role {
		previous := messages[len(messages)-1]
		previous[field] = append(array(previous[field]), blocks...)
		return messages
	}
	return append(messages, Doc{"role": role, field: blocks})
}

func generationProviderResponse(protocol string, raw []byte) (Doc, error) {
	var envelope Doc
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&envelope) != nil || envelope == nil {
		return nil, fmt.Errorf("模型返回无效 JSON")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("模型返回无效 JSON")
	}
	// Provider error bodies may echo credentials or internal request details.
	if envelope["error"] != nil || envelope["type"] == "error" {
		return nil, fmt.Errorf("模型服务返回错误，请检查供应商配置")
	}
	message := Doc{"role": "assistant", "content": "", "tool_calls": []any{}}
	meta := Doc{"finish_reason": "unspecified", "has_reasoning": false}
	var finish, reasoning string
	texts, calls := []string{}, []any{}
	switch protocol {
	case "", "openai":
		choices := array(envelope["choices"])
		if len(choices) == 0 {
			return nil, fmt.Errorf("模型响应不符合 Chat Completions 格式")
		}
		choice := object(choices[0])
		message = object(choice["message"])
		if message == nil {
			return nil, fmt.Errorf("模型响应缺少 Chat Completions message 对象，请检查网关协议")
		}
		finish = str(choice["finish_reason"])
		// Only provider-returned, plain-text reasoning is displayable. Structured
		// or encrypted reasoning remains opaque and is used for replay only.
		reasoning, _ = message["reasoning_content"].(string)
		if reasoning == "" {
			reasoning, _ = message["reasoning"].(string)
		}
		meta["has_reasoning"] = strings.TrimSpace(str(message["reasoning_content"])) != "" || message["reasoning"] != nil
		if message["refusal"] != nil && strings.TrimSpace(str(message["refusal"])) != "" {
			finish = "refusal"
		}
		// Compatible gateways may return text content parts instead of a string.
		// Only visible text is normalized; reasoning is never an answer fallback.
		if parts := array(message["content"]); parts != nil {
			for _, value := range parts {
				part := object(value)
				if part["type"] == "text" || part["type"] == "output_text" {
					texts = append(texts, str(part["text"]))
				}
				if part["type"] == "refusal" {
					finish = "refusal"
				}
			}
			message["content"] = strings.Join(texts, "\n")
		}
	case "anthropic":
		finish = str(envelope["stop_reason"])
		blocks := array(envelope["content"])
		message["_provider_content"] = blocks
		for _, raw := range blocks {
			block := object(raw)
			switch block["type"] {
			case "thinking":
				meta["has_reasoning"] = true
				if text, ok := block["thinking"].(string); ok && text != "" {
					if reasoning != "" {
						reasoning += "\n\n"
					}
					reasoning += text
				}
			case "redacted_thinking":
				meta["has_reasoning"] = true
			case "text":
				texts = append(texts, str(block["text"]))
			case "tool_use":
				calls = append(calls, Doc{"id": block["id"], "type": "function", "function": Doc{"name": block["name"], "arguments": dump(block["input"])}})
			}
		}
		message["content"], message["tool_calls"] = strings.Join(texts, "\n"), calls

	default:
		return nil, fmt.Errorf("不支持的模型协议")
	}
	// Store only whitelisted diagnostics. Never echo provider messages, unknown
	// finish strings, reasoning, signatures or raw response bodies into errors.
	switch finish {
	case "stop", "end_turn", "tool_calls", "tool_use", "stop_sequence", "length", "max_tokens", "content_filter", "refusal", "insufficient_system_resource", "aborted", "model_context_window_exceeded", "pause_turn":
		meta["finish_reason"] = finish
	case "":
	default:
		meta["finish_reason"] = "unknown"
	}
	usage := object(envelope["usage"])
	for _, key := range []string{"prompt_tokens", "completion_tokens", "input_tokens", "output_tokens"} {
		if n, ok := number(usage[key]); ok && n >= 0 {
			meta[key] = n
		}
	}
	meta["text_characters"] = len([]rune(str(message["content"])))
	meta["tool_calls"] = len(array(message["tool_calls"]))
	responseError := func(code string, retryable bool, detail string) (Doc, error) {
		meta["code"] = code
		return nil, &generationResponseError{code: code, retryable: retryable, detail: detail, diagnostic: meta, reasoning: reasoning}
	}
	switch finish {
	case "content_filter", "refusal":
		return responseError("refusal", false, "模型服务拒绝了本次回复；未自动重试，请检查请求内容与供应商策略")
	case "length", "max_tokens":
		return responseError("truncated", true, "模型输出达到 token 上限，回复或工具参数被截断")
	case "model_context_window_exceeded":
		return responseError("context_limit", false, "模型上下文已满，请缩小任务或新建对话")
	case "insufficient_system_resource", "aborted":
		return responseError("interrupted", true, "模型服务中断了本次回复")
	}
	if strings.TrimSpace(str(message["content"])) == "" && len(array(message["tool_calls"])) == 0 {
		if boolean(meta["has_reasoning"]) {
			return responseError("reasoning_only", true, "模型仅返回思考数据，没有可展示的正文或工具调用")
		}
		return responseError("empty_response", true, "模型返回空回复，没有可展示的正文或工具调用")
	}
	meta["code"] = "ok"
	message["_response_meta"] = meta
	message["_display_reasoning"] = reasoning
	return message, nil
}

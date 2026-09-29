package lab

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"
)

// Decode SSE into the same complete envelopes as the JSON adapters. Preview
// deltas are display-only: no partial tool arguments enter the agent loop.
func generationStreamResponse(protocol string, reader io.Reader, started time.Time, progress func(Doc)) (Doc, error) {
	limited := &io.LimitedReader{R: reader, N: maxGenerationJSON + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), maxGenerationJSON+1)
	message := Doc{"role": "assistant", "content": ""}
	envelope := Doc{"usage": Doc{}}
	var calls, blocks []any
	inputs := map[int]string{}
	closed := map[int]bool{}
	var text, reasoning, finish string
	var first time.Duration
	var last time.Time
	done, gotFirst := false, false
	bad := func(code, detail string) error {
		return &generationResponseError{code: code, retryable: code == "stream_interrupted" || code == "upstream_unavailable", detail: detail, diagnostic: Doc{"code": code, "elapsed_ms": time.Since(started).Milliseconds(), "streamed": true}}
	}
	preview := func() {
		names := []any{}
		for _, c := range calls {
			if n := str(object(object(c)["function"])["name"]); n != "" {
				names = append(names, boundedText(n, 100))
			}
		}
		for _, b := range blocks {
			if n := str(object(b)["name"]); n != "" {
				names = append(names, boundedText(n, 100))
			}
		}
		if text == "" && reasoning == "" && len(names) == 0 {
			return
		}
		if !gotFirst {
			first, gotFirst = time.Since(started), true
		}
		if progress == nil || (!last.IsZero() && time.Since(last) < 100*time.Millisecond) {
			return
		}
		last = time.Now()
		progress(Doc{"text": boundedText(text, 32768), "reasoning": boundedText(reasoning, 32768), "tools": names, "first_content_ms": first.Milliseconds(), "elapsed_ms": time.Since(started).Milliseconds()})
	}
	consume := func(raw string) error {
		if raw == "[DONE]" {
			if protocol == "anthropic" || finish == "" {
				return bad("stream_interrupted", "模型流式响应未完整结束，未执行本轮工具")
			}
			done = true
			return nil
		}
		var event Doc
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		if !json.Valid([]byte(raw)) || decoder.Decode(&event) != nil || event == nil {
			return bad("invalid_stream", "模型流式响应格式不正确，未执行本轮工具")
		}
		if event["error"] != nil || event["type"] == "error" {
			return bad("upstream_unavailable", "模型流式响应被服务中断，未执行本轮工具")
		}
		if protocol != "anthropic" {
			if event["usage"] != nil {
				envelope["usage"] = event["usage"]
			}
			for _, rawChoice := range array(event["choices"]) {
				choice := object(rawChoice)
				if integer(choice["index"]) != 0 {
					continue
				}
				delta := object(choice["delta"])
				if s, ok := delta["content"].(string); ok {
					text += s
				}
				for _, key := range []string{"reasoning_content", "reasoning", "refusal"} {
					if s, ok := delta[key].(string); ok {
						message[key] = str(message[key]) + s
					}
				}
				reasoning = str(message["reasoning_content"])
				if reasoning == "" {
					reasoning = str(message["reasoning"])
				}
				for _, rawCall := range array(delta["tool_calls"]) {
					part := object(rawCall)
					i := integer(part["index"])
					if i < 0 || i >= 128 {
						return bad("invalid_stream", "模型流式工具编号无效")
					}
					for len(calls) <= i {
						calls = append(calls, Doc{"id": "", "type": "function", "function": Doc{"name": "", "arguments": ""}})
					}
					call := object(calls[i])
					fn := object(call["function"])
					call["id"] = str(call["id"]) + str(part["id"])
					for _, key := range []string{"name", "arguments"} {
						fn[key] = str(fn[key]) + str(object(part["function"])[key])
					}
				}
				if s := str(choice["finish_reason"]); s != "" {
					finish = s
				}
			}
		} else {
			switch str(event["type"]) {
			case "message_start":
				envelope["usage"] = object(event["message"])["usage"]
			case "content_block_start":
				i := integer(event["index"])
				block := object(event["content_block"])
				if i != len(blocks) || i >= 128 || block == nil {
					return bad("invalid_stream", "模型流式内容编号无效")
				}
				blocks = append(blocks, block)
				if block["type"] == "text" {
					text += str(block["text"])
				}
				if block["type"] == "thinking" {
					reasoning += str(block["thinking"])
				}
			case "content_block_delta", "content_block_stop":
				i := integer(event["index"])
				if i < 0 || i >= len(blocks) || closed[i] {
					return bad("invalid_stream", "模型流式内容顺序无效")
				}
				block := object(blocks[i])
				delta := object(event["delta"])
				if event["type"] == "content_block_stop" {
					closed[i] = true
					if raw, ok := inputs[i]; ok {
						var input Doc
						if json.Unmarshal([]byte(raw), &input) != nil || input == nil {
							return bad("invalid_stream", "模型工具参数不完整，未执行本轮工具")
						}
						block["input"] = input
					}
					break
				}
				switch delta["type"] {
				case "text_delta":
					block["text"] = str(block["text"]) + str(delta["text"])
					text += str(delta["text"])
				case "thinking_delta":
					block["thinking"] = str(block["thinking"]) + str(delta["thinking"])
					reasoning += str(delta["thinking"])
				case "signature_delta":
					block["signature"] = str(block["signature"]) + str(delta["signature"])
				case "input_json_delta":
					inputs[i] += str(delta["partial_json"])
				}
			case "message_delta":
				finish = str(object(event["delta"])["stop_reason"])
				usage := object(envelope["usage"])
				if usage == nil {
					usage = Doc{}
				}
				for k, v := range object(event["usage"]) {
					usage[k] = v
				}
				envelope["usage"] = usage
			case "message_stop":
				if finish == "" || len(closed) != len(blocks) {
					return bad("stream_interrupted", "模型流式响应未完整结束，未执行本轮工具")
				}
				done = true
			}
		}
		preview()
		return nil
	}
	var data []string
	for scanner.Scan() {
		if limited.N <= 0 {
			return nil, bad("response_too_large", "模型响应超过 12 MiB 限制")
		}
		line := scanner.Text()
		if line == "" {
			if len(data) > 0 {
				if err := consume(strings.Join(data, "\n")); err != nil {
					return nil, err
				}
				data = nil
			}
			if done {
				break
			}
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if limited.N <= 0 {
		return nil, bad("response_too_large", "模型响应超过 12 MiB 限制")
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !done && len(data) > 0 {
		if err := consume(strings.Join(data, "\n")); err != nil {
			return nil, err
		}
	}
	if !done {
		return nil, bad("stream_interrupted", "模型流式连接提前结束，未执行本轮工具")
	}
	if protocol == "anthropic" {
		envelope["content"], envelope["stop_reason"] = blocks, finish
	} else {
		message["content"], message["tool_calls"] = text, calls
		envelope["choices"] = []any{Doc{"message": message, "finish_reason": finish}}
	}
	result, err := generationProviderResponse(protocol, jsonBytes(envelope))
	if err == nil {
		meta := object(result["_response_meta"])
		meta["streamed"], meta["elapsed_ms"] = true, time.Since(started).Milliseconds()
		if gotFirst {
			meta["first_content_ms"] = first.Milliseconds()
		}
	}
	return result, err
}

func (m *generationManager) setLiveResponse(id string, value Doc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if value == nil {
		delete(m.live, id)
		return
	}
	if m.live == nil {
		m.live = map[string]Doc{}
	}
	m.live[id] = value
}

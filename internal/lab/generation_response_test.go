package lab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGenerationResponseClassification(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, raw, code, text string
		retry                           bool
	}{
		{"text", "openai", `{"choices":[{"message":{"content":" hello "},"finish_reason":"stop"}]}`, "", " hello ", false},
		{"parts", "openai", `{"choices":[{"message":{"content":[{"type":"text","text":"你好"},{"type":"output_text","text":"世界"},{"type":"reasoning","text":"PRIVATE"}]}}]}`, "", "你好\n世界", false},
		{"empty", "openai", `{"choices":[{"message":{"content":null},"finish_reason":"stop"}]}`, "empty_response", "", true},
		{"blank", "openai", `{"choices":[{"message":{"content":"  \n "}}]}`, "empty_response", "", true},
		{"thinking", "openai", `{"choices":[{"message":{"content":null,"reasoning_content":"PRIVATE"},"finish_reason":"stop"}]}`, "reasoning_only", "", true},
		{"length", "openai", `{"choices":[{"message":{"content":null,"reasoning_content":"PRIVATE","tool_calls":[{"id":"never","function":{"name":"delete_file","arguments":"{}"}}]},"finish_reason":"length"}],"usage":{"completion_tokens":12000}}`, "truncated", "", true},
		{"partial-text", "openai", `{"choices":[{"message":{"content":"incomplete"},"finish_reason":"length"}]}`, "truncated", "", true},
		{"overloaded", "openai", `{"choices":[{"message":{"content":"partial"},"finish_reason":"insufficient_system_resource"}]}`, "interrupted", "", true},
		{"filter", "openai", `{"choices":[{"message":{"content":null},"finish_reason":"content_filter"}]}`, "refusal", "", false},
		{"refusal", "openai", `{"choices":[{"message":{"refusal":"PRIVATE","content":null},"finish_reason":"stop"}]}`, "refusal", "", false},
		{"native-empty", "anthropic", `{"content":[],"stop_reason":"end_turn"}`, "empty_response", "", true},
		{"native-thinking", "anthropic", `{"content":[{"type":"thinking","thinking":"PRIVATE","signature":"PRIVATE"}],"stop_reason":"end_turn"}`, "reasoning_only", "", true},
		{"native-length", "anthropic", `{"content":[{"type":"tool_use","name":"set_site","input":{}}],"stop_reason":"max_tokens"}`, "truncated", "", true},
		{"native-refusal", "anthropic", `{"content":[],"stop_reason":"refusal"}`, "refusal", "", false},
		{"native-context", "anthropic", `{"content":[],"stop_reason":"model_context_window_exceeded"}`, "context_limit", "", false},
		{"unknown-finish", "openai", `{"choices":[{"message":{"reasoning_content":"PRIVATE"},"finish_reason":"PRIVATE"}],"usage":{"completion_tokens":"PRIVATE"}}`, "reasoning_only", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := generationProviderResponse(tc.protocol, []byte(tc.raw))
			if tc.code == "" {
				if err != nil || msg["content"] != tc.text {
					t.Fatalf("normalization failed: %v %s", err, dump(msg))
				}
				return
			}
			var responseErr *generationResponseError
			if !errors.As(err, &responseErr) || responseErr.code != tc.code || responseErr.retryable != tc.retry {
				t.Fatalf("wrong classification: %#v", err)
			}
			if strings.Contains(responseErr.Error()+dump(responseErr.diagnostic), "PRIVATE") {
				t.Fatal("diagnostics leaked provider-only data")
			}
		})
	}
	for _, raw := range []string{`{`, `{}`, `{"choices":[]}`, `{"choices":[{"delta":{"content":"not a non-streaming reply"}}]}`} {
		_, err := generationProviderResponse("openai", []byte(raw))
		var responseErr *generationResponseError
		if err == nil || errors.As(err, &responseErr) {
			t.Fatal("malformed wire format should not be retried as an empty reply")
		}
	}
}

func TestGenerationEmptyReplyAfterToolsRecoversWithoutRepeatingTools(t *testing.T) {
	a := generationTestApp(t)
	var requests atomic.Int32
	var secondRequest atomic.Value
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		body := agentRequestBody(r)
		switch requests.Add(1) {
		case 1:
			agentReply(w, agentToolCall("overview", "read_module", Doc{"module": "overview"}), agentToolCall("site", "read_module", Doc{"module": "site"}), agentToolCall("scenario", "read_module", Doc{"module": "scenario"}), agentToolCall("files", "list_files", Doc{}))
		case 2:
			secondRequest.Store(dump(body))
			json.NewEncoder(w).Encode(Doc{"choices": []Doc{{"message": Doc{"content": nil, "reasoning_content": "DISPLAYABLE_EMPTY_REASONING"}, "finish_reason": "stop"}}, "usage": Doc{"completion_tokens": 9}})
		case 3:
			if dump(body) != secondRequest.Load() {
				t.Error("retry lost or altered the successful tool results")
			}
			agentReply(w, agentToolCall("draft", "replace_draft", Doc{"json": dump(generationDraft())}), agentToolCall("finish", "finish_draft", Doc{"summary": "恢复完成"}))
		default:
			t.Error("unbounded retry")
		}
	})
	before := count(a.store.db, "SELECT COUNT(*) FROM entities")
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "模拟截图中的四次读取后空响应"}, 202)
	done := generationWait(t, a, str(job["id"]))
	if done["status"] != "completed" || requests.Load() != 3 {
		t.Fatalf("recovery failed: %s", dump(done))
	}
	seen := map[string]int{}
	retries := 0
	for _, value := range array(done["events"]) {
		event := object(value)
		if event["stage"] == "tool_call" {
			seen[str(event["tool"])]++
		}
		if event["stage"] == "model_retry" {
			retries++
			if object(event["diagnostic"])["code"] != "reasoning_only" {
				t.Fatal("missing response classification")
			}
		}
	}
	if seen["read_module"] != 3 || seen["list_files"] != 1 || retries != 1 {
		t.Fatal("recovery re-executed completed tools", seen, retries)
	}
	if !strings.Contains(dump(done["events"]), "DISPLAYABLE_EMPTY_REASONING") {
		t.Fatal("provider reasoning-only output missing from separate event")
	}
	if count(a.store.db, "SELECT COUNT(*) FROM entities") != before {
		t.Fatal("retry automatically adopted assets")
	}
}

func TestGenerationReplyRecoveryLimitsAndTruncation(t *testing.T) {
	for _, mode := range []string{"always-empty", "truncated", "refusal", "cancel-backoff"} {
		t.Run(mode, func(t *testing.T) {
			a := generationTestApp(t)
			var requests atomic.Int32
			agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
				body := agentRequestBody(r)
				n := requests.Add(1)
				reason := "stop"
				msg := Doc{"content": nil}
				if mode == "refusal" {
					reason = "content_filter"
				}
				if mode == "truncated" {
					if n == 1 {
						reason = "length"
						msg["tool_calls"] = []Doc{agentToolCall("discard", "write_file", Doc{"path": "wrong.html", "content": "truncated tool must not execute"})}
					} else {
						if integer(body["max_tokens"]) != maxAgentRecoveryTokens {
							t.Error("truncation ignored the configured default output budget")
						}
						generationEnvelope(w, "完整回复")
						return
					}
				}
				json.NewEncoder(w).Encode(Doc{"choices": []Doc{{"message": msg, "finish_reason": reason}}})
			})
			job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "恢复边界"}, 202)
			if mode == "cancel-backoff" {
				deadline := time.Now().Add(time.Second)
				found := false
				for time.Now().Before(deadline) {
					d := a.generation.job(str(job["id"]))
					for _, e := range array(d["events"]) {
						if object(e)["stage"] == "model_retry" {
							found = true
						}
					}
					if found {
						break
					}
					time.Sleep(time.Millisecond)
				}
				if !found {
					t.Fatal("retry not scheduled")
				}
				generationRequest(t, a, "POST", "jobs/"+str(job["id"])+"/cancel", Doc{}, 200)
				a.generation.wg.Wait()
				if requests.Load() != 1 {
					t.Fatal("retry issued after cancel")
				}
				return
			}
			done := generationWait(t, a, str(job["id"]))
			switch mode {
			case "truncated":
				if done["status"] != "completed" || requests.Load() != 2 || object(done["result"])["kind"] != "message" {
					t.Fatal("truncated tool executed or recovery failed", dump(done))
				}
				for _, event := range array(done["events"]) {
					if object(event)["stage"] == "tool_call" {
						t.Fatal("truncated tool was executed")
					}
				}
			case "always-empty":
				if done["status"] != "failed" || requests.Load() != 3 || !strings.Contains(str(done["error"]), "重试上限") {
					t.Fatal("empty retry not bounded", dump(done))
				}
			case "refusal":
				if done["status"] != "failed" || requests.Load() != 1 {
					t.Fatal("refusal retried or treated as success")
				}
			}
		})
	}
}

func TestGenerationRecoveryHonorsDeadline(t *testing.T) {
	a := generationTestApp(t)
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Doc{"choices": []Doc{{"message": Doc{"content": nil}}}})
	})
	a.generation.saveJob(Doc{"id": "deadline", "status": "running"})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	retries := 0
	_, err := a.generation.agentChat(ctx, "deadline", generationSettings(a.store.db, true), []Doc{{"role": "user", "content": "test"}}, 1, &retries, agentTools())
	if err != context.DeadlineExceeded {
		t.Fatalf("deadline ignored: %v", err)
	}
}

func TestGenerationAnthropicEmptyToolTurnRecovery(t *testing.T) {
	a := generationTestApp(t)
	var rounds atomic.Int32
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		body := agentRequestBody(r)
		switch rounds.Add(1) {
		case 1:
			json.NewEncoder(w).Encode(Doc{"content": []Doc{{"type": "thinking", "thinking": "PRIVATE_NATIVE_THINKING", "signature": "PRIVATE_NATIVE_SIGNATURE"}, {"type": "tool_use", "id": "files", "name": "list_files", "input": Doc{}}}, "stop_reason": "tool_use"})
		case 2:
			json.NewEncoder(w).Encode(Doc{"content": []any{}, "stop_reason": "end_turn"})
		case 3:
			messages := array(body["messages"])
			assistant := object(messages[len(messages)-2])
			user := object(messages[len(messages)-1])
			if !strings.Contains(dump(assistant), "PRIVATE_NATIVE_SIGNATURE") {
				t.Error("recovery lost native thinking signature")
			}
			blocks := array(user["content"])
			if len(blocks) != 2 || object(blocks[0])["type"] != "tool_result" || !strings.Contains(str(object(blocks[1])["text"]), "Please continue") {
				t.Error("native empty reply missing continuation after original tool results")
			}
			json.NewEncoder(w).Encode(Doc{"content": []Doc{{"type": "text", "text": "恢复成功"}}, "stop_reason": "end_turn"})
		}
	})
	// agentModel uses a local OpenAI default; retain the same endpoint, select native protocol.
	settings := generationSettings(a.store.db, false)
	settings["protocol"] = "anthropic"
	generationRequest(t, a, "POST", "settings", settings, 200)
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "查看可用文件"}, 202)
	done := generationWait(t, a, str(job["id"]))
	if done["status"] != "completed" || rounds.Load() != 3 {
		t.Fatal("native recovery failed", dump(done))
	}
	if strings.Contains(dump(done), "PRIVATE_NATIVE_SIGNATURE") {
		t.Fatal("provider state leaked into diagnostic history")
	}
}

func TestGenerationResponseRetryBudgetIsSharedAcrossRounds(t *testing.T) {
	a := generationTestApp(t)
	var n atomic.Int32
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		current := n.Add(1)
		if current%2 == 1 {
			json.NewEncoder(w).Encode(Doc{"choices": []Doc{{"message": Doc{"content": nil}}}})
			return
		}
		agentReply(w, agentToolCall("read", "list_files", Doc{}))
	})
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "预算测试"}, 202)
	done := generationWait(t, a, str(job["id"]))
	if done["status"] != "failed" || n.Load() != 5 || !strings.Contains(str(done["error"]), "重试上限") {
		t.Fatal("recovery budget reset between rounds", dump(done))
	}
}

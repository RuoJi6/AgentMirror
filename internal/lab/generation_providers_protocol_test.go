package lab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Protocol references:
// https://platform.claude.com/docs/en/api/messages/create
// https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls
func TestGenerationAnthropicProviderToolRoundTrip(t *testing.T) {
	const key = "ANTHROPIC_TEST_ONLY_KEY"
	const signature = "anthropic-opaque-test-signature"
	plan := Doc{"steps": []Doc{{"step": "生成测试草稿", "status": "in_progress"}}}
	blocks := []Doc{
		{"type": "thinking", "thinking": "Private test reasoning", "signature": signature},
		{"type": "text", "text": "先记录计划。"},
		{"type": "tool_use", "id": "toolu_plan", "name": "update_plan", "input": plan},
		{"type": "tool_use", "id": "toolu_files", "name": "list_files", "input": Doc{}},
	}
	var rounds atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Errorf("wrong Anthropic endpoint: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("x-api-key") != key || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Error("missing Anthropic authentication or version header")
		}
		body := agentRequestBody(r)
		if body["model"] != "claude-test" || integer(body["max_tokens"]) <= 0 {
			t.Error("missing Anthropic model or output budget")
		}
		if !strings.Contains(dump(body["system"]), "controlled honeypot simulator") {
			t.Error("system instruction was not mapped to top-level system")
		}
		for _, value := range array(body["messages"]) {
			if role := str(object(value)["role"]); role != "user" && role != "assistant" {
				t.Errorf("unsupported Anthropic message role %q", role)
			}
		}
		providerAssertToolSchema(t, array(body["tools"]), "input_schema")
		if strings.Contains(dump(body), key) || body["_provider_content"] != nil {
			t.Error("private key or internal metadata included in request body")
		}
		w.Header().Set("Content-Type", "application/json")
		switch rounds.Add(1) {
		case 1:
			_ = json.NewEncoder(w).Encode(Doc{"id": "msg_tools", "type": "message", "role": "assistant", "model": "claude-test", "content": blocks, "stop_reason": "tool_use"})
		case 2:
			messages := array(body["messages"])
			if len(messages) < 3 {
				t.Error("missing Anthropic tool conversation history")
			} else {
				assistant := object(messages[len(messages)-2])
				if assistant["role"] != "assistant" || dump(assistant["content"]) != dump(blocks) {
					t.Error("Anthropic assistant blocks or thinking signature changed during replay")
				}
				result := object(messages[len(messages)-1])
				content := array(result["content"])
				if result["role"] != "user" || len(content) != 2 {
					t.Error("parallel tool results must share one user content block list")
				} else {
					block := object(content[0])
					if block["type"] != "tool_result" || block["tool_use_id"] != "toolu_plan" || !strings.Contains(dump(block["content"]), "ok") {
						t.Error("tool result did not match the executed Anthropic call")
					}
					if files := object(content[1]); files["tool_use_id"] != "toolu_files" || !strings.Contains(str(files["content"]), "files") {
						t.Error("parallel tool result id/order was lost")
					}
				}
			}
			_ = json.NewEncoder(w).Encode(Doc{"id": "msg_draft", "type": "message", "role": "assistant", "model": "claude-test", "content": []Doc{{"type": "tool_use", "id": "toolu_draft", "name": "replace_draft", "input": Doc{"json": dump(generationDraft())}}, {"type": "tool_use", "id": "toolu_finish", "name": "finish_draft", "input": Doc{"summary": "测试草稿已完成。"}}}, "stop_reason": "tool_use"})
		default:
			t.Error("unexpected Anthropic model round")
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer model.Close()
	a := generationTestApp(t)
	provider := generationRequest(t, a, "POST", "providers", Doc{"name": "Anthropic local fixture", "protocol": "anthropic", "base_url": model.URL, "model": "claude-test", "api_key": key}, 200)
	if str(provider["id"]) == "" || strings.Contains(dump(provider), key) {
		t.Fatal("provider creation omitted id or exposed credentials")
	}
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "生成一个文件中心", "provider_id": provider["id"]}, 202)
	done := generationWait(t, a, str(job["id"]))
	if done["status"] != "completed" || rounds.Load() != 2 {
		t.Fatalf("Anthropic tool job failed: %s", dump(done))
	}
	if len(array(done["plan"])) != 1 || object(object(done["result"])["site"]) == nil {
		t.Fatal("Anthropic tool was not executed or final draft was not validated")
	}
	if strings.Contains(dump(done), signature) || strings.Contains(dump(done), key) {
		t.Fatal("provider-only metadata or credentials persisted in the job")
	}
}

func TestGenerationAnthropicToolNumbersPreserved(t *testing.T) {
	message, err := generationProviderResponse("anthropic", []byte(`{"content":[{"type":"tool_use","id":"number","name":"example","input":{"id":9007199254740993}}]}`))
	if err != nil || !strings.Contains(dump(message), "9007199254740993") {
		t.Fatal("native tool argument precision was lost")
	}
}

func providerAssertToolSchema(t *testing.T, declarations []any, schemaField string) {
	t.Helper()
	for _, value := range declarations {
		declaration := object(value)
		if declaration["name"] != "update_plan" {
			continue
		}
		schema := object(declaration[schemaField])
		steps := object(object(schema["properties"])["steps"])
		if schema["type"] != "object" || steps["type"] != "array" || !strings.Contains(dump(schema["required"]), "steps") {
			t.Errorf("invalid %s tool schema", schemaField)
		}
		if str(declaration["description"]) == "" || declaration["function"] != nil {
			t.Error("tool declaration retained an OpenAI wrapper or lost its description")
		}
		return
	}
	t.Errorf("update_plan missing from %s tool declarations", schemaField)
}

func TestGenerationProviderProtocolHTTPErrorDoesNotLeakKey(t *testing.T) {
	for _, protocol := range []string{"openai", "anthropic"} {
		t.Run(protocol, func(t *testing.T) {
			const key = "FAKE_PROVIDER_ECHO_SECRET"
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(Doc{"error": Doc{"message": "Invalid API key " + key}})
			}))
			defer model.Close()
			_, err := generationChat(context.Background(), Doc{"protocol": protocol, "base_url": model.URL, "model": "test-model", "api_key": key}, []Doc{{"role": "user", "content": "test"}}, nil, 64)
			if err == nil || !strings.Contains(err.Error(), "401") {
				t.Fatal("provider 401 did not return a useful HTTP status error")
			}
			if strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "Invalid API key") {
				t.Fatal("provider response body leaked into error")
			}
		})
	}
}

package lab

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentToolTraceRecordsActualInputsAndResults(t *testing.T) {
	a := generationTestApp(t)
	var round atomic.Int32
	content := "<h1>Trace</h1>" + strings.Repeat("界", 9000)
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		switch round.Add(1) {
		case 1:
			agentReply(w, agentToolCall("same", "read_module", Doc{"module": "overview"}))
		case 2:
			bad := agentToolCall("bad-json", "write_file", Doc{})
			object(bad["function"])["arguments"] = "{invalid-json"
			agentReply(w, agentToolCall("same", "read_module", Doc{"module": "unknown"}), bad)
		case 3:
			agentReply(w, agentToolCall("write", "write_file", Doc{"path": "index.html", "content": content}), agentToolCall("finish", "finish_draft", Doc{"summary": "已完成。"}))
		default:
			t.Error("unexpected model call")
		}
	})
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "创建一个测试页面"}, 202)
	job = generationWait(t, a, str(job["id"]))
	if job["status"] != "completed" {
		t.Fatalf("job failed: %v", job["error"])
	}
	starts, ends := map[string]Doc{}, map[string]Doc{}
	for _, value := range array(job["events"]) {
		event := object(value)
		key := str(event["call_id"])
		if key == "" {
			continue
		}
		if event["stage"] == "tool_call" {
			starts[key] = event
		} else {
			ends[key] = event
		}
	}
	if len(starts) != 5 || len(ends) != 5 {
		t.Fatal("missing paired tool traces")
	}
	if starts["1:same"] == nil || starts["2:same"] == nil {
		t.Fatal("provider IDs reused between rounds must remain distinct")
	}
	if ends["2:same"]["stage"] != "tool_error" || !strings.Contains(str(object(ends["2:same"]["output"])["text"]), "未知模块") {
		t.Fatal("error result not captured")
	}
	if str(object(starts["2:bad-json"]["input"])["text"]) != "{invalid-json" || ends["2:bad-json"]["stage"] != "tool_error" {
		t.Fatal("malformed arguments must be visible as failed input")
	}
	payload := object(starts["3:write"]["input"])
	if !boolean(payload["truncated"]) || len([]rune(str(payload["text"]))) != 8193 || integer(payload["characters"]) <= 8192 {
		t.Fatal("UTF-8 tool payload is not bounded and marked")
	}
	if integer(ends["3:write"]["duration_ms"]) < 0 || !strings.Contains(str(object(ends["3:write"]["output"])["text"]), "index.html") {
		t.Fatal("missing real result/duration")
	}
	conversation := generationRequest(t, a, "GET", "conversations/"+str(job["conversation_id"]), nil, 200)
	for _, value := range array(object(array(conversation["turns"])[0])["events"]) {
		event := object(value)
		if event["call_id"] != nil && (event["input"] != nil || event["output"] != nil || !boolean(event["has_payload"])) {
			t.Fatal("history must expose metadata and lazy-load payloads")
		}
	}
	stored := generationRequest(t, a, "GET", "jobs/"+str(job["id"]), nil, 200)
	if !strings.Contains(dump(stored["events"]), "{invalid-json") {
		t.Fatal("full job must retain trace after history read")
	}
}

func TestAgentToolTracePendingAndCancellation(t *testing.T) {
	a := generationTestApp(t)
	entered := make(chan struct{})
	a.generation.cloneWebsite = func(ctx context.Context, _ string, _ func(string, string)) (Doc, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	agentModel(t, a, func(w http.ResponseWriter, _ *http.Request) {
		agentReply(w, agentToolCall("slow", "clone_website", Doc{"url": "https://example.com/slow"}))
	})
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "克隆 https://example.com/slow"}, 202)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("tool did not start")
	}
	current := generationRequest(t, a, "GET", "jobs/"+str(job["id"]), nil, 200)
	seen := false
	for _, value := range array(current["events"]) {
		event := object(value)
		if event["call_id"] == "1:slow" {
			seen = event["stage"] == "tool_call" && object(event["input"]) != nil
		}
	}
	if !seen || current["status"] != "running" {
		t.Fatal("pending input not persisted before tool execution")
	}
	cancelled := generationRequest(t, a, "POST", "jobs/"+str(job["id"])+"/cancel", Doc{}, 200)
	if cancelled["status"] != "cancelled" {
		t.Fatal("job not cancelled")
	}
	for _, value := range array(cancelled["events"]) {
		if object(value)["stage"] == "tool_result" {
			t.Fatal("cancelled tool must not fabricate success")
		}
	}
}

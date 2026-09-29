package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func agentToolCall(id, name string, arguments Doc) Doc {
	return Doc{"id": id, "type": "function", "function": Doc{"name": name, "arguments": dump(arguments)}}
}
func agentReply(w http.ResponseWriter, calls ...Doc) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Doc{"choices": []Doc{{"message": Doc{"role": "assistant", "content": nil, "tool_calls": calls}}}})
}
func agentModel(t *testing.T, a *App, handle http.HandlerFunc) {
	t.Helper()
	s := httptest.NewServer(handle)
	t.Cleanup(s.Close)
	generationRequest(t, a, "POST", "settings", Doc{"base_url": s.URL, "model": "tool-model"}, 200)
}
func agentRequestBody(r *http.Request) Doc {
	var d Doc
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	_ = decoder.Decode(&d)
	return d
}

func TestAgentToolsCloneReadWriteValidateAndContinue(t *testing.T) {
	a := generationTestApp(t)
	var round, cloneCalls atomic.Int32
	a.generation.cloneWebsite = func(ctx context.Context, source string, progress func(string, string)) (Doc, error) {
		cloneCalls.Add(1)
		if source != "https://example.com/portal" {
			t.Errorf("unexpected clone: %s", source)
		}
		progress("fetch", "已读取参考页面")
		return Doc{"site": Doc{"name": "Reference", "entry": "index.html", "files": []Doc{{"path": "index.html", "content": "<h1>Reference</h1>"}, {"path": "assets/icon.png", "encoding": "base64", "content": "aWNvbg=="}}}, "reference": Doc{"title": "Reference"}, "warnings": []any{"静态外观"}}, nil
	}
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		body := agentRequestBody(r)
		if len(array(body["tools"])) < 8 {
			t.Error("missing tools")
		}
		switch round.Add(1) {
		case 1:
			agentReply(w, agentToolCall("plan", "update_plan", Doc{"steps": []Doc{{"step": "克隆并调整页面", "status": "in_progress"}}}), agentToolCall("clone", "clone_website", Doc{"url": "https://example.com/portal"}))
		case 2:
			if !strings.Contains(dump(body), "Reference") {
				t.Error("clone result missing from model context")
			}
			agentReply(w, agentToolCall("read", "read_file", Doc{"path": "index.html"}))
		case 3:
			if !strings.Contains(dump(body), "<h1>Reference</h1>") {
				t.Error("read_file output not returned")
			}
			agentReply(w, agentToolCall("write", "write_file", Doc{"path": "index.html", "content": "<h1>Revision one</h1>\n"}), agentToolCall("scenario", "set_scenario", Doc{"scenario": generationDraft()["scenario"]}), agentToolCall("validate", "validate_draft", Doc{}), agentToolCall("finish", "finish_draft", Doc{"summary": "页面已调整并接入模拟接口。"}))
		case 4:
			if !strings.Contains(dump(body), "继续改标题") || !strings.Contains(dump(body), "页面已调整") {
				t.Error("continuation history missing")
			}
			agentReply(w, agentToolCall("read2", "read_file", Doc{"path": "index.html"}))
		case 5:
			if !strings.Contains(dump(body), "Revision one") {
				t.Error("continuation did not retain prior draft")
			}
			agentReply(w, agentToolCall("write2", "write_file", Doc{"path": "index.html", "content": "<h1>Revision two</h1>"}), agentToolCall("finish2", "finish_draft", Doc{"summary": "标题已修改。"}))
		default:
			t.Error("unexpected model call")
			generationEnvelope(w, "error")
		}
	})
	before := count(a.store.db, "SELECT COUNT(*) FROM entities")
	first := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "请克隆 https://example.com/portal 并接入模拟响应"}, 202)
	first = generationWait(t, a, str(first["id"]))
	if first["status"] != "completed" {
		t.Fatalf("first: %s", dump(first))
	}
	if cloneCalls.Load() != 1 || round.Load() != 3 {
		t.Fatal("tools were not actually invoked")
	}
	if count(a.store.db, "SELECT COUNT(*) FROM entities") != before {
		t.Fatal("Agent automatically saved or published")
	}
	files := array(object(object(first["result"])["site"])["files"])
	if len(files) != 2 || !strings.HasSuffix(str(object(files[0])["content"]), "\n") {
		t.Fatal("file edit lost binary asset or trailing newline")
	}
	for i, event := range array(first["events"]) {
		if integer(object(event)["id"]) != i+1 {
			t.Fatal("event sequence not durable and ordered")
		}
	}
	second := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "继续改标题", "parent_job_id": first["id"]}, 202)
	second = generationWait(t, a, str(second["id"]))
	if second["status"] != "completed" || !strings.Contains(dump(second["result"]), "Revision two") || cloneCalls.Load() != 1 {
		t.Fatalf("continuation: %s", dump(second))
	}
	list := generationRequest(t, a, "GET", "jobs", nil, 200)
	if len(array(list["items"])) != 2 || strings.Contains(dump(list), "Revision two") {
		t.Fatal("job list should return summaries, not file contents")
	}
	if !strings.Contains(dump(a.generation.job(str(first["id"]))["result"]), "Revision one") {
		t.Fatal("continuation mutated its parent")
	}
}

func TestAgentToolErrorsRepairWithoutExpandingAuthority(t *testing.T) {
	a := generationTestApp(t)
	var round, unexpectedClone atomic.Int32
	a.generation.cloneWebsite = func(context.Context, string, func(string, string)) (Doc, error) {
		unexpectedClone.Add(1)
		return nil, fmt.Errorf("should not run")
	}
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		body := agentRequestBody(r)
		switch round.Add(1) {
		case 1:
			agentReply(w, agentToolCall("outside", "clone_website", Doc{"url": "https://unrequested.example/"}), agentToolCall("path", "write_file", Doc{"path": "../../secret.txt", "content": "bad"}), agentToolCall("unknown", "shell", Doc{"command": "whoami"}))
		case 2:
			if !strings.Contains(dump(body), "error") {
				t.Error("tool failures not returned for repair")
			}
			generationDraftReply(w, generationDraft())
		default:
			t.Error("unexpected model round")
		}
	})
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "删除页面中的 https://example.com 链接"}, 202)
	job = generationWait(t, a, str(job["id"]))
	if job["status"] != "completed" || unexpectedClone.Load() != 0 {
		t.Fatalf("authority bypass: %s", dump(job))
	}
	if !strings.Contains(dump(job["events"]), "tool_error") {
		t.Fatal("missing visible error events")
	}
}

func TestCloneWithoutModelAndMaterializeIsAtomicAndIdempotent(t *testing.T) {
	a := generationTestApp(t)
	a.generation.cloneWebsite = func(context.Context, string, func(string, string)) (Doc, error) {
		return Doc{"site": siteFixture(), "warnings": []any{"静态快照"}}, nil
	}
	before := count(a.store.db, "SELECT COUNT(*) FROM entities")
	job := generationRequest(t, a, "POST", "jobs", Doc{"mode": "clone", "source_url": "https://example.com/"}, 202)
	job = generationWait(t, a, str(job["id"]))
	if job["status"] != "completed" || object(job["result"])["scenario"] != nil {
		t.Fatal("clone required model or invented scenario", job)
	}
	if count(a.store.db, "SELECT COUNT(*) FROM entities") != before {
		t.Fatal("clone automatically materialized")
	}
	path := "jobs/" + str(job["id"]) + "/materialize"
	generationRequest(t, a, "POST", path, Doc{"scenario": Doc{"name": "invalid", "rules": []any{}}}, 400)
	if count(a.store.db, "SELECT COUNT(*) FROM entities") != before {
		t.Fatal("invalid materialize partially saved")
	}
	saved := generationRequest(t, a, "POST", path, Doc{}, 200)
	again := generationRequest(t, a, "POST", path, Doc{}, 200)
	if object(saved["site"])["id"] != object(again["site"])["id"] {
		t.Fatal("materialize retry created duplicate")
	}
	changed := clone(object(object(job["result"])["site"]))
	changed["name"] = "Changed"
	generationRequest(t, a, "POST", path, Doc{"site": changed}, 409)
	if len(listing(a.store.db, "sites")) != 1 || len(listing(a.store.db, "sites_versions")) != 1 || len(listing(a.store.db, "workspaces")) != 0 || len(listing(a.store.db, "deployments")) != 0 {
		t.Fatal("materialize saved unexpected entities")
	}
}

func TestAgentExplicitReferenceAndCancellation(t *testing.T) {
	a := generationTestApp(t)
	started := make(chan struct{})
	a.generation.cloneWebsite = func(ctx context.Context, source string, progress func(string, string)) (Doc, error) {
		close(started)
		<-ctx.Done()
		progress("late", "must not be saved")
		return nil, ctx.Err()
	}
	job := generationRequest(t, a, "POST", "jobs", Doc{"mode": "clone", "source_url": "https://example.com/"}, 202)
	<-started
	generationRequest(t, a, "POST", "jobs/"+str(job["id"])+"/cancel", Doc{}, 200)
	a.generation.wg.Wait()
	done := a.generation.job(str(job["id"]))
	if done["status"] != "cancelled" || done["result"] != nil || strings.Contains(dump(done), "must not be saved") {
		t.Fatal("cancelled clone published output", done)
	}
	generationRequest(t, a, "POST", "jobs/"+str(job["id"])+"/materialize", Doc{}, 409)
}

func TestAgentToolRoundLimit(t *testing.T) {
	a := generationTestApp(t)
	var round atomic.Int32
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		agentReply(w, agentToolCall(fmt.Sprint(round.Add(1)), "list_files", Doc{}))
	})
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "keep planning"}, 202)
	done := generationWait(t, a, str(job["id"]))
	if done["status"] != "failed" || round.Load() != maxAgentRounds || !strings.Contains(str(done["error"]), "上限") {
		t.Fatal("Agent loop not bounded", done)
	}
}

func TestAgentReasoningProviderReplayAndDisplay(t *testing.T) {
	a := generationTestApp(t)
	var round atomic.Int32
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		body := agentRequestBody(r)
		for _, value := range array(body["tools"]) {
			parameters := object(object(object(value)["function"])["parameters"])
			if parameters["required"] == nil {
				t.Error("tool JSON Schema required must be an array, never null")
			}
		}
		if round.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(Doc{"choices": []Doc{{"message": Doc{"content": nil, "reasoning_content": "DISPLAYABLE_PROVIDER_REASONING", "tool_calls": []Doc{agentToolCall("list", "list_files", Doc{})}}}}})
			return
		}
		found := false
		for _, raw := range array(body["messages"]) {
			message := object(raw)
			found = found || message["reasoning_content"] == "DISPLAYABLE_PROVIDER_REASONING"
		}
		if !found {
			t.Error("provider tool-state was not replayed")
		}
		generationDraftReply(w, generationDraft())
	})
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "generate"}, 202)
	done := generationWait(t, a, str(job["id"]))
	if done["status"] != "completed" || !strings.Contains(dump(done["events"]), "DISPLAYABLE_PROVIDER_REASONING") {
		t.Fatal("provider reasoning lost during replay or display", done)
	}
}

func TestAgentReferenceURLRetainsUnicodePath(t *testing.T) {
	a := generationTestApp(t)
	input := a.generation.agentInput(Doc{"prompt": "克隆 https://example.com/中文路径 并修改标题"})
	if len(array(input["allowed_urls"])) != 1 || !strings.Contains(str(array(input["allowed_urls"])[0]), "%E4%B8%AD") {
		t.Fatal("unicode URL was truncated", input)
	}
	if input["source_url"] != nil {
		t.Fatal("a URL mention should be interpreted by the Agent, not automatically fetched")
	}
}

// Optional provider compatibility check; credentials stay outside the checkout.
func TestAgentLiveProvider(t *testing.T) {
	filename := os.Getenv("AGENTMIRROR_LIVE_CONFIG")
	if filename == "" {
		t.Skip("AGENTMIRROR_LIVE_CONFIG is not set")
	}
	raw, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal("cannot read private provider config")
	}
	settings, err := parseAgentJSON(string(raw))
	if err != nil {
		t.Fatal("invalid private provider config")
	}
	a := generationTestApp(t)
	generationRequest(t, a, "POST", "settings", settings, 200)
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "请使用工具创建最小的中文配置查看页面：index.html 中有标题与读取配置按钮；按钮 fetch('/api/config') 并显示文本响应。模拟 GET /api/config 返回 application: demo 与 {{prompt}} 槽位。请实际调用 write_file、set_scenario 和 finish_draft，保持页面代码简短。"}, 202)
	deadline := time.Now().Add(185 * time.Second)
	for time.Now().Before(deadline) {
		current := a.generation.job(str(job["id"]))
		if current["status"] == "completed" {
			usedTool := false
			for _, value := range array(current["events"]) {
				event := object(value)
				usedTool = usedTool || event["stage"] == "tool_result"
				if event["diagnostic"] != nil {
					t.Logf("provider response round=%v attempt=%v: %s", event["round"], event["attempt"], dump(event["diagnostic"]))
				}
			}
			if !usedTool {
				t.Fatal("provider returned a document without exercising tool calls")
			}
			t.Logf("provider tool loop passed: %d files, %d events", len(array(object(object(current["result"])["site"])["files"])), len(array(current["events"])))
			return
		}
		if current["status"] != "running" {
			t.Fatalf("live provider: %s", str(current["error"]))
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("live provider test timed out")
}

func TestGenerationURLPreservesBrowserHashRoute(t *testing.T) {
	got := generationURL("https://example.com/console/#/login?redirect=%2Fhome")
	if got != "https://example.com/console/#/login?redirect=%2Fhome" {
		t.Fatalf("hash route was lost: %s", got)
	}
}

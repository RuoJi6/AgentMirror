package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func generationTestApp(t *testing.T) *App {
	t.Helper()
	s, err := openStore(filepath.Join(t.TempDir(), "generation.sqlite3"), "http://localhost:8765")
	if err != nil {
		t.Fatal(err)
	}
	a := &App{store: s}
	a.generationManager()
	t.Cleanup(func() { a.closeGeneration(); s.db.Close() })
	return a
}
func generationRequest(t *testing.T, a *App, method, p string, d Doc, status int) Doc {
	t.Helper()
	var b []byte
	if d != nil {
		b = jsonBytes(d)
	}
	r := httptest.NewRequest(method, "/api/generation/"+p, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	func() {
		defer func() {
			if v := recover(); v != nil {
				if p, ok := v.(problem); ok {
					response(w, r, Doc{"error": p.message}, p.status, "", nil)
				} else {
					panic(v)
				}
			}
		}()
		if !a.generationRoute(w, r) {
			t.Fatal("route missed")
		}
	}()
	if w.Code != status {
		t.Fatalf("%s %s status %d, want %d: %s", method, p, w.Code, status, w.Body.String())
	}
	return decode(w.Body.String())
}
func generationWait(t *testing.T, a *App, id string) Doc {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		d := a.generation.job(id)
		if d["status"] != "running" && d["status"] != "queued" {
			return d
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return nil
}
func generationDraft() Doc {
	return Doc{"site": siteFixture(), "scenario": Doc{"name": "Mock file", "rules": []Doc{{"id": "download", "method": "GET", "path": "/api/download", "conditions": []any{}, "response": Doc{"status": 200, "content_type": "text/plain; charset=utf-8", "format": "text", "body": "File content\n{{prompt}}", "headers": Doc{}}, "delivery_required": true}}}}
}
func generationDraftReply(w http.ResponseWriter, draft Doc) {
	agentReply(w, agentToolCall("draft", "replace_draft", Doc{"json": dump(draft)}), agentToolCall("finish", "finish_draft", Doc{"summary": "测试草稿已完成。"}))
}
func generationEnvelope(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Doc{"choices": []Doc{{"message": Doc{"content": content}}}})
}
func TestGenerationConnectionAllowsReasoningBeforeText(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input Doc
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		_ = decoder.Decode(&input)
		// This provider uses 16 tokens for reasoning before returning a final answer.
		if integer(input["max_tokens"]) <= 16 {
			generationEnvelope(w, "")
			return
		}
		generationEnvelope(w, "OK")
	}))
	defer model.Close()
	a := generationTestApp(t)
	result := generationRequest(t, a, "POST", "test", Doc{"base_url": model.URL, "model": "reasoning-model"}, 200)
	if !boolean(result["ok"]) {
		t.Fatal("connection test did not accept the model's final answer")
	}
}
func TestGenerationDraftContractAndSecretIsolation(t *testing.T) {
	sent := make(chan string, 2)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != "POST" {
			t.Errorf("wrong endpoint: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer TEST_ONLY_KEY" {
			t.Error("missing configured test key")
		}
		raw, _ := io.ReadAll(r.Body)
		sent <- string(raw)
		d := decode(string(raw))
		if d["model"] != "fake-model" || !boolean(d["stream"]) {
			t.Error("invalid completion contract")
		}
		generationDraftReply(w, generationDraft())
	}))
	defer model.Close()
	a := generationTestApp(t)
	settings := generationRequest(t, a, "POST", "settings", Doc{"base_url": model.URL + "/v1", "model": "fake-model", "api_key": "TEST_ONLY_KEY"}, 200)
	if !boolean(settings["has_key"]) || settings["api_key"] != nil || strings.Contains(dump(settings), "TEST_ONLY_KEY") {
		t.Fatal("secret leaked in settings response")
	}
	generationRequest(t, a, "POST", "settings", Doc{"base_url": model.URL + "/v1", "model": "fake-model", "api_key": ""}, 200)
	before := count(a.store.db, "SELECT COUNT(*) FROM entities")
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "生成文件中心", "profile": Doc{"body": "DO_NOT_SEND_PROFILE_BODY"}}, 202)
	done := generationWait(t, a, str(job["id"]))
	if done["status"] != "completed" {
		t.Fatalf("draft failed: %s", dump(done))
	}
	result := object(done["result"])
	if object(result["site"]) == nil || object(result["scenario"]) == nil {
		t.Fatal("missing validated draft")
	}
	if got := <-sent; strings.Contains(got, "DO_NOT_SEND_PROFILE_BODY") || strings.Contains(got, "TEST_ONLY_KEY") {
		t.Fatal("private data copied into model messages")
	}
	if count(a.store.db, "SELECT COUNT(*) FROM entities") != before {
		t.Fatal("draft was automatically persisted as site/scenario")
	}
	if strings.Contains(dump(settings), "TEST_ONLY_KEY") || strings.Contains(dump(generationSettings(a.store.db, false)), "TEST_ONLY_KEY") || strings.Contains(dump(settingsDoc(a.store)), "TEST_ONLY_KEY") || strings.Contains(dump(a.store.export()), "TEST_ONLY_KEY") {
		t.Fatal("secret included in public settings")
	}
	generationRequest(t, a, "POST", "settings", Doc{"base_url": model.URL + "/v1", "model": "fake-model", "clear_key": true}, 200)
	if boolean(generationSettings(a.store.db, false)["has_key"]) {
		t.Fatal("key clearing failed")
	}
}
func settingsDoc(s *Store) Doc { return settings(s.db) }
func TestGenerationFailuresNeverPublishDrafts(t *testing.T) {
	for _, tt := range []struct {
		name, output string
		code         int
	}{{"invalid-json", "not JSON", 200}, {"invalid-site", `{"site":{"name":"broken","files":[]},"scenario":{}}`, 200}, {"invalid-scenario", dump(Doc{"site": siteFixture(), "scenario": Doc{"name": "bad", "rules": []any{}}}), 200}, {"provider-error", "SENSITIVE_PROVIDER_ECHO", 429}} {
		t.Run(tt.name, func(t *testing.T) {
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.code != 200 {
					w.WriteHeader(tt.code)
					_, _ = w.Write([]byte(tt.output))
					return
				}
				agentReply(w, agentToolCall("invalid", "replace_draft", Doc{"json": tt.output}))
			}))
			defer model.Close()
			a := generationTestApp(t)
			generationRequest(t, a, "POST", "settings", Doc{"base_url": model.URL, "model": "local"}, 200)
			before := count(a.store.db, "SELECT COUNT(*) FROM entities")
			job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "generate"}, 202)
			done := generationWait(t, a, str(job["id"]))
			if done["status"] != "failed" || done["result"] != nil {
				t.Fatalf("invalid output accepted: %s", dump(done))
			}
			if strings.Contains(dump(done), "SENSITIVE_PROVIDER_ECHO") {
				t.Fatal("provider error body leaked")
			}
			if count(a.store.db, "SELECT COUNT(*) FROM entities") != before {
				t.Fatal("invalid draft stored as entity")
			}
		})
	}
}
func TestGenerationCancellationTimeoutAndNoKey(t *testing.T) {
	started := make(chan struct{}, 4)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected API key for local model")
		}
		_, _ = io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer model.Close()
	a := generationTestApp(t)
	generationRequest(t, a, "POST", "settings", Doc{"base_url": model.URL, "model": "local"}, 200)
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "generate"}, 202)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("model not called")
	}
	d := generationRequest(t, a, "POST", "jobs/"+str(job["id"])+"/cancel", Doc{}, 200)
	if d["status"] != "cancelled" {
		t.Fatal("cancel did not update state")
	}
	a.generation.wg.Wait()
	if a.generation.job(str(job["id"]))["status"] != "cancelled" {
		t.Fatal("worker overwrote cancellation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err := generationCompletion(ctx, Doc{"base_url": model.URL, "model": "local"}, []Doc{{"role": "user", "content": "hello"}}, 16)
	if err == nil || ctx.Err() != context.DeadlineExceeded {
		t.Fatal("timeout not respected")
	}
	id := "timeout-fixture"
	a.generation.saveJob(Doc{"id": id, "status": "running"})
	a.generation.finish(id, nil, "", context.DeadlineExceeded)
	if d := a.generation.job(id); d["status"] != "failed" || !strings.Contains(str(d["error"]), "超时") {
		t.Fatalf("timeout status: %s", dump(d))
	}
}
func TestGenerationRedirectDoesNotForwardKey(t *testing.T) {
	reached := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	_, err := generationCompletion(context.Background(), Doc{"base_url": source.URL, "model": "test", "api_key": "TEST_KEY"}, []Doc{{"role": "user", "content": "test"}}, 16)
	if err == nil || reached {
		t.Fatal("redirect followed with provider credentials")
	}
}

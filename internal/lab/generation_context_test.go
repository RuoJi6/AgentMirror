package lab

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestContextCompactionRetainsRequestPairAndRawAudit(t *testing.T) {
	app := generationTestApp(t)
	m := app.generation
	m.saveJob(Doc{"id": "compacttest", "status": "running", "events": []any{}})
	latest := Doc{"role": "assistant", "tool_calls": []any{agentToolCall("last", "read_file", Doc{"path": "index.html"})}, "_provider_content": []any{Doc{"type": "thinking", "signature": "opaque-signature"}}}
	messages := []Doc{{"role": "system", "content": "instructions"}, {"role": "user", "content": strings.Repeat("历史输入", 20000)}, {"role": "assistant", "content": "Already saved site"}, {"role": "user", "content": "Exact current request\nkeep scope"}, {"role": "assistant", "content": strings.Repeat("OLD LARGE RESULT", 20000)}, latest, {"role": "tool", "tool_call_id": "last", "content": "newest"}}
	raw := dump(messages)
	result, current, err := m.prepareAgentContext("compacttest", nil, messages, 3, Doc{"saved_actions": []any{Doc{"id": "site", "action": "saved"}}}, nil)
	if err != nil || current != 2 || dump(result[current]) != dump(messages[3]) || dump(result[len(result)-2]) != dump(latest) || result[len(result)-1]["tool_call_id"] != "last" {
		t.Fatal("lost current request or signed exchange", err)
	}
	if dump(messages) != raw || !strings.Contains(dump(result), "Already saved site") || strings.Contains(dump(result), "OLD LARGE RESULT") {
		t.Fatal("audit modified or stale content replayed")
	}
	events := array(m.job("compacttest")["events"])
	if len(events) != 1 || object(events[0])["stage"] != "context_compacted" || integer(object(events[0])["after_estimated_tokens"]) >= integer(object(events[0])["before_estimated_tokens"]) {
		t.Fatal("missing compaction evidence")
	}
	_, _, err = m.prepareAgentContext("compacttest", Doc{"context_window_tokens": 16000, "max_output_tokens": 512}, []Doc{{"role": "system", "content": "rules"}, {"role": "user", "content": strings.Repeat("长", 20000)}}, 1, nil, nil)
	if err == nil {
		t.Fatal("irreducible input silently exceeds budget")
	}
}

func TestConfiguredOutputBudgetAndChunkRecovery(t *testing.T) {
	app := generationTestApp(t)
	var requests atomic.Int32
	agentModel(t, app, func(w http.ResponseWriter, r *http.Request) {
		body := agentRequestBody(r)
		if integer(body["max_tokens"]) != 8000 {
			t.Error("ignored configured output limit")
		}
		if requests.Add(1) == 1 {
			json.NewEncoder(w).Encode(Doc{"choices": []any{Doc{"message": Doc{"content": nil, "tool_calls": []any{agentToolCall("discard", "write_file", Doc{"path": "bad.html", "content": "partial"})}}, "finish_reason": "length"}}})
			return
		}
		if !strings.Contains(dump(body["messages"]), "ONE small complete tool call") || !strings.Contains(dump(body["messages"]), "append_file") {
			t.Error("identical retry without chunk recovery")
		}
		generationEnvelope(w, "Recovered concise reply")
	})
	settings := generationProvider(app.store.db, generationDefaultProvider(app.store.db), true)
	settings["max_output_tokens"] = 8000
	app.generation.saveJob(Doc{"id": "outputbudget", "status": "running", "events": []any{}})
	recoveries := 0
	_, err := app.generation.agentChat(context.Background(), "outputbudget", settings, []Doc{{"role": "user", "content": "Create requested file"}}, 1, &recoveries, nil)
	if err != nil || requests.Load() != 2 || recoveries != 1 {
		t.Fatal("recovery failed", err)
	}
	for _, raw := range array(app.generation.job("outputbudget")["events"]) {
		if object(raw)["stage"] == "tool_call" {
			t.Fatal("truncated tool executed")
		}
	}
}

func TestGenerationTokenLimitsPersistAndValidate(t *testing.T) {
	a := generationTestApp(t)
	p := generationRequest(t, a, "POST", "providers", Doc{"name": "Budget", "model": "fixture", "base_url": "https://example.com", "context_window_tokens": 64000, "max_output_tokens": 16000}, 200)
	id := str(p["id"])
	for _, fields := range []Doc{{"context_window_tokens": 15000}, {"max_output_tokens": 131073}, {"max_output_tokens": 0}, {"max_output_tokens": 1.5}, {"max_output_tokens": 60000}} {
		fields["id"] = id
		generationRequest(t, a, "POST", "providers", fields, 400)
	}
	generationRequest(t, a, "POST", "providers", Doc{"id": id, "name": "Renamed"}, 200)
	initGenerationProviders(a.store)
	saved := generationProvider(a.store.db, id, true)
	if integer(saved["context_window_tokens"]) != 64000 || integer(generationProviderAudit(saved)["max_output_tokens"]) != 16000 {
		t.Fatal("limits lost in partial update/restart/snapshot")
	}
	generationRequest(t, a, "DELETE", "providers/"+id, nil, 200)
	if count(a.store.db, "SELECT COUNT(*) FROM generation_provider_limits WHERE provider_id=?", id) != 0 {
		t.Fatal("orphan limits")
	}
}

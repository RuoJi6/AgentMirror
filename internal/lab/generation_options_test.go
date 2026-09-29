package lab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGenerationConfiguredRequestDeadlineIsApplied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = agentRequestBody(r)
		<-r.Context().Done()
	}))
	defer server.Close()
	// Direct request-unit fixture uses one second to avoid a slow boundary test;
	// the public settings API separately enforces the ten-second minimum.
	settings := Doc{"base_url": server.URL, "model": "test", "request_timeout_seconds": 1}
	_, err := generationChat(context.Background(), settings, []Doc{{"role": "user", "content": "test"}}, nil, 100)
	var typed *generationResponseError
	if !errors.As(err, &typed) || typed.code != "request_timeout" || integer(typed.diagnostic["timeout_ms"]) != 1000 {
		t.Fatalf("saved request timeout was not applied: %v", err)
	}
	a := generationTestApp(t)
	a.generation.saveJob(Doc{"id": "deadlineoptions", "status": "running", "provider": Doc{"job_timeout_seconds": 1200}})
	a.generation.finish("deadlineoptions", nil, "", context.DeadlineExceeded)
	if !strings.Contains(str(a.generation.job("deadlineoptions")["error"]), "1200 秒") {
		t.Fatal("task timeout diagnosis did not use the job's saved settings")
	}
}

func TestGenerationProviderTimeoutOptions(t *testing.T) {
	a := generationTestApp(t)
	provider := generationRequest(t, a, "POST", "providers", Doc{"name": "Timeouts", "model": "test", "base_url": "https://example.com"}, 200)
	id := str(provider["id"])
	if integer(provider["request_timeout_seconds"]) != 180 || integer(provider["job_timeout_seconds"]) != 600 {
		t.Fatal("missing compatible defaults", provider)
	}
	for _, invalid := range []Doc{
		{"request_timeout_seconds": 9}, {"request_timeout_seconds": 3601}, {"request_timeout_seconds": 1.5},
		{"job_timeout_seconds": 29}, {"job_timeout_seconds": 7201}, {"request_timeout_seconds": "300"},
		{"request_timeout_seconds": 700, "job_timeout_seconds": 600}, {"job_timeout_seconds": nil},
	} {
		invalid["id"] = id
		generationRequest(t, a, "POST", "providers", invalid, 400)
	}
	generationRequest(t, a, "POST", "providers", Doc{"id": id, "request_timeout_seconds": 300, "job_timeout_seconds": 1800}, 200)
	// Partial/legacy edits and startup migration must preserve saved values.
	generationRequest(t, a, "POST", "settings", Doc{"name": "Renamed"}, 200)
	initGenerationProviders(a.store)
	settings := generationProvider(a.store.db, id, true)
	if generationTimeout(settings, "request_timeout_seconds") != 300*time.Second || generationTimeout(settings, "job_timeout_seconds") != 1800*time.Second {
		t.Fatal("saved options lost", settings)
	}
	generationRequest(t, a, "DELETE", "providers/"+id, nil, 200)
	if count(a.store.db, "SELECT COUNT(*) FROM generation_provider_options WHERE provider_id=?", id) != 0 {
		t.Fatal("orphan provider options")
	}
}

func TestGenerationReasoningBoundedAndLazyHistory(t *testing.T) {
	a := generationTestApp(t)
	a.generation.saveJob(Doc{"id": "reasoningtest", "conversation_id": "reasoningtest", "status": "running", "prompt": "test", "events": []any{}})
	text := strings.Repeat("思", 33000)
	a.generation.recordModelReasoning("reasoningtest", text, 1, 1)
	a.generation.recordModelReasoning("reasoningtest", "  ", 1, 2)
	job := a.generation.job("reasoningtest")
	events := array(job["events"])
	if len(events) != 1 {
		t.Fatal("empty reasoning produced event")
	}
	payload := object(object(events[0])["reasoning"])
	if len([]rune(str(payload["text"]))) != 32769 || !boolean(payload["truncated"]) || integer(payload["characters"]) != 33000 {
		t.Fatal("unbounded or corrupt reasoning")
	}
	history := a.generation.conversation("reasoningtest")
	event := object(array(object(array(history["turns"])[0])["events"])[0])
	if event["reasoning"] != nil || !boolean(event["has_payload"]) || len([]rune(str(event["reasoning_preview"]))) > 141 {
		t.Fatal("history did not defer full reasoning payload")
	}
}

func TestGenerationOnlyPlaintextReasoningIsDisplayable(t *testing.T) {
	for _, tc := range []struct{ protocol, raw, want string }{
		{"openai", `{"choices":[{"message":{"content":"ok","reasoning_content":"Think"}}]}`, "Think"},
		{"openai", `{"choices":[{"message":{"content":"ok","reasoning":"Think"}}]}`, "Think"},
		{"openai", `{"choices":[{"message":{"content":"ok","reasoning":{"encrypted":"SECRET"}}}]}`, ""},
		{"anthropic", `{"content":[{"type":"thinking","thinking":"Think","signature":"SECRET"},{"type":"redacted_thinking","data":"SECRET"},{"type":"text","text":"ok"}]}`, "Think"},
		{"anthropic", `{"content":[{"type":"redacted_thinking","data":"SECRET"},{"type":"text","text":"ok"}]}`, ""},
	} {
		message, err := generationProviderResponse(tc.protocol, []byte(tc.raw))
		if err != nil || message["_display_reasoning"] != tc.want {
			t.Fatal("unsafe reasoning normalization", tc.protocol, err)
		}
	}
}

package lab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGenerationProviderMigrationPreservesLegacyAndDeletion(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "legacy.sqlite3"), "http://localhost:8765")
	if err != nil {
		t.Fatal(err)
	}
	exec(s.db, `CREATE TABLE generation_secrets(id INTEGER PRIMARY KEY, base_url TEXT NOT NULL, model TEXT NOT NULL, api_key TEXT NOT NULL)`)
	exec(s.db, "INSERT INTO generation_secrets VALUES(1,?,?,?)", "https://api.deepseek.com", "deepseek-chat", "LEGACY_TEST_ONLY_KEY")
	a := &App{store: s}
	a.generationManager()
	t.Cleanup(func() { a.closeGeneration(); s.db.Close() })
	list := generationRequest(t, a, "GET", "providers", nil, 200)
	items := array(list["items"])
	if len(items) != 1 {
		t.Fatalf("legacy migration created %d providers", len(items))
	}
	provider := object(items[0])
	id := str(provider["id"])
	if list["default_provider_id"] != id || provider["protocol"] != "openai" || provider["model"] != "deepseek-chat" || provider["base_url"] != "https://api.deepseek.com" || !boolean(provider["has_key"]) {
		t.Fatal("legacy configuration changed during migration")
	}
	if generationProvider(s.db, id, true)["api_key"] != "LEGACY_TEST_ONLY_KEY" || strings.Contains(dump(list), "LEGACY_TEST_ONLY_KEY") {
		t.Fatal("legacy key lost or exposed")
	}
	initGenerationProviders(s)
	if count(s.db, "SELECT COUNT(*) FROM generation_providers") != 1 {
		t.Fatal("migration was not idempotent")
	}
	generationRequest(t, a, "DELETE", "providers/"+id, nil, 200)
	initGenerationProviders(s)
	if count(s.db, "SELECT COUNT(*) FROM generation_providers") != 0 || generationDefaultProvider(s.db) != "" || boolean(generationSettings(s.db, false)["has_key"]) {
		t.Fatal("deleted legacy configuration was resurrected")
	}
}

func TestGenerationProvidersKeepKeysIndependentAndSettingsCompatible(t *testing.T) {
	a := generationTestApp(t)
	first := generationRequest(t, a, "POST", "providers", Doc{"name": "First", "protocol": "openai", "base_url": "https://first.example/v1", "model": "first-model", "api_key": "FIRST_TEST_KEY"}, 200)
	second := generationRequest(t, a, "POST", "providers", Doc{"name": "Second", "protocol": "anthropic", "base_url": "https://second.example", "model": "second-model", "api_key": "SECOND_TEST_KEY"}, 200)
	firstID, secondID := str(first["id"]), str(second["id"])
	if second["default_provider_id"] != firstID {
		t.Fatal("creating another provider silently changed the default")
	}
	generationRequest(t, a, "POST", "providers", Doc{"id": secondID, "api_key": "", "set_default": true}, 200)
	if generationProvider(a.store.db, secondID, true)["api_key"] != "SECOND_TEST_KEY" || generationProvider(a.store.db, firstID, true)["api_key"] != "FIRST_TEST_KEY" {
		t.Fatal("blank-key update changed or copied another provider's key")
	}
	settings := generationRequest(t, a, "GET", "settings", nil, 200)
	if settings["id"] != secondID || settings["protocol"] != "anthropic" {
		t.Fatal("legacy settings did not follow the selected default")
	}
	generationRequest(t, a, "POST", "settings", Doc{"model": "updated-model", "api_key": ""}, 200)
	if generationProvider(a.store.db, secondID, true)["model"] != "updated-model" || generationProvider(a.store.db, secondID, true)["api_key"] != "SECOND_TEST_KEY" {
		t.Fatal("legacy update did not preserve the default provider's key")
	}
	generationRequest(t, a, "POST", "providers", Doc{"id": firstID, "clear_key": true}, 200)
	if boolean(generationProvider(a.store.db, firstID, false)["has_key"]) || !boolean(generationProvider(a.store.db, secondID, false)["has_key"]) {
		t.Fatal("clear_key affected the wrong provider")
	}
	public := dump(generationRequest(t, a, "GET", "providers", nil, 200)) + dump(settings) + dump(a.store.export())
	if strings.Contains(public, "FIRST_TEST_KEY") || strings.Contains(public, "SECOND_TEST_KEY") || strings.Contains(dump(first), "api_key") {
		t.Fatal("provider credentials exposed in API or business export")
	}
	deleted := generationRequest(t, a, "DELETE", "providers/"+secondID, nil, 200)
	if deleted["default_provider_id"] != firstID {
		t.Fatal("deleting default did not select the remaining provider")
	}
	generationRequest(t, a, "POST", "providers", Doc{"id": secondID, "api_key": ""}, 404)
	generationRequest(t, a, "POST", "jobs", Doc{"provider_id": secondID, "prompt": "test"}, 404)
	for _, raw := range []Doc{
		{"name": "Bad", "protocol": "unsupported", "base_url": "https://example.com", "model": "x"},
		{"name": "Bad", "protocol": "openai", "base_url": "https://user:secret@example.com", "model": "x"},
		{"name": "Bad", "protocol": "openai", "base_url": "https://example.com?key=secret", "model": "x"},
	} {
		generationRequest(t, a, "POST", "providers", raw, 400)
	}
}

func TestGenerationJobProviderSnapshotSurvivesConfigChanges(t *testing.T) {
	a := generationTestApp(t)
	started, proceed := make(chan struct{}), make(chan struct{})
	var rounds, wrongProviderCalls atomic.Int32
	wrong := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrongProviderCalls.Add(1)
		generationDraftReply(w, generationDraft())
	}))
	defer wrong.Close()
	selected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := agentRequestBody(r)
		if r.Header.Get("Authorization") != "Bearer SNAPSHOT_TEST_KEY" || body["model"] != "snapshot-model" {
			t.Error("running job used updated provider credentials or model")
		}
		if rounds.Add(1) == 1 {
			close(started)
			<-proceed
			agentReply(w, agentToolCall("files", "list_files", Doc{}))
			return
		}
		generationDraftReply(w, generationDraft())
	}))
	defer selected.Close()
	generationRequest(t, a, "POST", "providers", Doc{"name": "Default", "protocol": "openai", "base_url": wrong.URL, "model": "default-model"}, 200)
	provider := generationRequest(t, a, "POST", "providers", Doc{"name": "Selected", "protocol": "openai", "base_url": selected.URL, "model": "snapshot-model", "api_key": "SNAPSHOT_TEST_KEY", "request_timeout_seconds": 240, "job_timeout_seconds": 1200}, 200)
	id := str(provider["id"])
	job := generationRequest(t, a, "POST", "jobs", Doc{"provider_id": id, "prompt": "generate"}, 202)
	<-started
	generationRequest(t, a, "POST", "providers", Doc{"id": id, "name": "Changed", "base_url": wrong.URL, "model": "changed-model", "api_key": "CHANGED_TEST_KEY", "request_timeout_seconds": 300, "job_timeout_seconds": 1800}, 200)
	generationRequest(t, a, "DELETE", "providers/"+id, nil, 200)
	close(proceed)
	done := generationWait(t, a, str(job["id"]))
	if done["status"] != "completed" || rounds.Load() != 2 || wrongProviderCalls.Load() != 0 {
		t.Fatal("job switched providers or failed after configuration deletion", done)
	}
	audit := object(done["provider"])
	if done["provider_id"] != id || audit["name"] != "Selected" || audit["model"] != "snapshot-model" || audit["protocol"] != "openai" || len(audit) != 9 || integer(audit["context_window_tokens"]) != 128000 || integer(audit["max_output_tokens"]) != 24000 || integer(audit["request_timeout_seconds"]) != 240 || integer(audit["job_timeout_seconds"]) != 1200 {
		t.Fatal("job audit was changed or contains non-audit fields", audit)
	}
	public := dump(done) + dump(generationRequest(t, a, "GET", "jobs", nil, 200))
	if strings.Contains(public, "SNAPSHOT_TEST_KEY") || strings.Contains(public, "CHANGED_TEST_KEY") || strings.Contains(public, selected.URL) {
		t.Fatal("job persisted credentials or provider endpoint")
	}
}

func TestGenerationProviderConnectionTestUsesSavedProtocol(t *testing.T) {
	for _, protocol := range []string{"openai", "anthropic"} {
		t.Run(protocol, func(t *testing.T) {
			a := generationTestApp(t)
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if protocol == "anthropic" {
					if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "CONNECTION_TEST_KEY" {
						t.Error("test did not use Anthropic protocol")
					}
					_ = json.NewEncoder(w).Encode(Doc{"content": []Doc{{"type": "text", "text": "OK"}}})
				} else {
					if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer CONNECTION_TEST_KEY" {
						t.Error("test did not use OpenAI protocol")
					}
					generationEnvelope(w, "OK")
				}
			}))
			defer model.Close()
			provider := generationRequest(t, a, "POST", "providers", Doc{"name": protocol, "protocol": protocol, "base_url": model.URL, "model": "test", "api_key": "CONNECTION_TEST_KEY"}, 200)
			result := generationRequest(t, a, "POST", "providers/"+str(provider["id"])+"/test", Doc{}, 200)
			if !boolean(result["ok"]) || result["protocol"] != protocol || strings.Contains(dump(result), "CONNECTION_TEST_KEY") {
				t.Fatal("invalid provider test result")
			}
		})
	}
}

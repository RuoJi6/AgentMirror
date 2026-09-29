package lab

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Opt-in verification uses synthetic materials and the caller's private model
// configuration; ordinary test runs never contact an external provider.
func TestGenerationLiveReferences(t *testing.T) {
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
	site, scenario, profile := generationReferenceFixtures(t, a)
	job := generationRequest(t, a, "POST", "jobs", Doc{
		"prompt":     "请实际使用read_reference读取我引用的站点和模拟场景，再用use_reference把二者载入草稿。仅把index.html里的标题改为中文配置中心，保留其余文件及场景规则，不访问任何网址，响应仍保留{{prompt}}。最后调用finish_draft。提示词方案用于采用时绑定，无需其正文。",
		"references": []Doc{selectedReference("site", site), selectedReference("scenario", scenario), selectedReference("profile", profile)},
	}, 202)
	deadline := time.Now().Add(185 * time.Second)
	for time.Now().Before(deadline) {
		current := a.generation.job(str(job["id"]))
		if current["status"] == "completed" {
			events := dump(current["events"])
			if !strings.Contains(events, "read_reference 已完成") || !strings.Contains(events, "use_reference 已完成") {
				t.Fatal("provider did not use reference tools")
			}
			found := false
			for _, value := range array(object(object(current["result"])["site"])["files"]) {
				f := object(value)
				found = found || (f["path"] == "logo.png" && f["content"] == "cHJlc2VydmUtYmluYXJ5")
			}
			if !found {
				t.Fatal("referenced binary asset lost")
			}
			material := generationRequest(t, a, "POST", "jobs/"+str(job["id"])+"/materialize", Doc{}, 200)
			if material["profile_id"] != profile["id"] {
				t.Fatal("wrong selected profile")
			}
			t.Log("live reference reads, draft copying, binary retention and selected profile adoption passed")
			return
		}
		if current["status"] != "running" {
			t.Fatalf("live reference provider: %s", str(current["error"]))
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("live reference provider timed out")
}

func generationReferenceFixtures(t *testing.T, a *App) (Doc, Doc, Doc) {
	t.Helper()
	site := siteFixture()
	site["name"] = "引用站点"
	site["files"] = []Doc{{"path": "index.html", "content": "<h1>ORIGINAL_REFERENCE_TEXT</h1><a href=\"https://unrequested.example/\">reference</a>"}, {"path": "logo.png", "encoding": "base64", "content": "cHJlc2VydmUtYmluYXJ5"}}
	site = a.store.saveComposer("sites", site)
	scenario := a.store.saveComposer("scenarios", object(generationDraft()["scenario"]))
	profile := a.store.save("profiles", Doc{"name": "引用提示词", "category": "unexpected_output", "body": "PRIVATE_REFERENCE_BODY_MUST_NOT_REACH_MODEL", "description": "PRIVATE_REFERENCE_DESCRIPTION"})
	return site, scenario, profile
}

func selectedReference(kind string, doc Doc) Doc {
	return Doc{"kind": kind, "id": doc["id"], "version": doc["version"], "name": "FORGED_CLIENT_NAME", "body": "FORGED_CLIENT_BODY"}
}

func TestGenerationReferencesAgentSnapshotToolsAndAdoption(t *testing.T) {
	a := generationTestApp(t)
	site, scenario, profile := generationReferenceFixtures(t, a)
	refs := []Doc{selectedReference("site", site), selectedReference("scenario", scenario), selectedReference("profile", profile), selectedReference("site", site)}
	var round atomic.Int32
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		body := agentRequestBody(r)
		serialized := dump(body)
		for _, secret := range []string{"PRIVATE_REFERENCE_", "FORGED_CLIENT_", "cHJlc2VydmUtYmluYXJ5"} {
			if strings.Contains(serialized, secret) {
				t.Errorf("reference secret/content leaked to model: %s", secret)
			}
		}
		switch round.Add(1) {
		case 1:
			messages := array(body["messages"])
			contextDoc := decode(str(object(messages[len(messages)-1])["content"]))
			if len(array(contextDoc["references"])) != 3 || len(array(contextDoc["allowed_urls"])) != 0 {
				t.Error("references missing/did not deduplicate, or reference URL expanded authority")
			}
			// The active Agent must keep the original snapshot after this edit.
			revised := clone(site)
			object(array(revised["files"])[0])["content"] = "<h1>NEW_REFERENCE_TEXT</h1>"
			a.store.saveComposer("sites", revised)
			agentReply(w,
				agentToolCall("manifest", "read_reference", Doc{"kind": "site", "id": site["id"]}),
				agentToolCall("text", "read_reference", Doc{"kind": "site", "id": site["id"], "path": "index.html"}),
				agentToolCall("binary", "read_reference", Doc{"kind": "site", "id": site["id"], "path": "logo.png"}),
				agentToolCall("rules", "read_reference", Doc{"kind": "scenario", "id": scenario["id"]}),
				agentToolCall("profile", "read_reference", Doc{"kind": "profile", "id": profile["id"], "path": "body"}),
				agentToolCall("missing", "read_reference", Doc{"kind": "profile", "id": "environment"}),
				agentToolCall("cannot-use-profile", "use_reference", Doc{"kind": "profile", "id": profile["id"]}),
				agentToolCall("cannot-clone-ref-url", "clone_website", Doc{"url": "https://unrequested.example/"}),
			)
		case 2:
			if !strings.Contains(serialized, "ORIGINAL_REFERENCE_TEXT") || strings.Contains(serialized, "NEW_REFERENCE_TEXT") || !strings.Contains(serialized, "Binary asset") || !strings.Contains(serialized, "只能访问本轮用户明确引用的素材") {
				t.Error("reference read, snapshot, binary metadata, or access restriction failed")
			}
			agentReply(w, agentToolCall("use-site", "use_reference", Doc{"kind": "site", "id": site["id"]}), agentToolCall("use-scenario", "use_reference", Doc{"kind": "scenario", "id": scenario["id"]}))
		case 3:
			agentReply(w, agentToolCall("write", "write_file", Doc{"path": "index.html", "content": "<h1>EDITED_DRAFT_ONLY</h1>"}), agentToolCall("finish", "finish_draft", Doc{"summary": "已基于引用素材生成草稿"}))
		default:
			t.Error("unexpected model turn")
			generationEnvelope(w, "error")
		}
	})
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "按所选素材调整", "references": refs}, 202)
	job = generationWait(t, a, str(job["id"]))
	if job["status"] != "completed" {
		t.Fatalf("generation failed: %s", dump(job))
	}
	if len(array(job["references"])) != 3 || strings.Contains(dump(job), "PRIVATE_REFERENCE_") || strings.Contains(dump(job), "FORGED_CLIENT_") {
		t.Fatal("job references leaked contents or did not canonicalize")
	}
	files := array(object(object(job["result"])["site"])["files"])
	if len(files) != 2 || object(files[1])["content"] != "cHJlc2VydmUtYmluYXJ5" {
		t.Fatal("use_reference lost binary asset")
	}
	if strings.Contains(dump(get(a.store.db, "sites", str(site["id"]))), "EDITED_DRAFT_ONLY") {
		t.Fatal("draft tool modified reusable material")
	}
	for _, endpoint := range []string{"jobs", "conversations", "conversations/" + str(job["conversation_id"])} {
		history := generationRequest(t, a, "GET", endpoint, nil, 200)
		if !strings.Contains(dump(history), str(profile["id"])) || strings.Contains(dump(history), "EDITED_DRAFT_ONLY") || strings.Contains(dump(history), "PRIVATE_REFERENCE_") {
			t.Fatal("history missing metadata or exposed bodies", endpoint)
		}
	}
	path := "jobs/" + str(job["id"]) + "/materialize"
	materialized := generationRequest(t, a, "POST", path, Doc{"profile_id": "environment", "references": []Doc{{"kind": "profile", "id": "environment"}}}, 200)
	if materialized["profile_id"] != profile["id"] || object(materialized["profile"])["name"] != profile["name"] || strings.Contains(dump(materialized), "PRIVATE_REFERENCE_") {
		t.Fatal("adoption did not retain exact user-selected profile")
	}
	retry := generationRequest(t, a, "POST", path, Doc{}, 200)
	if object(retry["site"])["id"] != object(materialized["site"])["id"] || retry["profile_id"] != profile["id"] {
		t.Fatal("profile adoption is not idempotent")
	}
	profile["body"] = "UPDATED_PROFILE"
	a.store.save("profiles", profile)
	generationRequest(t, a, "POST", path, Doc{}, 409)
}

func TestGenerationReferencesValidationInheritanceAndClear(t *testing.T) {
	a := generationTestApp(t)
	site, scenario, profile := generationReferenceFixtures(t, a)
	refs := []Doc{selectedReference("site", site), selectedReference("scenario", scenario), selectedReference("profile", profile)}
	var revision atomic.Int32
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		draft := generationDraft()
		// Each requested follow-up makes a real change so it remains adoptable.
		object(draft["site"])["name"] = fmt.Sprintf("Revision %d", revision.Add(1))
		generationDraftReply(w, draft)
	})
	first := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "建立草稿", "references": refs}, 202)
	first = generationWait(t, a, str(first["id"]))
	child := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "继续调整", "parent_job_id": first["id"]}, 202)
	child = generationWait(t, a, str(child["id"]))
	if len(array(child["references"])) != 3 {
		t.Fatal("omission did not inherit references")
	}
	cleared := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "清空引用", "parent_job_id": child["id"], "references": []any{}}, 202)
	cleared = generationWait(t, a, str(cleared["id"]))
	if len(array(cleared["references"])) != 0 {
		t.Fatal("explicit empty references did not clear inheritance")
	}
	materialized := generationRequest(t, a, "POST", "jobs/"+str(cleared["id"])+"/materialize", Doc{"profile_id": profile["id"]}, 200)
	if materialized["profile_id"] != nil {
		t.Fatal("materialize request injected a profile")
	}
	cases := []struct {
		refs   any
		status int
	}{
		{nil, 400},
		{"not-array", 400},
		{[]Doc{{"kind": "site", "id": "missing"}}, 404},
		{[]Doc{{"kind": "site", "id": site["id"], "version": 99}}, 409},
		{[]Doc{{"kind": "site", "id": site["id"], "version": 0}}, 400},
		{[]Doc{{"kind": "site", "id": site["id"], "version": "1"}}, 400},
		{[]Doc{{"kind": "unknown", "id": site["id"]}}, 400},
		{[]Doc{selectedReference("profile", profile), {"kind": "profile", "id": "environment"}}, 400},
		{[]Doc{refs[0], refs[0], refs[0], refs[0], refs[0], refs[0], refs[0], refs[0], refs[0]}, 400},
	}
	for _, tc := range cases {
		generationRequest(t, a, "POST", "jobs", Doc{"prompt": "无效引用", "references": tc.refs}, tc.status)
	}
	updated := clone(site)
	updated["name"] = "新版站点"
	a.store.saveComposer("sites", updated)
	continued := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "旧引用续聊", "parent_job_id": first["id"]}, 202)
	continued = generationWait(t, a, str(continued["id"]))
	ref := object(array(continued["references"])[0])
	if continued["status"] != "completed" || integer(ref["version"]) != 2 || ref["name"] != "新版站点" {
		t.Fatal("continuation did not resolve the same reference ID to its latest version")
	}
	if integer(object(array(a.generation.job(str(first["id"]))["references"])[0])["version"]) != 1 {
		t.Fatal("continuation rewrote historical reference metadata")
	}
	// Explicit version pins retain their conflict protection. Latest-reference
	// continuation cannot switch to another ID when an old material is deleted.
	generationRequest(t, a, "POST", "jobs", Doc{"prompt": "固定旧版", "references": refs}, 409)
	a.store.deleteComposer("sites", str(site["id"]))
	replacement := siteFixture()
	replacement["name"] = site["name"]
	a.store.saveComposer("sites", replacement)
	generationRequest(t, a, "POST", "jobs", Doc{"prompt": "不能替换已删除引用", "parent_job_id": continued["id"]}, 404)
}

func TestGenerationReferenceProfileOnlyCloneAndNoBodySnapshot(t *testing.T) {
	a := generationTestApp(t)
	_, _, profile := generationReferenceFixtures(t, a)
	refs := []Doc{selectedReference("profile", profile)}
	meta, snapshots := a.generation.resolveReferences(Doc{"references": refs}, nil)
	if len(meta) != 1 || strings.Contains(dump(snapshots), "PRIVATE_REFERENCE_") {
		t.Fatal("profile body or description was retained in snapshot")
	}
	a.generation.cloneWebsite = func(context.Context, string, func(string, string)) (Doc, error) {
		return Doc{"site": siteFixture()}, nil
	}
	job := generationRequest(t, a, "POST", "jobs", Doc{"mode": "clone", "source_url": "https://example.com/", "references": refs}, 202)
	job = generationWait(t, a, str(job["id"]))
	if job["status"] != "completed" {
		t.Fatal("clone-only reference failed", job["error"])
	}
	materialized := generationRequest(t, a, "POST", "jobs/"+str(job["id"])+"/materialize", Doc{}, 200)
	if materialized["profile_id"] != profile["id"] || materialized["scenario"] != nil {
		t.Fatal("clone-only profile metadata missing or invented scenario")
	}
}

func TestGenerationReferenceReadPaginationAndCopyIsolation(t *testing.T) {
	a := generationTestApp(t)
	site, _, _ := generationReferenceFixtures(t, a)
	refs, snapshots := a.generation.resolveReferences(Doc{"references": []Doc{selectedReference("site", site)}}, nil)
	agent := &draftAgent{references: refs, referenceSnapshots: snapshots, reference: Doc{"title": "previous clone"}, warnings: []any{"previous clone warning"}, cloned: map[string]bool{"https://prior.example/": true}, allowed: []any{"https://prior.example/"}}
	page, err := agent.perform(context.Background(), "read_reference", Doc{"kind": "site", "id": site["id"], "path": "index.html", "offset": 4, "limit": 8})
	if err != nil || page["content"] != "ORIGINAL" || !boolean(page["truncated"]) || integer(page["next_offset"]) != 12 {
		t.Fatal("reference text pagination failed", page, err)
	}
	if _, err := agent.perform(context.Background(), "use_reference", Doc{"kind": "site", "id": "environment"}); err == nil {
		t.Fatal("unselected material accepted")
	}
	if _, err := agent.perform(context.Background(), "use_reference", Doc{"kind": "site", "id": site["id"]}); err != nil {
		t.Fatal(err)
	}
	if agent.reference != nil || len(agent.warnings) != 0 || len(agent.cloned) != 0 || len(agent.allowed) != 1 {
		t.Fatal("site replacement kept stale clone provenance or changed URL authority")
	}
	object(array(agent.site["files"])[0])["content"] = "changed"
	page, err = agent.perform(context.Background(), "read_reference", Doc{"kind": "site", "id": site["id"], "path": "index.html"})
	if err != nil || !strings.Contains(str(page["content"]), "ORIGINAL_REFERENCE_TEXT") {
		t.Fatal("draft edits mutated reference snapshot", page, err)
	}
}

func TestGenerationReferencesSnapshotBudgetAndAtomicProfileAdoption(t *testing.T) {
	a := generationTestApp(t)
	_, _, profile := generationReferenceFixtures(t, a)
	refs := []Doc{}
	// Real entities are already individually bounded. Together their encoded
	// snapshots can exceed the Agent's separate cumulative material budget.
	for _, id := range []string{"large-reference-one", "large-reference-two", "large-reference-three"} {
		d := Doc{"id": id, "name": id, "version": 1, "files": []Doc{{"path": "one.css", "encoding": "utf8", "content": strings.Repeat("a", 3<<20)}, {"path": "two.css", "encoding": "utf8", "content": strings.Repeat("b", 3<<20)}}}
		put(a.store.db, "sites", d)
		refs = append(refs, selectedReference("site", d))
	}
	generationRequest(t, a, "POST", "jobs", Doc{"prompt": "引用过多素材", "references": refs}, 413)
	job := Doc{"id": "profile-adoption-test", "status": "completed", "references": []Doc{selectedReference("profile", profile)}, "result": generationDraft()}
	a.generation.saveJob(job)
	path := "jobs/profile-adoption-test/materialize"
	before := count(a.store.db, "SELECT COUNT(*) FROM entities")
	profile["body"] = "Updated body"
	a.store.save("profiles", profile)
	generationRequest(t, a, "POST", path, Doc{}, 409)
	if count(a.store.db, "SELECT COUNT(*) FROM entities") != before || a.generation.job("profile-adoption-test")["materialized"] != nil {
		t.Fatal("stale profile adoption partially persisted materials")
	}
	exec(a.store.db, "DELETE FROM entities WHERE kind='profiles' AND id=?", profile["id"])
	generationRequest(t, a, "POST", path, Doc{}, 404)
}

package lab

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAgentConsultationAndJSONExamplesNeverCreateDrafts(t *testing.T) {
	for _, text := range []string{"我可以解答使用问题、编写站点文件或配置模拟响应。", dump(generationDraft()), "```json\n" + dump(generationDraft()) + "\n```", "这里是说明：\n" + strings.Repeat("示例内容", 1000)} {
		t.Run(boundedText(text, 20), func(t *testing.T) {
			a := generationTestApp(t)
			var calls, clones atomic.Int32
			a.generation.cloneWebsite = func(context.Context, string, func(string, string)) (Doc, error) { clones.Add(1); return nil, nil }
			agentModel(t, a, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); generationEnvelope(w, text) })
			before := count(a.store.db, "SELECT COUNT(*) FROM entities")
			job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "请介绍用途并展示 JSON 示例，不修改任何内容", "source_url": "https://example.com/reference"}, 202)
			job = generationWait(t, a, str(job["id"]))
			result := object(job["result"])
			if job["status"] != "completed" || result["kind"] != "message" || result["summary"] != text || len(array(result["changes"])) != 0 || result["site"] != nil || result["scenario"] != nil || calls.Load() != 1 || clones.Load() != 0 {
				t.Fatalf("consultation changed a draft or forced model repair: %s", dump(job))
			}
			generationRequest(t, a, "POST", "jobs/"+str(job["id"])+"/materialize", Doc{"site": siteFixture()}, 409)
			if count(a.store.db, "SELECT COUNT(*) FROM entities") != before {
				t.Fatal("consultation created persistent assets")
			}
		})
	}
}

func TestAgentDialogueRetainsBaselineAndChangesOnlyRequestedModule(t *testing.T) {
	a := generationTestApp(t)
	var round atomic.Int32
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		body := agentRequestBody(r)
		switch round.Add(1) {
		case 1:
			agentReply(w, agentToolCall("docs", "read_module", Doc{"module": "overview"}))
		case 2:
			if !strings.Contains(dump(body), "overview") {
				t.Error("module documentation missing")
			}
			generationEnvelope(w, "我可以帮你创建站点或模拟响应，你可以单独选择其中一种。")
		case 3:
			if !strings.Contains(dump(body), "单独选择") {
				t.Error("question reply was not preserved for follow-up")
			}
			agentReply(w, agentToolCall("site", "write_file", Doc{"path": "index.html", "content": "<h1>First site</h1>"}), agentToolCall("finish", "finish_draft", Doc{"summary": "只创建了站点。"}))
		case 4:
			if !strings.Contains(dump(body), "index.html") || !strings.Contains(dump(body), "只创建了站点") {
				t.Error("baseline/history missing from consultation")
			}
			generationEnvelope(w, "该站点当前用于显示标题，没有请求任何模拟接口。")
		case 5:
			if !strings.Contains(dump(body), "用于显示标题") || !strings.Contains(dump(body), "index.html") {
				t.Error("message parent lost its baseline or reply")
			}
			agentReply(w, agentToolCall("scenario", "set_scenario", Doc{"scenario": generationDraft()["scenario"]}), agentToolCall("finish", "finish_draft", Doc{"summary": "只添加模拟响应。"}))
		case 6:
			agentReply(w, agentToolCall("finish", "finish_draft", Doc{"summary": "可以继续解释已有草稿。"}))
		case 7:
			agentReply(w, agentToolCall("write", "write_file", Doc{"path": "index.html", "content": "<h1>Second site</h1>"}))
		case 8:
			generationEnvelope(w, "标题已调整，模拟响应保持原样。")
		default:
			t.Error("unexpected model round")
			generationEnvelope(w, "停止")
		}
	})
	beforeSites := count(a.store.db, "SELECT COUNT(*) FROM entities WHERE kind='sites'")
	beforeScenarios := count(a.store.db, "SELECT COUNT(*) FROM entities WHERE kind='scenarios'")
	var jobs []Doc
	var parent string
	prompts := []string{"你可以做什么？", "先只创建一个站点", "它有什么用途？", "接着只添加一个模拟接口", "不用修改，继续说明", "现在只改标题"}
	expected := []string{"message", "draft", "message", "draft", "message", "draft"}
	for i, prompt := range prompts {
		raw := Doc{"prompt": prompt}
		if parent != "" {
			raw["parent_job_id"] = parent
		}
		job := generationRequest(t, a, "POST", "jobs", raw, 202)
		job = generationWait(t, a, str(job["id"]))
		result := object(job["result"])
		if job["status"] != "completed" || result["kind"] != expected[i] {
			t.Fatalf("turn %d: %s", i, dump(job))
		}
		if i > 0 && object(result["site"]) == nil {
			t.Fatal("message/continuation lost site baseline")
		}
		wantReplay := (i + 1) * 2
		if wantReplay > 18 {
			wantReplay = 18
		}
		if len(array(job["conversation"])) != wantReplay {
			t.Fatal("conversation replay window lost current context")
		}
		parent = str(job["id"])
		jobs = append(jobs, job)
	}
	if dump(object(jobs[1]["result"])["changes"]) != `["site"]` || dump(object(jobs[3]["result"])["changes"]) != `["scenario"]` || dump(object(jobs[5]["result"])["changes"]) != `["site"]` {
		t.Fatal("unmodified module reported as changed")
	}
	// Adopting the scenario-only turn must not persist its inherited site.
	path := "jobs/" + str(jobs[3]["id"]) + "/materialize"
	generationRequest(t, a, "POST", path, Doc{"site": siteFixture()}, 400)
	saved := generationRequest(t, a, "POST", path, Doc{}, 200)
	again := generationRequest(t, a, "POST", path, Doc{}, 200)
	if saved["site"] != nil || object(saved["scenario"])["id"] != object(again["scenario"])["id"] {
		t.Fatal("scenario-only adoption created site or duplicate")
	}
	if count(a.store.db, "SELECT COUNT(*) FROM entities WHERE kind='sites'") != beforeSites || count(a.store.db, "SELECT COUNT(*) FROM entities WHERE kind='scenarios'") != beforeScenarios+1 {
		t.Fatal("adoption persisted unintended modules")
	}
	detail := a.generation.conversation(str(jobs[0]["conversation_id"]))
	for i, v := range array(detail["turns"]) {
		turn := object(v)
		if object(turn["result"])["kind"] != expected[i] || strings.Contains(dump(turn), "<h1>") {
			t.Fatal("history lost reply kind or leaked full draft")
		}
	}
}

func TestAgentScenarioOnlyEmptyDraftAndNoopFinish(t *testing.T) {
	a := generationTestApp(t)
	var round atomic.Int32
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		switch round.Add(1) {
		case 1:
			agentReply(w, agentToolCall("scenario", "set_scenario", Doc{"scenario": generationDraft()["scenario"]}), agentToolCall("validate", "validate_draft", Doc{}), agentToolCall("finish", "finish_draft", Doc{"summary": "只创建模拟响应。"}))
		case 2:
			agentReply(w, agentToolCall("same", "set_scenario", Doc{"scenario": generationDraft()["scenario"]}), agentToolCall("finish", "finish_draft", Doc{"summary": "无需修改。"}))
		default:
			agentReply(w, agentToolCall("finish", "finish_draft", Doc{"summary": "你希望从哪个模块开始？"}))
		}
	})
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "只创建场景"}, 202)
	job = generationWait(t, a, str(job["id"]))
	result := object(job["result"])
	if job["status"] != "completed" || result["kind"] != "draft" || result["site"] != nil || dump(result["changes"]) != `["scenario"]` {
		t.Fatal("scenario-only job demanded a site", job)
	}
	saved := generationRequest(t, a, "POST", "jobs/"+str(job["id"])+"/materialize", Doc{}, 200)
	if saved["site"] != nil || saved["scenario"] == nil {
		t.Fatal("scenario-only result malformed")
	}
	same := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "维持原样", "parent_job_id": job["id"]}, 202)
	same = generationWait(t, a, str(same["id"]))
	if object(same["result"])["kind"] != "message" || len(array(object(same["result"])["changes"])) != 0 {
		t.Fatal("identical write invented a change")
	}
	empty := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "你可以做什么？"}, 202)
	empty = generationWait(t, a, str(empty["id"]))
	if object(empty["result"])["kind"] != "message" || object(empty["result"])["site"] != nil {
		t.Fatal("no-op finish created assets")
	}
}

func TestAgentFailedToolsRollbackAndChangedModuleValidation(t *testing.T) {
	a := generationTestApp(t)
	var round atomic.Int32
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		switch round.Add(1) {
		case 1:
			agentReply(w, agentToolCall("invalid", "set_site", Doc{"name": "Partial mutation", "entry": "../../bad.html"}))
		case 2:
			generationEnvelope(w, "路径无效，请使用相对文件路径。")
		case 3:
			agentReply(w, agentToolCall("write", "write_file", Doc{"path": "style.css", "content": "body{}"}), agentToolCall("finish", "finish_draft", Doc{"summary": "已完成"}))
		case 4:
			body := agentRequestBody(r)
			if strings.Contains(dump(body["messages"]), "尚未设置模拟场景") {
				t.Error("validation demanded unrelated scenario")
			}
			agentReply(w, agentToolCall("repair", "write_file", Doc{"path": "index.html", "content": "<h1>Valid</h1>"}), agentToolCall("finish", "finish_draft", Doc{"summary": "站点已修复。"}))
		default:
			generationEnvelope(w, "完成")
		}
	})
	first := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "尝试使用错误路径"}, 202)
	first = generationWait(t, a, str(first["id"]))
	if object(first["result"])["kind"] != "message" || object(first["result"])["site"] != nil {
		t.Fatal("failed tool left partial draft")
	}
	second := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "只创建站点"}, 202)
	second = generationWait(t, a, str(second["id"]))
	if second["status"] != "completed" || dump(object(second["result"])["changes"]) != `["site"]` || object(second["result"])["scenario"] != nil {
		t.Fatal("single module repair failed", second)
	}
}

func TestAgentEmptyProviderReplyFailsWithoutDraftRepair(t *testing.T) {
	a := generationTestApp(t)
	var calls atomic.Int32
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); generationEnvelope(w, "  ") })
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "你可以做什么？"}, 202)
	job = generationWait(t, a, str(job["id"]))
	if job["status"] != "failed" || calls.Load() != 3 || job["result"] != nil || !strings.Contains(str(job["error"]), "重试上限") || strings.Contains(dump(job["events"]), `"stage":"validation"`) {
		t.Fatal("empty output was accepted or forced to generate a draft", job)
	}
}

func TestAgentChangesIgnorePersistenceMetadataAndRevertedEdits(t *testing.T) {
	original := validateSite(siteFixture())
	original["id"] = "saved-site"
	original["version"] = 7
	a := &draftAgent{site: clone(original), initialSite: generationModuleSnapshot("site", original)}
	a.site["id"] = "different-id"
	a.site["version"] = 9
	if result := a.result(); result["kind"] != "message" || len(array(result["changes"])) != 0 {
		t.Fatal("persistence metadata caused an authored change")
	}
	a.site["name"] = "Changed name"
	if result := a.result(); result["kind"] != "draft" || dump(result["changes"]) != `["site"]` {
		t.Fatal("authored change was missed")
	}
	a.site["name"] = original["name"]
	if a.result()["kind"] != "message" {
		t.Fatal("reverted edits produced an adoptable draft")
	}
}

func TestGenerationLegacyDraftMetadataAndAdoptionRemainCompatible(t *testing.T) {
	a := generationTestApp(t)
	legacy := Doc{"id": "legacy-draft", "status": "completed", "result": generationDraft(), "created_at": timestamp(), "updated_at": timestamp()}
	a.generation.saveJob(legacy)
	list := a.generation.listConversations("")
	detail := a.generation.conversation("legacy-draft")
	jobs := a.generation.listJobs()
	for _, result := range []Doc{object(object(list[0])["result"]), object(object(array(detail["turns"])[0])["result"]), object(jobs[0]["result"])} {
		if result["kind"] != "draft" || dump(result["changes"]) != `["site","scenario"]` || result["site"] != nil || result["scenario"] != nil {
			t.Fatal("legacy draft summary changed its meaning", result)
		}
	}
	first := generationRequest(t, a, "POST", "jobs/legacy-draft/materialize", Doc{}, 200)
	again := generationRequest(t, a, "POST", "jobs/legacy-draft/materialize", Doc{}, 200)
	if object(first["site"])["id"] != object(again["site"])["id"] || object(first["scenario"])["id"] != object(again["scenario"])["id"] {
		t.Fatal("legacy adoption lost idempotency")
	}
}

package lab

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestGenerationConversationsFilterWorkspacesBeforeLimit(t *testing.T) {
	a := generationTestApp(t)
	first := a.store.saveComposer("workspaces", Doc{"name": "First", "slug": "first"})
	second := a.store.saveComposer("workspaces", Doc{"name": "Second", "slug": "second"})
	deleted := a.store.saveComposer("workspaces", Doc{"name": "Deleted", "slug": "deleted"})
	for i := 0; i < 60; i++ {
		a.generation.saveJob(Doc{"id": fmt.Sprintf("other%d", i), "workspace_id": second["id"], "prompt": "其他工作区", "created_at": fmt.Sprintf("2026-09-18T12:%02d:00Z", i), "updated_at": fmt.Sprintf("2026-09-18T12:%02d:00Z", i)})
	}
	for _, d := range []Doc{
		{"id": "first-root", "workspace_id": first["id"], "prompt": "首轮", "created_at": "2026-09-18T10:00:00Z", "updated_at": "2026-09-18T10:00:00Z"},
		{"id": "first-child", "workspace_id": first["id"], "parent_job_id": "first-root", "prompt": "续聊", "created_at": "2026-09-18T10:01:00Z", "updated_at": "2026-09-18T10:01:00Z"},
		{"id": "unassigned-root", "prompt": "未保存的旧草稿", "created_at": "2026-09-18T10:02:00Z", "updated_at": "2026-09-18T10:02:00Z"},
		{"id": "deleted-root", "workspace_id": deleted["id"], "prompt": "已删除工作区的对话", "created_at": "2026-09-18T10:03:00Z", "updated_at": "2026-09-18T10:03:00Z"},
		// Before workspace isolation, a later turn could carry another workspace.
		// Keep that original unassigned history recoverable without rewriting it.
		{"id": "unassigned-child", "workspace_id": second["id"], "parent_job_id": "unassigned-root", "created_at": "2026-09-18T10:04:00Z", "updated_at": "2026-09-18T10:04:00Z"},
	} {
		a.generation.saveJob(d)
	}
	exec(a.store.db, "DELETE FROM entities WHERE kind='workspaces' AND id=?", deleted["id"])
	before := dump(rows(a.store.db, "SELECT id,doc FROM generation_jobs ORDER BY id"))
	all := array(generationRequest(t, a, "GET", "conversations", nil, 200)["items"])
	if len(all) != 50 {
		t.Fatal("global history limit changed")
	}
	filtered := array(generationRequest(t, a, "GET", "conversations?workspace_id="+str(first["id"]), nil, 200)["items"])
	if len(filtered) != 1 || object(filtered[0])["id"] != "first-root" || object(filtered[0])["workspace_id"] != first["id"] || integer(object(filtered[0])["turn_count"]) != 2 {
		t.Fatalf("workspace history was filtered after the global limit or lost ownership: %s", dump(filtered))
	}
	thread := generationRequest(t, a, "GET", "conversations/first-root", nil, 200)
	if thread["workspace_id"] != first["id"] || object(array(thread["turns"])[1])["workspace_id"] != first["id"] {
		t.Fatal("conversation details lost workspace metadata")
	}
	unassigned := array(generationRequest(t, a, "GET", "conversations?workspace_id=unassigned", nil, 200)["items"])
	if len(unassigned) != 2 || object(unassigned[0])["id"] != "unassigned-root" || object(unassigned[0])["workspace_id"] != "" || object(unassigned[1])["id"] != "deleted-root" || object(unassigned[1])["workspace_id"] != deleted["id"] {
		t.Fatalf("unassigned history did not retain orphaned and original legacy conversations: %s", dump(unassigned))
	}
	if after := dump(rows(a.store.db, "SELECT id,doc FROM generation_jobs ORDER BY id")); after != before {
		t.Fatal("history read migrated stored jobs")
	}
	generationRequest(t, a, "GET", "conversations?workspace_id=../bad", nil, 400)
}

func TestGenerationConversationContinuationKeepsWorkspace(t *testing.T) {
	a := generationTestApp(t)
	first := a.store.saveComposer("workspaces", Doc{"name": "First", "slug": "first"})
	second := a.store.saveComposer("workspaces", Doc{"name": "Second", "slug": "second"})
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) { generationDraftReply(w, generationDraft()) })
	for _, d := range []Doc{
		{"id": "owned", "workspace_id": first["id"], "status": "completed", "result": generationDraft()},
		{"id": "legacy", "status": "completed", "result": generationDraft()},
	} {
		a.generation.saveJob(d)
	}
	for _, body := range []Doc{
		{"workspace_id": second["id"], "parent_job_id": "owned"},
		{"workspace_id": second["id"], "conversation_id": "owned"},
		{"parent_job_id": "owned"},
		{"workspace_id": first["id"], "parent_job_id": "legacy"},
		{"workspace_id": first["id"], "conversation_id": "legacy"},
	} {
		body["prompt"] = "继续调整"
		generationRequest(t, a, "POST", "jobs", body, 409)
	}
	generationRequest(t, a, "POST", "jobs", Doc{"prompt": "开始", "workspace_id": "missing-workspace"}, 404)
	if count(a.store.db, "SELECT COUNT(*) FROM generation_jobs") != 2 {
		t.Fatal("rejected workspace continuation saved a job")
	}
	for _, body := range []Doc{
		{"workspace_id": first["id"], "parent_job_id": "owned"},
		{"workspace_id": first["id"], "conversation_id": "owned"},
		{"parent_job_id": "legacy"},
		{},
	} {
		body["prompt"] = "继续调整"
		job := generationRequest(t, a, "POST", "jobs", body, 202)
		finished := generationWait(t, a, str(job["id"]))
		if finished["status"] != "completed" || str(finished["workspace_id"]) != str(body["workspace_id"]) {
			t.Fatalf("valid workspace or legacy continuation failed: %s", dump(finished))
		}
		if body["parent_job_id"] == "owned" || body["conversation_id"] == "owned" {
			if finished["conversation_id"] != "owned" {
				t.Fatal("same-workspace continuation split the conversation")
			}
		}
	}
}

func TestGenerationConversationHistoryAndRetry(t *testing.T) {
	a := generationTestApp(t)
	m := a.generation
	for i, id := range []string{"first", "second", "retry", "other"} {
		d := Doc{"id": id, "prompt": "制作配置中心", "status": "completed", "created_at": fmt.Sprintf("2026-09-18T10:00:0%dZ", i), "updated_at": fmt.Sprintf("2026-09-18T10:00:0%dZ", i), "events": []Doc{{"stage": "write_file", "detail": "已写入页面"}}, "result": Doc{"summary": "页面已完成", "site": Doc{"files": []Doc{{"content": "DO_NOT_RETURN_FILE"}}}}, "conversation": []Doc{{"content": "DO_NOT_RETURN_CONTEXT"}}}
		if id == "second" {
			d["parent_job_id"] = "first"
			d["status"] = "failed"
			d["error"] = "连接超时"
		}
		if id == "retry" {
			d["conversation_id"] = "first"
		}
		m.saveJob(d)
	}
	list := generationRequest(t, a, "GET", "conversations", nil, 200)
	oldJob := generationRequest(t, a, "GET", "jobs/second", nil, 200)
	if oldJob["conversation_id"] != "first" || m.job("second")["conversation_id"] != nil {
		t.Fatal("legacy job must resolve its conversation without mutating stored history")
	}
	if len(array(list["items"])) != 2 {
		t.Fatalf("grouping: %s", dump(list))
	}
	thread := generationRequest(t, a, "GET", "conversations/first", nil, 200)
	turns := array(thread["turns"])
	if len(turns) != 3 || object(turns[1])["status"] != "failed" || object(turns[2])["id"] != "retry" {
		t.Fatalf("turn order: %s", dump(thread))
	}
	if strings.Contains(dump(list)+dump(thread), "DO_NOT_RETURN") {
		t.Fatal("history leaked site or model context")
	}
	if !strings.Contains(dump(thread), "已写入页面") {
		t.Fatal("history lost tool events")
	}
	if m.jobConversationID(Doc{}, Doc{"parent_job_id": "retry"}, "new") != "first" {
		t.Fatal("continuation split history")
	}
	if m.jobConversationID(Doc{"conversation_id": "first"}, Doc{}, "new") != "first" {
		t.Fatal("retry split history")
	}
	if m.jobConversationID(Doc{}, Doc{}, "new") != "new" {
		t.Fatal("new chat reused history")
	}
	for _, tc := range []struct {
		raw, input Doc
		status     int
	}{
		{Doc{"conversation_id": "unknown"}, Doc{}, 404},
		{Doc{"conversation_id": "other"}, Doc{"parent_job_id": "first"}, 409},
		{Doc{"conversation_id": "../bad"}, Doc{}, 400},
	} {
		func() {
			defer func() {
				p, ok := recover().(problem)
				if !ok || p.status != tc.status {
					t.Errorf("wanted %d, got %#v", tc.status, p)
				}
			}()
			m.jobConversationID(tc.raw, tc.input, "new")
		}()
	}
	generationRequest(t, a, "GET", "conversations/missing", nil, 404)
}

func TestGenerationConversationHistoryLimitIsExplicit(t *testing.T) {
	a := generationTestApp(t)
	for i := 0; i < 102; i++ {
		a.generation.saveJob(Doc{"id": fmt.Sprintf("turn%d", i), "conversation_id": "thread", "prompt": fmt.Sprintf("第%d轮", i), "created_at": fmt.Sprintf("%06d", i), "updated_at": fmt.Sprintf("%06d", i)})
	}
	d := generationRequest(t, a, "GET", "conversations/thread", nil, 200)
	if len(array(d["turns"])) != 100 || !boolean(d["truncated"]) || integer(d["total_turns"]) != 102 || d["title"] != "第0轮" {
		t.Fatalf("limit: %s", dump(d))
	}
}

func TestConversationPagesAndTitles(t *testing.T) {
	a := generationTestApp(t)
	for i := 0; i < 62; i++ {
		agent := "writer"
		if i >= 57 {
			agent = "reviewer"
		}
		a.generation.saveJob(Doc{"id": fmt.Sprintf("paged-%02d", i), "workspace_id": "owned", "prompt": fmt.Sprintf("原始需求 %d", i), "status": "completed", "agent": Doc{"id": agent, "name": agent}, "created_at": fmt.Sprintf("2026-09-18T%02d:00:00Z", i), "updated_at": fmt.Sprintf("2026-09-18T%02d:00:00Z", i)})
	}
	a.generation.saveJob(Doc{"id": "outside", "workspace_id": "outside", "prompt": "其他工作区", "status": "running"})
	before := dump(rows(a.store.db, "SELECT id,doc FROM generation_jobs ORDER BY id"))
	summary := generationRequest(t, a, "GET", "conversations?workspace_id=owned&summary=1", nil, 200)
	if integer(summary["total"]) != 62 || len(array(summary["groups"])) != 2 || summary["items"] != nil {
		t.Fatalf("bad summary: %s", dump(summary))
	}
	page := generationRequest(t, a, "GET", "conversations?workspace_id=owned&agent_id=writer&page=12", nil, 200)
	if integer(page["total"]) != 57 || integer(page["page_size"]) != 5 || len(array(page["items"])) != 2 || object(array(page["items"])[1])["id"] != "paged-00" {
		t.Fatalf("older conversations inaccessible: %s", dump(page))
	}
	first := generationRequest(t, a, "GET", "conversations?workspace_id=owned&agent_id=reviewer&page=99", nil, 200)
	if integer(first["page"]) != 1 || len(array(first["items"])) != 5 {
		t.Fatalf("agent pagination: %s", dump(first))
	}
	empty := generationRequest(t, a, "GET", "conversations?workspace_id=owned&agent_id=missing&page=1", nil, 200)
	if len(array(empty["items"])) != 0 || integer(empty["pages"]) != 1 {
		t.Fatal("empty page invalid")
	}
	for _, invalid := range []string{"0", "-1", "x", "1.5"} {
		generationRequest(t, a, "GET", "conversations?page="+invalid, nil, 400)
	}
	renamed := generationRequest(t, a, "POST", "conversations/paged-00/rename", Doc{"title": "  自定义对话  ", "version": 0}, 200)
	if renamed["title"] != "自定义对话" || integer(renamed["title_version"]) != 1 {
		t.Fatal("rename not saved")
	}
	generationRequest(t, a, "POST", "conversations/paged-00/rename", Doc{"title": "过期覆盖", "version": 0}, 409)
	for _, body := range []Doc{{"title": " ", "version": 1}, {"title": strings.Repeat("字", 81), "version": 1}, {"title": "a\nb", "version": 1}, {"title": "缺少版本"}, {"title": "小数版本", "version": 1.5}} {
		generationRequest(t, a, "POST", "conversations/paged-00/rename", body, 400)
	}
	generationRequest(t, a, "POST", "conversations/missing/rename", Doc{"title": "不存在", "version": 0}, 404)
	if dump(rows(a.store.db, "SELECT id,doc FROM generation_jobs ORDER BY id")) != before {
		t.Fatal("rename changed task history")
	}
	a.generation.saveJob(Doc{"id": "continuation", "conversation_id": "paged-00", "workspace_id": "owned", "prompt": "继续", "status": "completed", "agent": Doc{"id": "writer"}, "created_at": "2026-09-19T00:00:00Z", "updated_at": "2026-09-19T00:00:00Z"})
	detail := generationRequest(t, a, "GET", "conversations/paged-00", nil, 200)
	if detail["title"] != "自定义对话" || integer(detail["title_version"]) != 1 || integer(detail["total_turns"]) != 2 {
		t.Fatalf("continuation lost name: %s", dump(detail))
	}
	page = generationRequest(t, a, "GET", "conversations?workspace_id=owned&agent_id=writer&page=1", nil, 200)
	if object(array(page["items"])[0])["title"] != "自定义对话" {
		t.Fatal("index lost saved title")
	}
}

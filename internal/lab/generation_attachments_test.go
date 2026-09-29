package lab

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestChatAttachmentMetadataAndQuestionContinuation(t *testing.T) {
	for _, prompt := range []string{"帮我托管附件", "将这个文件进行托管，并绑定端口", "host the uploaded file"} {
		modules := object(generationEditScope(Doc{}, Doc{"prompt": prompt}))["modules"]
		if !has(array(modules), "site") || !has(array(modules), "ports") {
			t.Fatal("hosting auto scope missing site/ports", prompt)
		}
	}
	if has(array(generationEditScope(Doc{"edit_scope": "site"}, Doc{"prompt": "托管附件"})["modules"]), "ports") {
		t.Fatal("hosting expanded explicit scope")
	}
	a := generationTestApp(t)
	meta := a.store.receiveHostedFile(strings.NewReader("BINARY_CONTENT_MUST_NOT_REACH_MODEL"), "客户端.zip")
	second := a.store.receiveHostedFile(strings.NewReader("SECOND_BINARY_PRIVATE"), "补充.zip")
	var round atomic.Int32
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		body := dump(agentRequestBody(r))
		if strings.Contains(body, "BINARY_CONTENT_MUST_NOT_REACH_MODEL") || strings.Contains(body, "SECOND_BINARY_PRIVATE") || strings.Contains(body, "SPOOFED_FILENAME") {
			t.Error("client content leaked into model context")
		}
		if !strings.Contains(body, str(meta["id"])) || !strings.Contains(body, "客户端.zip") {
			t.Error("uploaded metadata missing")
		}
		if round.Add(1) == 1 {
			agentReply(w, agentToolCall("ask", "ask_user", Doc{"question": "请上传补充文件，并说明下载目录"}))
		} else {
			if !strings.Contains(body, str(second["id"])) {
				t.Error("question upload missing")
			}
			generationEnvelope(w, "已收到附件")
		}
	})
	for _, invalid := range []any{"bad", []any{Doc{"id": "missing-file"}}, []any{Doc{}}} {
		status := 400
		if strings.Contains(dump(invalid), "missing-file") {
			status = 404
		}
		generationRequest(t, a, "POST", "jobs", Doc{"prompt": "托管文件", "attachments": invalid}, status)
	}
	j := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "托管附件", "attachments": []any{Doc{"id": meta["id"], "name": "SPOOFED_FILENAME", "content": "BINARY_CONTENT_MUST_NOT_REACH_MODEL"}}}, 202)
	a.generation.wg.Wait()
	waiting := a.generation.job(str(j["id"]))
	if waiting["status"] != "waiting_user" {
		t.Fatal("not waiting")
	}
	answer := Doc{"question_id": object(waiting["question"])["id"], "answer": "放在 downloads", "attachments": []any{Doc{"id": meta["id"]}, Doc{"id": second["id"]}}}
	next := generationRequest(t, a, "POST", "jobs/"+str(j["id"])+"/answer", answer, 202)
	again := generationRequest(t, a, "POST", "jobs/"+str(j["id"])+"/answer", answer, 202)
	if next["id"] != again["id"] {
		t.Fatal("duplicate answer started another task")
	}
	a.generation.wg.Wait()
	answer["attachments"] = []any{Doc{"id": meta["id"]}}
	generationRequest(t, a, "POST", "jobs/"+str(j["id"])+"/answer", answer, 409)
	history := a.generation.conversation(str(j["conversation_id"]))
	turns := array(history["turns"])
	if len(turns) != 2 || len(array(object(turns[1])["attachments"])) != 2 {
		t.Fatal("attachment history lost")
	}
	input := a.generation.agentInput(Doc{"prompt": "继续", "parent_job_id": next["id"]})
	if len(array(input["attachments"])) != 2 {
		t.Fatal("continuation lost attachments")
	}
	input = a.generation.agentInput(Doc{"prompt": "新问题", "parent_job_id": next["id"], "attachments": []any{}})
	if len(array(input["attachments"])) != 0 {
		t.Fatal("explicit clearing ignored")
	}
}

func TestEmptyWorkspaceAgentHostingAndHTTPDownload(t *testing.T) {
	b := newLab(t)
	content := []byte("PK\x03\x04\x00\xffunchanged download bytes")
	meta := b.app.store.receiveHostedFile(bytes.NewReader(content), "客户端 #1.zip")
	w := b.api("/workspaces", "POST", Doc{"name": "Empty", "slug": "empty-download", "bindings": []any{}}, 200)
	a := bindingAgent(t, b, w)
	a.editScope = generationEditScope(Doc{"edit_scope": "site"}, Doc{})
	agentOperation(t, a, "read_workspace_sites", Doc{})
	result := agentOperation(t, a, "attach_hosted_file", Doc{"version": w["version"], "site_version": 0, "site_node_id": "root", "file_id": meta["id"], "path": "downloads/客户端 #1.zip"})
	if len(array(result["download_urls"])) != 0 || str(result["notice"]) == "" {
		t.Fatal("invented public URL")
	}
	w = get(b.app.store.db, "workspaces", str(w["id"]))
	if str(w["site_id"]) == "" || integer(w["version"]) != 2 {
		t.Fatal("empty root not automatically bound")
	}
	site := get(b.app.store.db, "sites", str(w["site_id"]))
	if len(array(site["files"])) != 2 {
		t.Fatal("missing download page/file")
	}
	dep := b.api("/workspaces/"+str(w["id"])+"/publish", "POST", Doc{"version": w["version"]}, 200)
	base := str(b.listener(dep)["public_url"])
	r := b.request(base, "/downloads/"+hostedDownloadPath("客户端 #1.zip"), "GET", nil)
	if r.status != 200 || !bytes.Equal(r.body, content) || !strings.Contains(r.header.Get("Content-Disposition"), "attachment") {
		t.Fatal("download bytes or headers wrong")
	}
	agentOperation(t, a, "read_workspace_sites", Doc{})
	result = agentOperation(t, a, "attach_hosted_file", Doc{"version": a.workspaceSitesVersion, "site_version": site["version"], "site_node_id": "root", "file_id": meta["id"], "path": "second.zip"})
	if str(object(array(result["download_urls"])[0])["url"]) != base+"/second.zip" {
		t.Fatal("missing bound download URL")
	}
	if r := b.request(base, "/second.zip", "GET", nil); !bytes.Equal(r.body, content) {
		t.Fatal("live publication not updated")
	}
	a.editScope = generationEditScope(Doc{"edit_scope": "read_only"}, Doc{})
	if _, err := a.perform(context.Background(), "attach_hosted_file", Doc{}); err == nil {
		t.Fatal("read-only scope bypassed")
	}
}

func TestEmptyHostingAtomicValidationAndManualEndpoint(t *testing.T) {
	b := newLab(t)
	w := b.api("/workspaces", "POST", Doc{"name": "Manual", "slug": "manual-files", "bindings": []any{}}, 200)
	meta := b.app.store.receiveHostedFile(strings.NewReader("download"), "index.html")
	path := "/workspaces/" + str(w["id"]) + "/hosted-files"
	args := Doc{"version": w["version"], "site_version": 0, "site_node_id": "root", "file_id": meta["id"], "path": "collect"}
	before := count(b.app.store.db, "SELECT COUNT(*) FROM entities WHERE kind='sites'")
	b.api(path, "POST", args, 409)
	if count(b.app.store.db, "SELECT COUNT(*) FROM entities WHERE kind='sites'") != before || str(get(b.app.store.db, "workspaces", str(w["id"]))["site_id"]) != "" {
		t.Fatal("failed attach left partial material")
	}
	args["path"], args["version"] = "index.html", 999
	b.api(path, "POST", args, 409)
	args["version"] = w["version"]
	args["site_id"] = "unsaved-selection"
	b.api(path, "POST", args, 409)
	args["site_id"] = ""
	result := b.api(path, "POST", args, 200)
	site := object(result["site"])
	if site["entry"] == "index.html" || len(array(site["files"])) != 2 {
		t.Fatal("download replaced HTML entry")
	}
	args["version"], args["site_version"] = object(result["workspace"])["version"], site["version"]
	args["site_id"] = site["id"]
	b.api(path, "POST", args, 409)
}

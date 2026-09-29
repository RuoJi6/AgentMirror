package lab

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestWorkspaceAgentSitesPortsAndHostedDownloads(t *testing.T) {
	b := newLab(t)
	_, _, vpn, w, content := linkedSitesFixture(b)
	a := bindingAgent(t, b, w)
	a.editScope = generationEditScope(Doc{"edit_scope": "all"}, Doc{})
	call := func(name string, args Doc) Doc {
		args["version"] = a.workspaceSitesVersion
		return agentOperation(t, a, name, args)
	}
	agentOperation(t, a, "read_workspace_sites", Doc{})
	call("publish_workspace", Doc{})
	p := b.port()
	base := "http://127.0.0.1:" + fmtPort(p)
	port := call("set_workspace_port", Doc{"json": dump(Doc{"name": "VPN independent", "host": "127.0.0.1", "port": p, "public_url": base, "site_node_id": "vpn"})})
	listenerID := port["listener_id"]
	if r := b.request(base, "/", "GET", nil); r.status != 200 || !strings.Contains(string(r.body), "Download") {
		t.Fatal("agent port not serving child")
	}
	meta := object(array(agentOperation(t, a, "list_hosted_files", Doc{"query": "客户端"})["items"])[0])
	call("attach_hosted_file", Doc{"site_node_id": "vpn", "site_version": a.workspaceSiteVersions[str(vpn["id"])], "path": "downloads/agent.zip", "file_id": meta["id"]})
	if r := b.request(base, "/downloads/agent.zip", "GET", nil); r.status != 200 || !bytes.Equal(r.body, content) {
		t.Fatal("agent download not hot updated")
	}
	call("remove_hosted_file", Doc{"site_node_id": "vpn", "site_version": a.workspaceSiteVersions[str(vpn["id"])], "path": "downloads/agent.zip"})
	expectStatus(t, b.request(base, "/downloads/agent.zip", "GET", nil), 404)
	call("set_workspace_port", Doc{"listener_id": listenerID, "json": `{"enabled":false}`})
	if boolean(get(b.app.store.db, "listeners", str(listenerID))["enabled"]) {
		t.Fatal("pause failed")
	}
	call("set_workspace_port", Doc{"listener_id": listenerID, "json": `{"enabled":true,"site_node_id":"oss"}`})
	if r := b.request(base, "/", "GET", nil); r.status != 200 || !strings.Contains(string(r.body), "VPN") {
		t.Fatal("switch failed")
	}
	call("delete_workspace_port", Doc{"listener_id": listenerID})
	added := call("upsert_workspace_site", Doc{"json": dump(Doc{"name": "Extra", "parent_id": "root", "segment": "extra", "site_id": vpn["id"]})})
	node := added["site_node_id"]
	call("upsert_workspace_site", Doc{"site_node_id": node, "json": `{"name":"Renamed","segment":"renamed"}`})
	call("delete_workspace_site", Doc{"site_node_id": node})
	if len(array(get(b.app.store.db, "workspaces", str(w["id"]))["site_mounts"])) != 2 || entityMaybe(b.app.store.db, "sites", str(vpn["id"])) == nil {
		t.Fatal("removing mount deleted material or other nodes")
	}
	if entityMaybe(b.app.store.db, "hosted_files", str(meta["id"])) == nil {
		t.Fatal("detaching deleted hosted bytes")
	}
}

func TestWorkspaceAgentGuardsConcurrentForeignAndScopedWrites(t *testing.T) {
	b := newLab(t)
	_, _, vpn, w, _ := linkedSitesFixture(b)
	dep := b.api("/workspaces/"+str(w["id"])+"/publish", "POST", Doc{}, 200)
	w = get(b.app.store.db, "workspaces", str(w["id"]))
	l := childListener(b, dep, "vpn")
	a := bindingAgent(t, b, w)
	a.editScope = generationEditScope(Doc{"edit_scope": "all"}, Doc{})
	reject := func(name string, args Doc) {
		t.Helper()
		if _, err := a.perform(context.Background(), name, args); err == nil {
			t.Fatalf("invalid %s allowed: %s", name, dump(args))
		}
	}
	reject("delete_workspace_site", Doc{"version": w["version"], "site_node_id": "vpn"})
	agentOperation(t, a, "read_workspace_sites", Doc{})
	reject("set_workspace_port", Doc{"version": a.workspaceSitesVersion, "json": `{"name":"Missing explicit address"}`})
	before := dump(get(b.app.store.db, "workspaces", str(w["id"])))
	reject("delete_workspace_site", Doc{"version": a.workspaceSitesVersion, "site_node_id": "vpn"})
	if dump(get(b.app.store.db, "workspaces", str(w["id"]))) != before {
		t.Fatal("dependency rejection partially saved")
	}
	l["name"] = "Changed by user"
	b.api("/listeners", "POST", l, 200)
	reject("set_workspace_port", Doc{"version": a.workspaceSitesVersion, "listener_id": l["id"], "json": `{"enabled":false}`})
	other := clone(w)
	delete(other, "id")
	other["name"], other["slug"] = "Other", "other"
	delete(other, "deployment_id")
	other = b.api("/workspaces", "POST", other, 200)
	od := b.api("/workspaces/"+str(other["id"])+"/publish", "POST", Doc{}, 200)
	ol := childListener(b, od, "vpn")
	reject("delete_workspace_port", Doc{"version": a.workspaceSitesVersion, "listener_id": ol["id"]})
	vpn["name"] = "New material name"
	b.api("/sites", "POST", vpn, 200)
	reject("remove_hosted_file", Doc{"version": a.workspaceSitesVersion, "site_version": a.workspaceSiteVersions[str(vpn["id"])], "site_node_id": "vpn", "path": "downloads/client.zip"})
	agentOperation(t, a, "read_workspace_sites", Doc{})
	current := get(b.app.store.db, "workspaces", str(w["id"]))
	current["name"] = "Concurrent rename"
	b.api("/workspaces", "POST", current, 200)
	reject("upsert_workspace_site", Doc{"version": a.workspaceSitesVersion, "site_node_id": "oss", "json": `{"name":"overwrite"}`})
	for _, scope := range []string{"site", "ports", "read_only", "scenario"} {
		a.editScope = generationEditScope(Doc{"edit_scope": scope}, Doc{})
		for _, tool := range a.scopedTools() {
			kind := toolEditModule(str(object(tool["function"])["name"]))
			if kind != "" && kind != scope {
				t.Fatal("scope advertised unwanted mutation", scope, kind)
			}
		}
		if scope != "ports" {
			reject("delete_workspace_port", Doc{"version": a.workspaceSitesVersion, "listener_id": l["id"]})
		}
		if scope != "site" {
			reject("upsert_workspace_site", Doc{"version": a.workspaceSitesVersion, "site_node_id": "oss", "json": `{"name":"BAD"}`})
		}
	}
}

func TestWorkspaceInteractionUpgradePreservesUserToolChoices(t *testing.T) {
	b := newLab(t)
	for _, id := range []string{"writer", "reviewer"} {
		old := get(b.app.store.db, "agent_definitions", id)
		delete(old, "workspace_interaction_version")
		old["tools"] = []any{"read_module"}
		b.app.store.write(func(q queryer) { put(q, "agent_definitions", old) })
	}
	initAgentDefinitions(b.app.store)
	for _, id := range []string{"writer", "reviewer"} {
		upgraded := get(b.app.store.db, "agent_definitions", id)
		if !has(array(upgraded["tools"]), "ask_user") || (id == "writer" && !has(array(upgraded["tools"]), "set_workspace_port")) {
			t.Fatal("missing migration")
		}
		upgraded["tools"] = []any{"finish_draft"}
		if id == "reviewer" {
			upgraded["tools"] = []any{"finish_review"}
		}
		saved := b.app.store.saveAgentDefinition(upgraded)
		initAgentDefinitions(b.app.store)
		if dump(get(b.app.store.db, "agent_definitions", id)) != dump(saved) {
			t.Fatal("restart re-enabled removed tools")
		}
	}
	for _, test := range []struct{ prompt, kind string }{{"创建子站点", "site"}, {"子站绑定端口", "ports"}, {"修改文件托管", "site"}, {"停用端口", "ports"}} {
		if !requestedEditModules(test.prompt)[test.kind] {
			t.Fatal("missing scope", test.prompt)
		}
	}
}

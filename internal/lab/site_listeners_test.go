package lab

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func childListener(b *testLab, dep Doc, node string) Doc {
	b.t.Helper()
	p := b.port()
	return b.api("/listeners", "POST", Doc{"name": node + " entry", "host": "127.0.0.1", "port": p, "public_url": "http://127.0.0.1:" + fmtPort(p), "deployment_id": dep["id"], "site_node_id": node, "enabled": true}, 200)
}

func TestChildListenerRoutesDownloadsAndHotSave(t *testing.T) {
	b := newLab(t)
	_, oss, vpn, w, content := linkedSitesFixture(b)
	dep := b.api("/workspaces/"+str(w["id"])+"/publish", "POST", Doc{}, 200)
	root, ol, vl := b.listener(dep), childListener(b, dep, "oss"), childListener(b, dep, "vpn")
	ob, vb := str(ol["public_url"]), str(vl["public_url"])
	histories := map[string]string{}
	for _, test := range []struct {
		listener                 Doc
		path, body, server, node string
	}{
		{root, "/", "OSS", "nginx", "root"}, {ol, "/", "VPN", "ObjectStore", "oss"}, {vl, "/", "Download", "nginx", "vpn"},
		{ol, "/vpn/", "Download", "nginx", "vpn"}, {root, "/oss/vpn/", "Download", "nginx", "vpn"},
		{ol, "/api/status", `"ok":true`, "ObjectStore", "oss"}, {ol, "/vpn/api/status", `"ok":true`, "nginx", "vpn"}, {vl, "/api/status", `"ok":true`, "nginx", "vpn"},
	} {
		r := b.request(str(test.listener["public_url"]), test.path, "GET", nil)
		if r.status != 200 || !strings.Contains(string(r.body), test.body) || r.header.Get("Server") != test.server {
			t.Fatalf("%s %s: %d %q %q", test.listener["name"], test.path, r.status, r.body, r.header.Get("Server"))
		}
		run := r.header.Get("X-Run-ID")
		if prior := histories[str(test.listener["id"])]; prior != "" && prior != run {
			t.Fatal("same port lost session")
		}
		histories[str(test.listener["id"])] = run
		events := rows(b.app.store.db, "SELECT detail FROM events WHERE session_id=? AND kind='response_sent' ORDER BY id DESC LIMIT 1", run)
		if len(events) != 1 || decode(str(events[0]["detail"]))["site_node_id"] != test.node {
			t.Fatalf("incorrect node evidence %v", events)
		}
	}
	if histories[str(root["id"])] == histories[str(ol["id"])] || histories[str(ol["id"])] == histories[str(vl["id"])] {
		t.Fatal("ports shared sessions")
	}
	for _, path := range []string{"/oss/", "/vpn/", "/oss/vpn/api/status"} {
		expectStatus(t, b.request(vb, path, "GET", nil), 404)
	}
	for _, test := range []struct{ base, path string }{{vb, "/downloads/client.zip"}, {ob, "/vpn/downloads/client.zip"}} {
		r := b.request(test.base, test.path, "GET", nil)
		if r.status != 200 || !bytes.Equal(r.body, content) || !strings.Contains(r.header.Get("Content-Disposition"), "attachment") {
			t.Fatal("child download changed bytes/headers")
		}
		req, _ := http.NewRequest("GET", test.base+test.path, nil)
		req.Header.Set("Range", "bytes=2-6")
		res, err := b.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 206 || !bytes.Equal(got, content[2:7]) {
			t.Fatal("child download range")
		}
		head := b.request(test.base, test.path, "HEAD", nil)
		if head.status != 200 || len(head.body) != 0 || head.header.Get("Content-Length") != fmtPort(len(content)) {
			t.Fatal("child download HEAD")
		}
	}
	run := histories[str(vl["id"])]
	snapshot := dump(object(b.app.store.session(run))["snapshot"])
	release := dump(get(b.app.store.db, "composer_releases", str(dep["release_id"])))
	object(array(vpn["files"])[0])["content"] = "<h1>Updated VPN</h1>"
	b.api("/sites", "POST", vpn, 200)
	for _, test := range []struct{ base, path string }{{vb, "/"}, {ob, "/vpn/"}, {str(root["public_url"]), "/oss/vpn/"}} {
		r := b.request(test.base, test.path, "GET", nil)
		if !strings.Contains(string(r.body), "Updated VPN") {
			t.Fatal("save did not update all entries")
		}
		if test.base == vb && r.header.Get("X-Run-ID") != run {
			t.Fatal("hot save replaced visitor")
		}
	}
	if dump(object(b.app.store.session(run))["snapshot"]) != snapshot || dump(get(b.app.store.db, "composer_releases", str(dep["release_id"]))) != release {
		t.Fatal("hot save rewrote history")
	}
	object(array(oss["files"])[0])["content"] = "<h1>Updated OSS</h1>"
	b.api("/sites", "POST", oss, 200)
	b.restart()
	if r := b.request(ob, "/", "GET", nil); !strings.Contains(string(r.body), "Updated OSS") {
		t.Fatal("restart lost child entry")
	}
	if r := b.request(vb, "/", "GET", nil); !strings.Contains(string(r.body), "Updated VPN") {
		t.Fatal("restart lost VPN entry")
	}
}

func TestChildListenerCallbacksIsolationAndRebinding(t *testing.T) {
	b := newLab(t)
	_, _, _, w, _ := linkedSitesFixture(b)
	profile := b.api("/profiles", "POST", Doc{"name": "Child receipt", "body": "TOKEN={{token}} URL={{callback_url}} RUN={{run_id}}"}, 200)
	scenario := b.api("/scenarios", "POST", Doc{"name": "Receipt fixture", "rules": []any{Doc{"id": "receipt", "method": "GET", "path": "/receipt", "delivery_required": true, "response": Doc{"status": 200, "format": "text", "body": "{{prompt}}"}}}}, 200)
	w["callback_path"] = "/receipts/report"
	for _, node := range []string{"root", "oss", "vpn"} {
		w["bindings"] = append(array(w["bindings"]), Doc{"id": node + "-receipt", "scenario_id": scenario["id"], "profile_id": profile["id"], "site_node_id": node})
	}
	w = b.api("/workspaces", "POST", w, 200)
	dep := b.api("/workspaces/"+str(w["id"])+"/publish", "POST", Doc{}, 200)
	ol, vl := childListener(b, dep, "oss"), childListener(b, dep, "vpn")
	ob, vb := str(ol["public_url"]), str(vl["public_url"])
	first := b.request(vb, "/receipt", "GET", nil)
	run, token := first.header.Get("X-Run-ID"), responseToken(t, string(first.body))
	if !strings.Contains(string(first.body), "URL="+vb+"/receipts/report") {
		t.Fatal("callback has wrong origin/path")
	}
	collect := func(base, token string, want int) {
		t.Helper()
		expectStatus(t, b.request(base, "/receipts/report", "POST", Doc{"run_id": run, "token": token, "data": "synthetic"}), want)
	}
	collect(vb, token, 201)
	collect(ob, token, 403)
	expectStatus(t, b.request(ob, "/receipt?run_id="+run, "GET", nil), 403)
	second := b.request(vb, "/receipt", "GET", nil)
	latest := responseToken(t, string(second.body))
	collect(vb, token, 403)
	collect(vb, latest, 201)
	// Older settings clients omit the new field; preserve this entry's site.
	delete(vl, "site_node_id")
	vl["name"] = "renamed"
	vl = b.api("/listeners", "POST", vl, 200)
	if listenerSiteID(vl) != "vpn" {
		t.Fatal("old client silently rebound child to root")
	}
	vl["site_node_id"] = "oss"
	vl = b.api("/listeners", "POST", vl, 200)
	collect(vb, latest, 403)
	expectStatus(t, b.request(vb, "/receipt?run_id="+run, "GET", nil), 403)
	fresh := b.request(vb, "/", "GET", nil)
	if fresh.header.Get("X-Run-ID") == run || !strings.Contains(string(fresh.body), "VPN") {
		t.Fatal("rebinding reused stale visitor or root")
	}
	// Health follows the entry's simulated Server header as well.
	if b.request(vb, "/health", "GET", nil).header.Get("Server") != "ObjectStore" {
		t.Fatal("child health leaked global header")
	}
	vl["site_node_id"] = "root"
	b.api("/listeners", "POST", vl, 200)
	if r := b.request(vb, "/", "GET", nil); !strings.Contains(string(r.body), "OSS") {
		t.Fatal("explicit reset to main site failed")
	}
}

func TestChildListenerValidationAndPreview(t *testing.T) {
	b := newLab(t)
	_, _, vpn, w, content := linkedSitesFixture(b)
	dep := b.api("/workspaces/"+str(w["id"])+"/publish", "POST", Doc{}, 200)
	vl := childListener(b, dep, "vpn")
	invalid := clone(vl)
	invalid["site_node_id"] = "missing"
	b.api("/listeners", "POST", invalid, 404)
	if listenerSiteID(get(b.app.store.db, "listeners", str(vl["id"]))) != "vpn" {
		t.Fatal("failed update mutated listener")
	}
	// Pausing does not discard the binding: node removal still needs an explicit unbind.
	vl["enabled"] = false
	b.api("/listeners", "POST", vl, 200)
	w = get(b.app.store.db, "workspaces", str(w["id"]))
	invalid = clone(w)
	invalid["site_mounts"] = array(invalid["site_mounts"])[:1]
	invalid["bindings"] = array(invalid["bindings"])[:2]
	b.api("/workspaces", "POST", invalid, 409)
	// Rebased callback paths and health must never hide a child's routes or files.
	for _, path := range []string{"collect", "health"} {
		site := clone(vpn)
		site["files"] = append(array(site["files"]), Doc{"path": path, "encoding": "utf8", "content": "reserved"})
		b.api("/sites", "POST", site, 409)
	}
	for _, node := range []string{"oss", "vpn"} {
		req := clone(w)
		req["site_node_id"] = node
		req["request"] = Doc{"method": "GET", "path": "/api/status"}
		out := b.api("/composer/preview", "POST", req, 200)
		if integer(out["status"]) != 200 || out["binding_id"] != node+"-status" {
			t.Fatalf("wrong preview rule %v", out)
		}
		req["request"] = Doc{"method": "GET", "path": "/"}
		out = b.api("/composer/preview", "POST", req, 200)
		want := "nginx"
		if node == "oss" {
			want = "ObjectStore"
		}
		if object(out["headers"])["Server"] != want {
			t.Fatalf("preview wrong Server %v", out)
		}
	}
	descendant := clone(w)
	descendant["site_node_id"] = "oss"
	descendant["request"] = Doc{"method": "GET", "path": "/vpn/api/status"}
	preview := b.api("/composer/preview", "POST", descendant, 200)
	if preview["binding_id"] != "vpn-status" || object(preview["headers"])["Server"] != "nginx" {
		t.Fatalf("descendant must inherit workspace header, not OSS header: %v", preview)
	}
	req := clone(w)
	req["site_node_id"] = "vpn"
	req["request"] = Doc{"method": "GET", "path": "/downloads/client.zip"}
	out := b.api("/composer/preview", "POST", req, 200)
	if integer(out["status"]) != 200 || integer(object(out["download"])["size"]) != len(content) {
		t.Fatal("preview lost child download")
	}
	// Release original nested path after explicitly removing the port.
	b.api("/listeners/"+str(vl["id"]), "DELETE", nil, 200)
	b.api("/workspaces", "POST", invalid, 200)
}

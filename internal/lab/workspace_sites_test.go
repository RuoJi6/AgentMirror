package lab

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func linkedSitesFixture(b *testLab) (Doc, Doc, Doc, Doc, []byte) {
	makeSite := func(name, html string) Doc {
		return b.api("/sites", "POST", Doc{"name": name, "files": []any{Doc{"path": "index.html", "encoding": "utf8", "content": html}}}, 200)
	}
	root := makeSite("Portal", `<a href="oss/">OSS</a>`)
	oss := makeSite("OSS", `<a href="vpn/">VPN</a>`)
	vpn := makeSite("VPN", `<a href="downloads/client.zip">Download</a>`)
	content := []byte("PK\x03\x04\x00\xffharmless download fixture\r\n")
	meta := b.app.store.receiveHostedFile(bytes.NewReader(content), "客户端.zip")
	vpn["files"] = append(array(vpn["files"]), Doc{"path": "downloads/client.zip", "encoding": "hosted", "file_id": meta["id"], "download_name": "客户端.zip"})
	vpn = b.api("/sites", "POST", vpn, 200)
	scenario := b.api("/scenarios", "POST", Doc{"name": "Node status", "rules": []any{Doc{"id": "status", "method": "GET", "path": "/api/status", "conditions": []any{}, "response": Doc{"status": 200, "format": "json", "body": `{"ok":true}`}}}}, 200)
	w := b.api("/workspaces", "POST", Doc{"name": "Linked workspace", "slug": "linked", "site_id": root["id"], "server_header": "nginx", "site_mounts": []any{
		Doc{"id": "oss", "site_id": oss["id"], "name": "OSS", "parent_id": "root", "segment": "oss", "server_header": "ObjectStore"},
		Doc{"id": "vpn", "site_id": vpn["id"], "name": "VPN", "parent_id": "oss", "segment": "vpn"},
	}, "bindings": []any{Doc{"id": "root-status", "scenario_id": scenario["id"]}, Doc{"id": "oss-status", "scenario_id": scenario["id"], "site_node_id": "oss"}, Doc{"id": "vpn-status", "scenario_id": scenario["id"], "site_node_id": "vpn"}}}, 200)
	return root, oss, vpn, w, content
}

func TestLinkedSitesHTTPDownloadsAndLiveUpdate(t *testing.T) {
	b := newLab(t)
	_, oss, vpn, w, content := linkedSitesFixture(b)
	dep := b.api("/workspaces/"+str(w["id"])+"/publish", "POST", Doc{"version": w["version"]}, 200)
	base := str(b.listener(dep)["public_url"])
	var run string
	for _, path := range []string{"/", "/oss/", "/oss/vpn/", "/oss/vpn/downloads/client.zip", "/api/status", "/oss/api/status", "/oss/vpn/api/status"} {
		r := b.request(base, path, "GET", nil)
		if r.status != 200 {
			t.Fatalf("%s: %d %s", path, r.status, r.body)
		}
		if run == "" {
			run = r.header.Get("X-Run-ID")
		}
		if r.header.Get("X-Run-ID") != run {
			t.Fatal("linked navigation lost session")
		}
		want := "nginx"
		if path == "/oss/" || path == "/oss/api/status" {
			want = "ObjectStore"
		}
		if r.header.Get("Server") != want {
			t.Fatalf("%s wrong Server %q", path, r.header.Get("Server"))
		}
		if strings.HasSuffix(path, ".zip") && (!bytes.Equal(r.body, content) || !strings.Contains(r.header.Get("Content-Disposition"), "attachment")) {
			t.Fatal("download altered bytes/headers")
		}
	}
	redirect := b.request(base, "/oss/vpn?from=oss", "GET", nil)
	if redirect.status != 307 || redirect.header.Get("Location") != "/oss/vpn/?from=oss" {
		t.Fatalf("bad canonical redirect: %v", redirect)
	}
	missing := b.request(base, "/oss/vpn/missing.css", "GET", nil)
	if missing.status != 404 {
		t.Fatal("child resource fell through")
	}
	for _, test := range []struct {
		method, rangeValue string
		status             int
		want               []byte
	}{{"HEAD", "", 200, nil}, {"GET", "bytes=2-6", 206, content[2:7]}, {"GET", "bytes=999-", 416, nil}} {
		req, _ := http.NewRequest(test.method, base+"/oss/vpn/downloads/client.zip", nil)
		req.Header.Set("Range", test.rangeValue)
		res, err := b.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != test.status || (test.status != 416 && !bytes.Equal(body, test.want)) {
			t.Fatalf("range/head mismatch %d %q", res.StatusCode, body)
		}
		if test.method == "HEAD" && res.Header.Get("Content-Length") != fmt.Sprint(len(content)) {
			t.Fatal("HEAD lost length")
		}
	}
	if count(b.app.store.db, "SELECT COUNT(*) FROM events WHERE session_id=? AND kind='file_download'", run) != 2 {
		t.Fatal("missing or spurious downloads")
	}
	responseEvents := rows(b.app.store.db, "SELECT detail FROM events WHERE session_id=? AND kind='response_sent'", run)
	found := false
	for _, row := range responseEvents {
		d := decode(str(row["detail"]))
		if d["path"] == "/oss/vpn/downloads/client.zip" {
			found = true
			if d["site_version_id"] != versionKey(vpn) || d["site_node_id"] != "vpn" {
				t.Fatalf("wrong download version evidence %v", d)
			}
		}
	}
	if !found {
		t.Fatal("no response evidence")
	}
	release := get(b.app.store.db, "composer_releases", str(dep["release_id"]))
	immutable := dump(release)
	object(array(oss["files"])[0])["content"] = `<h1>Updated OSS</h1>`
	oss = b.api("/sites", "POST", oss, 200)
	if r := b.request(base, "/oss/", "GET", nil); !strings.Contains(string(r.body), "Updated OSS") || r.header.Get("X-Run-ID") != run {
		t.Fatal("child save did not update existing visitor")
	}
	if dump(get(b.app.store.db, "composer_releases", str(release["id"]))) != immutable {
		t.Fatal("mutated release history")
	}
	// Old clients changing other workspace settings must not drop child mounts.
	delete(w, "site_mounts")
	w = b.api("/workspaces", "POST", w, 200)
	if len(array(w["site_mounts"])) != 2 {
		t.Fatal("legacy save removed mounts")
	}
	b.api("/sites/"+str(oss["id"]), "DELETE", nil, 409)
	// Removing a hosted reference removes the public URL but preserves immutable bytes.
	fid := str(object(array(vpn["files"])[1])["file_id"])
	vpn["files"] = array(vpn["files"])[:1]
	b.api("/sites", "POST", vpn, 200)
	if r := b.request(base, "/oss/vpn/downloads/client.zip", "GET", nil); r.status != 404 {
		t.Fatal("removed download still reachable")
	}
	f, _ := b.app.store.openHostedFile(fid)
	defer f.Close()
	saved, _ := io.ReadAll(f)
	if !bytes.Equal(saved, content) {
		t.Fatal("historical blob removed")
	}
}

func TestLinkedSitesPreviewAndReviewParity(t *testing.T) {
	b := newLab(t)
	_, _, _, w, content := linkedSitesFixture(b)
	for _, path := range []string{"/oss/", "/oss/api/status", "/oss/vpn/downloads/client.zip"} {
		req := clone(w)
		req["request"] = Doc{"method": "GET", "path": path}
		out := b.api("/composer/preview", "POST", req, 200)
		if integer(out["status"]) != 200 {
			t.Fatalf("preview failed %v", out)
		}
		if strings.HasPrefix(path, "/oss/") && !strings.Contains(path, "/vpn/") && object(out["headers"])["Server"] != "ObjectStore" {
			t.Fatalf("child preview header %v", out)
		}
		if strings.HasSuffix(path, ".zip") && (out["source"] != "hosted_file" || integer(object(out["download"])["size"]) != len(content)) {
			t.Fatal("preview missing download metadata")
		}
	}
	before := count(b.app.store.db, "SELECT COUNT(*) FROM events")
	r := &workspaceReviewer{manager: b.app.generation, jobID: "linked-review", snapshot: reviewSnapshot(b.app.store.db, str(w["id"])), config: get(b.app.store.db, "agent_definitions", "reviewer")}
	if err := r.open(); err != nil {
		t.Fatal(err)
	}
	defer r.sandbox.close()
	out := r.sandbox.request(Doc{"path": "/oss/vpn/downloads/client.zip"}, true)
	if out["body"] != "" || !boolean(out["body_omitted"]) || integer(out["body_bytes"]) != len(content) || object(out["download"])["sha256"] != fmt.Sprintf("%x", sha256.Sum256(content)) {
		t.Fatalf("binary must be streamed/hashed, not model text: %v", out)
	}
	result := r.suite(context.Background())
	if integer(object(result["counts"])["fail"]) != 0 {
		t.Fatalf("review mismatch %s", dump(result))
	}
	if count(b.app.store.db, "SELECT COUNT(*) FROM events") != before {
		t.Fatal("review wrote production evidence")
	}
}

func TestLinkedSitesRejectAmbiguousTopologyAndHostedReferences(t *testing.T) {
	b := newLab(t)
	root, _, _, w, _ := linkedSitesFixture(b)
	for _, mode := range []string{"cycle", "missing", "duplicate", "reserved", "depth", "shadow", "rule"} {
		t.Run(mode, func(t *testing.T) {
			raw := clone(w)
			mounts := array(raw["site_mounts"])
			code := 400
			switch mode {
			case "cycle":
				object(mounts[0])["parent_id"] = "vpn"
			case "missing":
				object(mounts[1])["parent_id"] = "absent"
			case "duplicate":
				object(mounts[1])["parent_id"] = "root"
				object(mounts[1])["segment"] = "oss"
				code = 409
			case "reserved":
				object(mounts[0])["segment"] = "collect"
				code = 409
			case "depth":
				parent := "vpn"
				for i := 0; i < 7; i++ {
					id := fmt.Sprint("deep", i)
					mounts = append(mounts, Doc{"id": id, "name": id, "site_id": root["id"], "parent_id": parent, "segment": id})
					parent = id
				}
				raw["site_mounts"] = mounts
			case "shadow":
				object(mounts[0])["segment"] = "index.html" // slug validation, no ambiguous asset serving
			case "rule":
				object(array(raw["bindings"])[0])["paths"] = Doc{"status": "/oss/api/other"}
				code = 409
			}
			b.api("/workspaces", "POST", raw, code)
		})
	}
	invalid := clone(root)
	invalid["files"] = append(array(invalid["files"]), Doc{"path": "vpn.zip", "encoding": "hosted", "file_id": "missing"})
	b.api("/sites", "POST", invalid, 404)
}

func TestHostedUploadAuthenticationAndBinaryIntegrity(t *testing.T) {
	b := newLab(t)
	content := bytes.Repeat([]byte{0, 255, 1, 2}, 1<<18)
	endpoint := b.admin + "/api/hosted-files?name=" + url.QueryEscape("客户端.zip")
	send := func(token string) *http.Response {
		req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(content))
		req.Header.Set("X-Admin-Token", token)
		res, err := b.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := send("")
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("upload accepted without CSRF")
	}
	res = send(b.csrf)
	payload, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatalf("upload %d %s", res.StatusCode, payload)
	}
	meta := decode(string(payload))
	if integer(meta["size"]) != len(content) || str(meta["sha256"]) != fmt.Sprintf("%x", sha256.Sum256(content)) {
		t.Fatal("upload corrupted metadata")
	}
	f, _ := b.app.store.openHostedFile(str(meta["id"]))
	saved, _ := io.ReadAll(f)
	f.Close()
	if !bytes.Equal(saved, content) {
		t.Fatal("upload corrupted content")
	}
	req, _ := http.NewRequest("POST", b.admin+"/api/hosted-files?name=..%2Fevil.zip", strings.NewReader("x"))
	req.Header.Set("X-Admin-Token", b.csrf)
	res, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatal("unsafe filename accepted")
	}
}

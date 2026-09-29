package lab

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

type testLab struct {
	t      *testing.T
	app    *App
	client *http.Client
	admin  string
	opts   Options
	ports  map[int]bool
	csrf   string
}
type reply struct {
	status int
	body   []byte
	header http.Header
}

func (r reply) doc() Doc { return decode(string(r.body)) }
func newLab(t *testing.T) *testLab {
	b := newUnauthedLab(t)
	b.authenticate(true)
	return b
}
func newUnauthedLab(t *testing.T) *testLab {
	return newTestLab(t, true)
}
func newTestLab(t *testing.T, authoringFixtures bool) *testLab {
	t.Helper()
	b := &testLab{t: t, ports: map[int]bool{}}
	b.opts = Options{DBPath: filepath.Join(t.TempDir(), "lab.sqlite3"), AdminPort: b.port(), Frontend: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>Embedded administration</title>")}, "assets/app.js": &fstest.MapFile{Data: []byte("/* bundled frontend */")}}}
	var err error
	b.app, err = New(b.opts)
	if err != nil {
		t.Fatal(err)
	}
	if authoringFixtures {
		resetAuthoringTestFixtures(b.app.store)
	}
	b.admin = b.app.AdminURL()
	transport := &http.Transport{Proxy: nil}
	jar, _ := cookiejar.New(nil)
	b.client = &http.Client{Transport: transport, Jar: jar, Timeout: 5 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(func() { transport.CloseIdleConnections(); b.app.Close() })
	return b
}
func (b *testLab) authenticate(setup bool) {
	status := b.api("/auth/status", "GET", nil, 200)
	b.csrf = str(status["csrf"])
	body := Doc{"username": "testadmin", "password": "SYNTHETIC_AUTH_PASSWORD_2026", "confirm_password": "SYNTHETIC_AUTH_PASSWORD_2026"}
	endpoint := "/auth/login"
	if setup {
		endpoint = "/auth/setup"
	}
	result := b.api(endpoint, "POST", body, 200)
	b.csrf = str(result["csrf"])
}
func (b *testLab) port() int {
	b.t.Helper()
	for {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			b.t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		if !b.ports[port] {
			b.ports[port] = true
			return port
		}
	}
}
func (b *testLab) request(base, p, method string, body any) reply {
	b.t.Helper()
	var raw []byte
	if body != nil {
		raw = jsonBytes(body)
	}
	req, err := http.NewRequest(method, base+p, bytes.NewReader(raw))
	if err != nil {
		b.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if base == b.admin {
		req.Header.Set("X-Admin-Token", b.csrf)
	}
	response, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		b.t.Fatal(err)
	}
	return reply{response.StatusCode, payload, response.Header}
}
func (b *testLab) api(p, method string, body any, status int) Doc {
	b.t.Helper()
	r := b.request(b.admin, "/api"+p, method, body)
	if r.status != status {
		b.t.Fatalf("%s %s: status %d, want %d: %s", method, p, r.status, status, r.body)
	}
	return r.doc()
}
func (b *testLab) deployment() Doc {
	seedLegacyTemplates(b.app.store)
	return b.app.store.save("deployments", Doc{"name": "Go 测试部署", "slug": "go-fixture", "template_id": "login", "profile_id": "receipt", "carriers": []any{"api"}, "surfaces": []any{}, "trigger": "page_load", "linked_surfaces": true, "enabled": true})
}
func (b *testLab) listener(d Doc) Doc {
	p := b.port()
	raw := Doc{"name": "Go 测试端口", "host": "127.0.0.1", "port": p, "public_url": "http://127.0.0.1:" + fmtPort(p), "deployment_id": d["id"], "root_surface": "page", "enabled": true, "overrides": nil}
	if d["mode"] == "composable" {
		return b.api("/listeners", "POST", raw, 200)
	}
	return b.legacyListener(raw)
}

// Old runtime contracts are exercised with internal fixtures; their removed
// authoring endpoints must not be reopened just to construct test data.
func seedLegacyTemplates(s *Store) {
	s.write(func(q queryer) {
		for _, raw := range array(defaults["templates"]) {
			doc := clone(object(raw))
			if entityMaybe(q, "templates", str(doc["id"])) == nil {
				doc["created_at"], doc["updated_at"] = timestamp(), timestamp()
				put(q, "templates", doc)
			}
		}
	})
}

func (b *testLab) legacyListener(raw Doc) Doc {
	m := b.app.listeners
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked(raw)
}
func (b *testLab) visit(l Doc) (Doc, reply) {
	b.t.Helper()
	r := b.request(str(l["public_url"]), "/", "GET", nil)
	if r.status != 200 {
		b.t.Fatalf("visit: %d %s", r.status, r.body)
	}
	return b.api("/sessions/"+r.header.Get("X-Run-ID"), "GET", nil, 200), r
}
func (b *testLab) restart() {
	b.t.Helper()
	b.client.CloseIdleConnections()
	b.app.Close()
	var err error
	b.app, err = New(b.opts)
	if err != nil {
		b.t.Fatal(err)
	}
	b.admin = b.app.AdminURL()
	b.authenticate(false)
}
func expectStatus(t *testing.T, r reply, status int) {
	t.Helper()
	if r.status != status {
		t.Fatalf("got %d, want %d: %s", r.status, status, r.body)
	}
}

func TestFreshDatabaseAndEmbeddedFrontend(t *testing.T) {
	b := newTestLab(t, false)
	b.authenticate(true)
	state := b.api("/state", "GET", nil, 200)
	if len(array(state["deployments"])) != 0 || len(array(state["listeners"])) != 0 {
		t.Fatal("fresh database should only start management")
	}
	if object(state["runtime"])["backend"] != "go" || len(array(state["profiles"])) != 12 || len(array(state["workspaces"])) != 12 {
		t.Fatal("wrong initial state")
	}
	for _, p := range []string{"/", "/deployments", "/assets/app.js"} {
		expectStatus(t, b.request(b.admin, p, "GET", nil), 200)
	}
	for _, p := range []string{"/missing.css", "/assets/../../go.mod", "/%2e%2e/server.py", "/api/missing"} {
		expectStatus(t, b.request(b.admin, p, "GET", nil), 404)
	}
	b.restart()
	if len(array(b.api("/state", "GET", nil, 200)["deployments"])) != 0 {
		t.Fatal("restart seeded deployments")
	}
}
func TestManagementHostOriginAndCSRF(t *testing.T) {
	b := newLab(t)
	cases := []struct{ name, host, token, origin string }{{"foreign host", "evil.invalid", b.csrf, ""}, {"missing token", "", "", ""}, {"bad token", "", "bad", ""}, {"foreign origin", "", b.csrf, "https://evil.invalid"}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", b.admin+"/api/profiles", strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			if c.host != "" {
				req.Host = c.host
			}
			req.Header.Set("X-Admin-Token", c.token)
			req.Header.Set("Origin", c.origin)
			r, err := b.client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			if r.StatusCode != 403 {
				t.Fatal(r.StatusCode)
			}
		})
	}
	l := b.listener(b.deployment())
	for _, p := range []string{"/api/state", "/api/export", "/api/listeners"} {
		expectStatus(t, b.request(str(l["public_url"]), p, "GET", nil), 404)
	}
}
func TestRequestBodyValidation(t *testing.T) {
	b := newLab(t)
	cases := []struct {
		name, body, contentType string
		want                    int
	}{{"non object", "[]", "application/json", 400}, {"invalid json", "{", "application/json", 400}, {"trailing object", "{}{}", "application/json", 400}, {"non finite", "{\"x\":NaN}", "application/json", 400}, {"invalid utf8", string([]byte{'{', '"', 'x', '"', ':', '"', 255, '"', '}'}), "application/json", 400}, {"wrong content type", "{}", "text/plain", 415}, {"size limit", strings.Repeat("x", maxBody+1), "application/json", 413}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", b.admin+"/api/profiles", strings.NewReader(c.body))
			req.Header.Set("X-Admin-Token", b.csrf)
			req.Header.Set("Content-Type", c.contentType)
			r, err := b.client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			if r.StatusCode != c.want {
				t.Fatalf("%d != %d", r.StatusCode, c.want)
			}
		})
	}
}
func TestProfileVersionsValidationAndSnapshot(t *testing.T) {
	b := newLab(t)
	profile := get(b.app.store.db, "profiles", "receipt")
	stale := clone(profile)
	profile["body"] = "GO_VERSION_TWO {{run_id}} {{token}} {{callback_url}}"
	profile = b.api("/profiles", "POST", profile, 200)
	if integer(profile["version"]) != 2 {
		t.Fatal("version not incremented")
	}
	b.api("/profiles", "POST", stale, 409)
	d := b.deployment()
	l := b.listener(d)
	session, _ := b.visit(l)
	snap := object(session["snapshot"])
	profile["body"] = "GO_VERSION_THREE"
	b.api("/profiles", "POST", profile, 200)
	r := b.request(str(l["public_url"]), "/portal/api/content?run_id="+str(session["id"]), "GET", nil)
	if str(object(r.doc()["client_api"])["instruction"]) != "GO_VERSION_THREE" {
		t.Fatal("existing session did not use latest profile")
	}
	if dump(object(b.app.store.session(str(session["id"]))["snapshot"])) != dump(snap) {
		t.Fatal("profile hot update rewrote historical snapshot")
	}
	if !strings.Contains(str(snap["instruction"]), str(session["id"])) {
		t.Fatal("variables not rendered")
	}
	versions := b.request(b.admin, "/api/profiles/receipt/versions", "GET", nil)
	var history []Doc
	json.Unmarshal(versions.body, &history)
	if len(history) != 3 {
		t.Fatal("history lost")
	}
	profile = get(b.app.store.db, "profiles", "receipt")
	profile["body"] = "{{result.custom_output}}"
	profile = b.api("/profiles", "POST", profile, 200)
	profile["body"] = "text"
	profile["fields"] = nil
	b.api("/profiles", "POST", profile, 400)
	profile["fields"] = []any{Doc{"name": "repeated", "type": "string", "required": true}, Doc{"name": "repeated", "type": "string", "required": false}}
	b.api("/profiles", "POST", profile, 400)
}
func TestDeliveryCarriersEscapingAndLoginGate(t *testing.T) {
	b := newLab(t)
	profile := get(b.app.store.db, "profiles", "receipt")
	profile["body"] = `TEXT </script><script>alert(1)</script> --> */ " TOKEN={{token}}`
	b.api("/profiles", "POST", profile, 200)
	d := b.deployment()
	d["carriers"] = []any{"html_visible", "html_hidden", "html_comment", "js", "api"}
	d["trigger"] = "after_login"
	d = b.app.store.save("deployments", d)
	l := b.listener(d)
	session, r := b.visit(l)
	id := str(session["id"])
	token := str(object(session["snapshot"])["token"])
	base := str(l["public_url"])
	if strings.Contains(string(r.body), token) {
		t.Fatal("locked HTML leaked instruction")
	}
	for _, p := range []string{"/assets/client.js", "/portal/api/login-config"} {
		rep := b.request(base, p+"?run_id="+id, "GET", nil)
		if strings.Contains(string(rep.body), token) {
			t.Fatal("locked carrier leaked")
		}
	}
	expectStatus(t, b.request(base, "/portal/workspace?run_id="+id, "GET", nil), 403)
	expectStatus(t, b.request(base, "/portal/api/session?run_id="+id, "POST", Doc{"action": "login"}), 200)
	r = b.request(base, "/portal/workspace?run_id="+id, "GET", nil)
	expectStatus(t, r, 200)
	if strings.Contains(string(r.body), token) || responseToken(t, string(r.body)) == "" || strings.Contains(string(r.body), "<script>alert(1)</script>") || !strings.Contains(string(r.body), "&#45;&#45;") {
		t.Fatal("unlocked HTML escaping failed")
	}
	script := b.request(base, "/assets/client.js?run_id="+id, "GET", nil)
	if !strings.Contains(string(script.body), "* /") {
		t.Fatal("JS comment terminator not escaped")
	}
	config := b.request(base, "/portal/api/login-config?run_id="+id, "GET", nil).doc()
	if object(config["client_api"]) == nil {
		t.Fatal("login did not unlock API")
	}
	preview := page(get(b.app.store.db, "templates", "login"), nil, true, false)
	if strings.Contains(preview, "run_id="+id) {
		t.Fatal("preview recorded a session")
	}
}
func TestHeadRequestsDoNotRecordDelivery(t *testing.T) {
	b := newLab(t)
	d := b.deployment()
	d["carriers"] = []any{"html_visible", "api", "js"}
	d = b.app.store.save("deployments", d)
	l := b.listener(d)
	base := str(l["public_url"])
	r := b.request(base, "/", "HEAD", nil)
	expectStatus(t, r, 200)
	id := r.header.Get("X-Run-ID")
	for _, p := range []string{"/portal/api/content", "/assets/client.js"} {
		expectStatus(t, b.request(base, p+"?run_id="+id, "HEAD", nil), 200)
	}
	session := b.api("/sessions/"+id, "GET", nil, 200)
	for _, v := range array(session["events"]) {
		if object(v)["kind"] == "delivery" {
			t.Fatal("HEAD recorded delivery")
		}
	}
}
func TestComposedSurfacesAndArtifactBodies(t *testing.T) {
	b := newLab(t)
	d := b.deployment()
	for _, kind := range []string{"swagger", "springboot", "hash_dump", "backup"} {
		d["surfaces"] = append(array(d["surfaces"]), Doc{"kind": kind, "profile_id": "receipt"})
	}
	d = b.app.store.save("deployments", d)
	l := b.listener(d)
	base := str(l["public_url"])
	swagger := b.request(base, "/swagger-ui/", "GET", nil)
	id := swagger.header.Get("X-Run-ID")
	expectStatus(t, swagger, 200)
	spec := b.request(base, "/v3/api-docs?run_id="+id, "GET", nil)
	expectStatus(t, spec, 200)
	if !strings.Contains(str(object(spec.doc()["info"])["description"]), "AGENTMIRROR_TEST") {
		t.Fatal("missing OpenAPI instruction")
	}
	expectStatus(t, b.request(base, "/actuator/info?run_id="+id, "GET", nil), 403)
	for _, p := range []string{"/actuator", "/actuator/health", "/actuator/info", "/actuator/env", "/exports/hashes.txt", "/backups/", "/backups/application.yml.bak"} {
		expectStatus(t, b.request(base, p, "GET", nil), 200)
	}
	archive := b.request(base, "/backups/site-backup.zip", "GET", nil)
	expectStatus(t, archive, 200)
	reader, err := zip.NewReader(bytes.NewReader(archive.body), int64(len(archive.body)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 3 {
		t.Fatal("wrong archive layout")
	}
	for _, f := range reader.File {
		if f.Name == "integration-notes.txt" {
			r, _ := f.Open()
			content, _ := io.ReadAll(r)
			r.Close()
			if !strings.Contains(string(content), "AGENTMIRROR_TEST") {
				t.Fatal("wrong archive notes")
			}
		}
	}
	for _, surface := range array(d["surfaces"]) {
		object(surface)["profile_id"] = "observe"
	}
	b.app.store.save("deployments", d)
	spec = b.request(base, "/v3/api-docs?run_id="+id, "GET", nil)
	if !strings.Contains(str(object(spec.doc()["info"])["description"]), "AGENTMIRROR_TEST") {
		t.Fatal("surface snapshot mutated")
	}
}
func TestReceiptValidationAnnotationsAndClear(t *testing.T) {
	b := newLab(t)
	profile := get(b.app.store.db, "profiles", "receipt")
	profile["fields"] = []any{Doc{"name": "n", "type": "number", "required": true}, Doc{"name": "details", "type": "object", "required": false}}
	profile["commands"] = []any{Doc{"id": "fixed", "label": "合成测试", "command": "printf SYNTHETIC", "expected": "SYNTHETIC"}}
	b.api("/profiles", "POST", profile, 200)
	l := b.listener(b.deployment())
	session, _ := b.visit(l)
	id := str(session["id"])
	base := str(l["public_url"])
	payload := Doc{"run_id": id, "token": object(session["snapshot"])["token"], "data": Doc{"n": 42}, "command_results": []any{Doc{"id": "fixed", "stdout": "SYNTHETIC", "stderr": "", "exit_code": 0}}}
	invalid := clone(payload)
	invalid["token"] = "bad"
	expectStatus(t, b.request(base, "/collect", "POST", invalid), 403)
	invalid = clone(payload)
	invalid["data"] = Doc{}
	expectStatus(t, b.request(base, "/collect", "POST", invalid), 201)
	invalid = clone(payload)
	invalid["data"] = Doc{"n": true}
	expectStatus(t, b.request(base, "/collect", "POST", invalid), 201)
	invalid = clone(payload)
	invalid["command_results"] = []any{Doc{"id": "fixed", "exit_code": nil}}
	expectStatus(t, b.request(base, "/collect", "POST", invalid), 201)
	invalid = clone(payload)
	invalid["command_results"] = []any{Doc{"id": "unknown"}}
	expectStatus(t, b.request(base, "/collect", "POST", invalid), 201)
	expectStatus(t, b.request(base, "/collect", "POST", payload), 201)
	expectStatus(t, b.request(base, "/collect", "POST", payload), 201)
	b.api("/sessions/"+id+"/label", "POST", Doc{"outcome": "executed", "note": "合成回执，未执行命令"}, 200)
	if integer(b.api("/sessions?status=received", "GET", nil, 200)["total"]) != 1 {
		t.Fatal("received filter")
	}
	exported := b.api("/export", "GET", nil, 200)
	if len(array(object(array(exported["sessions"])[0])["reports"])) != 6 {
		t.Fatal("lost repeated receipts")
	}
	b.api("/sessions", "DELETE", nil, 200)
	state := b.api("/state", "GET", nil, 200)
	if integer(object(state["stats"])["reports"]) != 0 || count(b.app.store.db, "SELECT COUNT(*) FROM events") != 0 || len(array(state["legacy_deployments"])) != 1 {
		t.Fatal("clear did not cascade or removed configuration")
	}
}
func TestListenerRollbackPauseDeleteAndRestart(t *testing.T) {
	b := newLab(t)
	l := b.listener(b.deployment())
	old := clone(l)
	base := str(l["public_url"])
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	busy := occupied.Addr().(*net.TCPAddr).Port
	l["port"] = busy
	l["public_url"] = "http://127.0.0.1:" + fmtPort(busy)
	b.api("/listeners", "POST", l, 409)
	expectStatus(t, b.request(base, "/health", "GET", nil), 200)
	if integer(get(b.app.store.db, "listeners", str(l["id"]))["port"]) != integer(old["port"]) {
		t.Fatal("failed bind changed config")
	}
	l = clone(old)
	l["host"] = "0.0.0.0"
	b.api("/listeners", "POST", l, 409)
	l = clone(old)
	l["enabled"] = false
	l = b.api("/listeners", "POST", l, 200)
	if l["status"] != "stopped" {
		t.Fatal(l)
	}
	b.client.CloseIdleConnections()
	if r, err := b.client.Get(base + "/health"); err == nil {
		r.Body.Close()
		t.Fatal("paused socket still listening")
	}
	l["enabled"] = true
	l = b.api("/listeners", "POST", l, 200)
	b.restart()
	expectStatus(t, b.request(base, "/health", "GET", nil), 200)
	b.api("/listeners/"+str(l["id"]), "DELETE", nil, 200)
	b.restart()
	if len(array(b.api("/state", "GET", nil, 200)["listeners"])) != 0 {
		t.Fatal("deleted port resurrected")
	}
}
func TestIndependentListeners(t *testing.T) {
	b := newLab(t)
	d := b.deployment()
	first := b.listener(d)
	second := b.listener(d)
	second["overrides"] = Doc{"profile_id": "observe", "carriers": []any{"api"}, "trigger": "page_load", "surfaces": []any{Doc{"kind": "swagger", "profile_id": "receipt"}}}
	second["root_surface"] = "swagger"
	second = b.legacyListener(second)
	redirect := b.request(str(second["public_url"]), "/", "GET", nil)
	expectStatus(t, redirect, 302)
	if redirect.header.Get("Location") != "/swagger-ui/" {
		t.Fatal("wrong root")
	}
	session, _ := b.visit(first)
	id := str(session["id"])
	expectStatus(t, b.request(str(second["public_url"]), "/portal/api/content?run_id="+id, "GET", nil), 403)
	expectStatus(t, b.request(str(second["public_url"]), "/collect", "POST", Doc{"run_id": id, "token": object(session["snapshot"])["token"], "data": Doc{"message": "fixed"}}), 403)
	expectStatus(t, b.request(str(second["public_url"]), "/h/other", "GET", nil), 404)
	b.api("/listeners/"+str(second["id"])+"/default", "POST", nil, 200)
	if b.api("/state", "GET", nil, 200)["settings"].(map[string]any)["default_listener"] != second["id"] {
		t.Fatal("default not saved")
	}

}
func TestConcurrentRequestsAndOptimisticEditing(t *testing.T) {
	b := newLab(t)
	l := b.listener(b.deployment())
	base := str(l["public_url"])
	profile := get(b.app.store.db, "profiles", "receipt")
	raw := jsonBytes(profile)
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	results := make(chan int, 2)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := b.client.Get(base + "/portal/api/content")
			if err != nil {
				errs <- err
				return
			}
			defer r.Body.Close()
			io.Copy(io.Discard, r.Body)
			if r.StatusCode != 200 {
				errs <- fmt.Errorf("request status %d", r.StatusCode)
			}
		}()
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("POST", b.admin+"/api/profiles", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Admin-Token", b.csrf)
			r, err := b.client.Do(req)
			if err != nil {
				errs <- err
				return
			}
			defer r.Body.Close()
			io.Copy(io.Discard, r.Body)
			results <- r.StatusCode
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	close(results)
	statuses := map[int]int{}
	for code := range results {
		statuses[code]++
	}
	if statuses[200] != 1 || statuses[409] != 1 {
		t.Fatal("optimistic edit lost", statuses)
	}
	if count(b.app.store.db, "SELECT COUNT(*) FROM sessions") != 20 {
		t.Fatal("concurrent visit lost")
	}
	for _, row := range rows(b.app.store.db, "SELECT snapshot FROM sessions") {
		snap := decode(str(row["snapshot"]))
		version := integer(object(snap["profile"])["version"])
		if version != 1 && version != 2 {
			t.Fatal("partial profile snapshot")
		}
	}
}

func TestReferenceValidationAndUnboundPorts(t *testing.T) {
	b := newLab(t)
	d := b.deployment()
	l := b.listener(d)
	b.api("/profiles/receipt", "DELETE", nil, 400)
	b.api("/templates/login", "DELETE", nil, 410)
	b.api("/deployments/"+str(d["id"]), "DELETE", nil, 410)
	invalid := clone(l)
	invalid["port"] = 8765.5
	b.api("/listeners", "POST", invalid, 400)
	invalid = clone(l)
	invalid["public_url"] = "http://0.0.0.0:" + fmtPort(integer(l["port"]))
	b.api("/listeners", "POST", invalid, 400)
	invalid = clone(l)
	invalid["host"] = "localhost"
	b.api("/listeners", "POST", invalid, 400)
	invalid = clone(l)
	invalid["deployment_id"] = nil
	b.api("/listeners", "POST", invalid, 400)
	invalid["enabled"] = false
	unbound := b.api("/listeners", "POST", invalid, 200)
	if unbound["deployment_id"] != nil || unbound["status"] != "stopped" {
		t.Fatal("unbound port listening")
	}
	b.api("/listeners/"+str(l["id"]), "DELETE", nil, 200)
	b.app.store.delete("deployments", str(d["id"]))
}

func TestPythonDatabaseCompatibility(t *testing.T) {
	// Fixture was exported by the actual Python Store, not by Go's initializer.
	b := newLab(t)
	b.app.Close()
	target := filepath.Join(t.TempDir(), "python-v2.sqlite3")
	schema, err := os.ReadFile("testdata/python-v2.sql")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(string(schema))
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/python-v2-expected.json")
	if err != nil {
		t.Fatal(err)
	}
	expected := decode(string(raw))
	b.opts.DBPath = target
	b.opts.PublicPort = b.port()
	b.app, err = New(b.opts)
	if err != nil {
		t.Fatal(err)
	}
	b.admin = b.app.AdminURL()
	// Upgrading an old database must require setup before exposing any records.
	b.api("/state", "GET", nil, 401)
	b.authenticate(true)
	state := b.api("/state", "GET", nil, 200)
	for _, key := range []string{"templates", "profiles", "deployments"} {
		for _, item := range array(expected[key]) {
			id := str(object(item)["id"])
			actual := entityMaybe(b.app.store.db, key, id)
			// Unused original defaults may be retired by the builtin migration;
			// the edited receipt profile and its history must remain identical.
			if key == "profiles" && actual == nil && (id == "observe" || id == "environment" || id == "custom") {
				continue
			}
			if !reflect.DeepEqual(actual, object(item)) {
				t.Fatalf("Python %s/%s changed during migration", key, id)
			}
		}
	}
	if state["templates"] != nil || len(array(state["deployments"])) != 0 || len(array(state["legacy_deployments"])) != len(array(expected["deployments"])) {
		t.Fatal("legacy authoring data remained in current management state")
	}
	original := object(expected["session"])
	id := str(original["id"])
	actual := b.api("/sessions/"+id, "GET", nil, 200)
	if !reflect.DeepEqual(actual, original) {
		t.Fatal("Python session, events, reports or snapshot changed")
	}
	history := b.request(b.admin, "/api/profiles/receipt/versions", "GET", nil)
	var versions any
	decoder := json.NewDecoder(bytes.NewReader(history.body))
	decoder.UseNumber()
	if err = decoder.Decode(&versions); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(versions, expected["versions"]) {
		t.Fatal("Python version history changed")
	}
	if len(array(state["listeners"])) != 1 || !boolean(object(state["settings"])["ports_unified"]) {
		t.Fatal("legacy fixed ports not migrated")
	}
	snap := object(original["snapshot"])
	expectStatus(t, b.request("http://127.0.0.1:"+fmtPort(b.opts.PublicPort), "/collect", "POST", Doc{"run_id": id, "token": snap["token"], "data": Doc{"message": "SYNTHETIC_GO_RECEIPT"}}), 201)
	b.api("/listeners/primary", "DELETE", nil, 200)
	b.restart()
	if len(array(b.api("/state", "GET", nil, 200)["listeners"])) != 0 {
		t.Fatal("deleted legacy listener recreated")
	}
	if integer(object(b.api("/state", "GET", nil, 200)["stats"])["reports"]) != 2 {
		t.Fatal("receipt lost after restart")
	}
}

func TestRestoreConflictKeepsManagementAvailable(t *testing.T) {
	b := newLab(t)
	l := b.listener(b.deployment())
	b.app.Close()
	occupied, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", fmtPort(integer(l["port"]))))
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	b.app, err = New(b.opts)
	if err != nil {
		t.Fatal(err)
	}
	b.admin = b.app.AdminURL()
	state := b.api("/state", "GET", nil, 200)
	found := false
	for _, v := range array(state["listeners"]) {
		item := object(v)
		if item["id"] == l["id"] {
			found = true
			if item["status"] != "error" || str(item["error"]) == "" || !boolean(item["enabled"]) {
				t.Fatal("missing bind failure", item)
			}
		}
	}
	if !found {
		t.Fatal("lost listener")
	}
	occupied.Close()
	restored := b.api("/listeners", "POST", l, 200)
	if restored["status"] != "running" {
		t.Fatal("failed to retry listener")
	}
}

func TestManagementStartupRejectsConfiguredPort(t *testing.T) {
	b := newLab(t)
	l := b.listener(b.deployment())
	b.app.Close()
	invalid := b.opts
	invalid.AdminPort = integer(l["port"])
	other, err := New(invalid)
	if err == nil {
		other.Close()
		t.Fatal("startup reused a public port")
	}
	b.app, err = New(b.opts)
	if err != nil {
		t.Fatal(err)
	}
	b.admin = b.app.AdminURL()
	if integer(b.app.listeners.runtime()["admin_port"]) != b.opts.AdminPort {
		t.Fatal("startup did not use configured management port")
	}
}

func TestManagementStartupConfigurationAndLegacyUpgrade(t *testing.T) {
	b := newLab(t)
	l := b.listener(b.deployment())
	original := get(b.app.store.db, "listeners", str(l["id"]))
	// Simulate the previous release's persisted administration entry.
	legacyPort := b.port()
	b.app.store.write(func(q queryer) {
		put(q, "listeners", Doc{"id": "admin", "role": "admin", "name": "旧管理后台", "host": "127.0.0.1", "port": legacyPort, "enabled": true, "public_url": "http://127.0.0.1:" + fmtPort(legacyPort)})
	})
	oldURL := b.admin
	b.opts.AdminHost = "0.0.0.0"
	b.opts.AdminPort = b.port()
	b.restart()
	state := b.api("/state", "GET", nil, 200)
	runtime := object(state["runtime"])
	if integer(runtime["admin_port"]) != b.opts.AdminPort || runtime["admin_host"] != "0.0.0.0" {
		t.Fatal("management did not use startup options", runtime)
	}
	if len(array(state["listeners"])) != 1 || count(b.app.store.db, "SELECT COUNT(*) FROM entities WHERE kind='listeners' AND id='admin'") != 0 {
		t.Fatal("management remains in persisted test listeners")
	}
	if !reflect.DeepEqual(original, get(b.app.store.db, "listeners", str(l["id"]))) {
		t.Fatal("upgrade changed test listener")
	}
	expectStatus(t, b.request(str(l["public_url"]), "/health", "GET", nil), 200)
	for _, url := range []string{oldURL, "http://127.0.0.1:" + fmtPort(legacyPort)} {
		if r, err := b.client.Get(url + "/api/state"); err == nil {
			r.Body.Close()
			t.Fatal("previous management port still listening", url)
		}
	}
	b.api("/listeners", "POST", Doc{"id": "admin", "port": b.port()}, 400)
	b.api("/listeners", "POST", Doc{"role": "admin", "port": b.port()}, 400)
	b.api("/listeners/admin", "DELETE", nil, 400)
	b.api("/listeners/admin/default", "POST", nil, 400)
	reserved := clone(l)
	reserved["port"] = b.opts.AdminPort
	reserved["public_url"] = b.admin
	reserved["enabled"] = false
	b.api("/listeners", "POST", reserved, 409)
	if b.app.AdminURL() != b.admin {
		t.Fatal("API changed management listener")
	}
	// Wildcard binding still checks Host and same-origin writes.
	req, _ := http.NewRequest("GET", b.admin+"/api/state", nil)
	req.Host = "untrusted.invalid:" + fmtPort(b.opts.AdminPort)
	r, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatal("wildcard admin accepted foreign host")
	}
	// Clearing the host option restores its default rather than reading the DB.
	b.opts.AdminHost = ""
	b.restart()
	if b.app.listeners.runtime()["admin_host"] != "127.0.0.1" {
		t.Fatal("management host persisted across startup options")
	}
}

func TestManagementStartupValidation(t *testing.T) {
	b := newLab(t)
	b.app.Close()
	for _, host := range []string{"localhost", "invalid", "::1"} {
		opts := b.opts
		opts.AdminHost = host
		app, err := New(opts)
		if err == nil {
			app.Close()
			t.Fatal("invalid management host accepted", host)
		}
	}
	occupied, err := net.Listen("tcp4", "127.0.0.1:"+fmtPort(b.opts.AdminPort))
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	app, err := New(b.opts)
	if err == nil {
		app.Close()
		t.Fatal("occupied management port accepted")
	}
	occupied.Close()
	b.restart()
	b.api("/state", "GET", nil, 200)
}

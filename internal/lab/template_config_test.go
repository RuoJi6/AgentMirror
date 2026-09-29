package lab

import (
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func templateStore(t *testing.T) *Store {
	t.Helper()
	s, err := openStore(filepath.Join(t.TempDir(), "templates.sqlite3"), "http://127.0.0.1:18765")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	resetAuthoringTestFixtures(s)
	seedLegacyTemplates(s)
	return s
}

func customTemplateFixture() Doc {
	return Doc{"name": "自定义门户", "kind": "docs", "brand": "Example <Lab>", "title": "团队门户", "description": "Fixture description", "accent": "#2468ab", "render_mode": "custom", "html": `<main class="portal"><h1>{{title}}</h1><p>{{brand}}</p></main>`, "css": ".portal { padding: 42px; }", "tags": []any{" 内部站点 ", "API", "api"}}
}

func TestLegacyTemplateConfigurationPersistenceAndRendering(t *testing.T) {
	s := templateStore(t)
	raw := customTemplateFixture()
	preview := page(merge(Doc{"kind": raw["kind"]}, templateConfig(raw, nil)), nil, true, false)
	if !strings.Contains(preview, "团队门户") {
		t.Fatal("legacy rendering did not preserve appearance")
	}
	if count(s.db, "SELECT COUNT(*) FROM sessions") != 0 || len(listing(s.db, "templates")) != 4 {
		t.Fatal("preview wrote a template or session")
	}
	saved := s.save("templates", raw)
	if !reflect.DeepEqual(saved["tags"], []any{"内部站点", "API"}) {
		t.Fatal("tags not normalized", saved["tags"])
	}
	preview = page(saved, nil, true, false)
	for _, expected := range []string{"Example &lt;Lab&gt;", "团队门户", ".portal { padding: 42px; }", "不投放提示词"} {
		if !strings.Contains(preview, expected) {
			t.Fatal("preview missing", expected)
		}
	}
	// Old clients that edit basic fields must not erase newly added source/tags.
	legacyEdit := clone(saved)
	for _, key := range []string{"render_mode", "html", "css", "tags"} {
		delete(legacyEdit, key)
	}
	legacyEdit["title"] = "更新标题"
	updated := s.save("templates", legacyEdit)
	for _, key := range []string{"render_mode", "html", "css", "tags"} {
		if !reflect.DeepEqual(updated[key], saved[key]) {
			t.Fatal("legacy edit erased", key)
		}
	}
	copy := clone(updated)
	delete(copy, "id")
	copy["name"] = "副本"
	copied := s.save("templates", copy)
	if copied["id"] == saved["id"] || !reflect.DeepEqual(copied["tags"], saved["tags"]) || copied["html"] != saved["html"] {
		t.Fatal("copy did not preserve custom configuration")
	}
	check(s.db.Close())
	reopened, err := openStore(s.path, "http://127.0.0.1:18765")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.db.Close()
	if get(reopened.db, "templates", str(updated["id"]))["html"] != saved["html"] {
		t.Fatal("custom source not persisted after reopening")
	}
}

func TestTemplateConfigurationValidation(t *testing.T) {
	cases := []struct {
		key   string
		value any
	}{
		{"render_mode", "unknown"}, {"html", " "}, {"html", 42},
		{"html", strings.Repeat("x", 96*1024+1)}, {"css", strings.Repeat("x", 32*1024+1)},
		{"tags", "login"}, {"tags", []any{" "}}, {"tags", []any{42}},
		{"tags", []any{strings.Repeat("字", 33)}}, {"tags", make([]any, 13)},
		{"accent", "red"}, {"kind", "login"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			defer func() {
				value := recover()
				p, ok := value.(problem)
				if !ok || p.status != 400 {
					t.Fatal("expected validation error", value)
				}
			}()
			raw := customTemplateFixture()
			raw[tc.key] = tc.value
			templateConfig(raw, nil)
		})
	}
}

func TestTemplateCustomDeploymentAndSnapshot(t *testing.T) {
	s := templateStore(t)
	raw := customTemplateFixture()
	raw["kind"], raw["html"] = "login", `<section class="portal"><h1>{{title}}</h1>{{login_form}}</section>`
	template := s.save("templates", raw)
	d := s.save("deployments", Doc{"name": "自定义页面部署", "slug": "custom-page", "template_id": template["id"], "profile_id": "receipt", "carriers": []any{"html_visible", "api"}, "surfaces": []any{}, "trigger": "after_login", "linked_surfaces": true, "enabled": true})
	// Exercise the public handlers without opening a socket or using a live DB.
	listener := Doc{"id": "template-fixture", "name": "Template fixture", "host": "127.0.0.1", "port": 18765, "public_url": "http://127.0.0.1:18765", "deployment_id": d["id"], "enabled": true, "root_surface": "page", "overrides": nil}
	s.write(func(q queryer) { put(q, "listeners", listener) })
	app := &App{store: s}
	w := httptest.NewRecorder()
	app.publicRoute(w, httptest.NewRequest("GET", "/", nil), listener)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `id="login-form"`) || strings.Contains(w.Body.String(), `aria-label="客户端接入说明"`) {
		t.Fatal("custom login entry or delivery gate failed", w.Code)
	}
	id := w.Header().Get("X-Run-ID")
	if id == "" {
		t.Fatal("custom deployment did not create a session")
	}
	session := s.session(id)
	snapshot := object(session["snapshot"])
	if object(snapshot["template"])["html"] != raw["html"] {
		t.Fatal("custom source missing from session snapshot")
	}
	updated := clone(template)
	updated["html"] = `<h1>修改后的页面</h1>{{login_form}}`
	s.save("templates", updated)
	if strings.Contains(page(object(snapshot["template"]), session, false, false), "修改后的页面") {
		t.Fatal("template edit changed existing session")
	}
	form := httptest.NewRequest("POST", "/portal/api/session?run_id="+id, strings.NewReader(`{"action":"login"}`))
	form.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	app.publicRoute(w, form, listener)
	if w.Code != 200 {
		t.Fatal("custom login failed", w.Code, w.Body.String())
	}
	next := str(decode(w.Body.String())["next"])
	w = httptest.NewRecorder()
	app.publicRoute(w, httptest.NewRequest("GET", next, nil), listener)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `aria-label="客户端接入说明"`) {
		t.Fatal("post-login delivery missing", w.Code)
	}
	if !strings.Contains(clientJS(session, false), `JSON.stringify({action:'login'})`) {
		t.Fatal("login must submit a simulation event, not form credentials")
	}
}

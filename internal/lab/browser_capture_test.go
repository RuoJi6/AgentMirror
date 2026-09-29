package lab

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrowserFetchUsesPublicPinnedAnonymousGETs(t *testing.T) {
	c, hits := testSiteCloner(t, map[string]cloneFixture{
		"/page":          {contentType: "text/html", body: "<h1>render me</h1>"},
		"/redirect":      {status: 302, location: "/page"},
		"/hash-redirect": {status: 302, location: "/redirect#/dashboard"},
		"/clear-hash":    {status: 302, location: "/page#"},
		"/private":       {status: 302, location: "http://127.0.0.1/secret"},
	})
	for _, request := range []struct{ method, url string }{
		{"POST", "http://site.example.com/page"},
		{"GET", "http://127.0.0.1/secret"},
		{"GET", "http://169.254.169.254/latest/meta-data"},
		{"GET", "file:///etc/passwd"},
		{"GET", "http://user:secret@site.example.com/page"},
	} {
		if result := c.browserFetch(context.Background(), "1", request.method, request.url, "document"); result["error"] == nil {
			t.Fatalf("request escaped browser fetch guard: %#v", request)
		}
	}
	if len(hits()) != 0 {
		t.Fatal("blocked requests reached network")
	}
	if result := c.browserFetch(context.Background(), "2", "GET", "http://site.example.com/private", "document"); result["error"] == nil {
		t.Fatal("private redirect allowed")
	}
	result := c.browserFetch(context.Background(), "3", "GET", "http://site.example.com/page", "document")
	if result["status"] != 200 {
		t.Fatal(result)
	}
	body, err := base64.StdEncoding.DecodeString(str(result["body_base64"]))
	if err != nil || string(body) != "<h1>render me</h1>" {
		t.Fatal(result)
	}
	if object(result["headers"])["set-cookie"] != nil {
		t.Fatal("source cookie forwarded")
	}
	result = c.browserFetch(context.Background(), "4", "GET", "http://site.example.com/redirect", "document")
	if result["status"] != 307 || object(result["headers"])["location"] != "http://site.example.com/page" {
		t.Fatalf("browser location must follow validated redirect: %#v", result)
	}
	for source, expected := range map[string]string{
		"/hash-redirect": "http://site.example.com/page#/dashboard",
		"/clear-hash":    "http://site.example.com/page#",
	} {
		redirect := c.browserFetch(context.Background(), "hash", "GET", "http://site.example.com"+source, "document")
		if object(redirect["headers"])["location"] != expected {
			t.Fatalf("redirect fragment lost: %#v", redirect)
		}
	}
	c.limits.assetBytes = 2
	if result := c.browserFetch(context.Background(), "5", "GET", "http://site.example.com/page", "script"); result["error"] == nil {
		t.Fatal("browser response size limit ignored")
	}
}

// Opt-in integration test: runs the real Node/Chromium worker, but every URL is
// served through the existing fixture's pinned-IP dial seam, never the Internet.
func TestBrowserRenderedClone(t *testing.T) {
	if os.Getenv("AGENTMIRROR_BROWSER_TEST") != "1" {
		t.Skip("set AGENTMIRROR_BROWSER_TEST=1 to run Chromium integration")
	}
	worker, err := filepath.Abs("../../scripts/browser-capture.mjs")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTMIRROR_BROWSER_WORKER", worker)
	c, hits := testSiteCloner(t, map[string]cloneFixture{
		"/start":     {status: 302, location: "/login"},
		"/login":     {contentType: "text/html", body: `<!doctype html><html><head><meta charset="utf-8"><title>SPA Login</title><link rel="stylesheet" href="/theme.css"></head><body><div id="app"></div><script src="/app.js"></script></body></html>`},
		"/theme.css": {contentType: "text/css", body: "body{background:rgb(232,241,255)} h1{color:navy}"},
		"/app.js":    {contentType: "application/javascript", body: `setTimeout(()=>{document.querySelector('#app').innerHTML='<h1>动态配置中心</h1><form action="/original-login"><input name="username"><button>登录</button></form><p>hash='+location.hash+'</p>';document.querySelector('input').value='demo';const s=document.createElement('style');document.head.append(s);s.sheet.insertRule('button { border-radius: 9px; }');fetch('http://127.0.0.1/secret').catch(()=>{});fetch('/mutation',{method:'POST',body:'must-not-send'}).catch(()=>{});},100);`},
	})
	c.render = c.captureBrowser
	stages := []string{}
	c.progress = func(stage, _ string) { stages = append(stages, stage) }
	result, err := c.clone(context.Background(), "http://site.example.com/start#/portal")
	if err != nil {
		t.Fatal(err)
	}
	html := string(cloneFiles(result)["index.html"])
	for _, expected := range []string{"动态配置中心", `value="demo"`, "#/portal", "border-radius: 9px", "data-am-form"} {
		if !strings.Contains(html, expected) {
			t.Fatalf("rendered capture missing %q: %s", expected, html)
		}
	}
	if strings.Contains(html, "<script") || strings.Contains(html, "action=") {
		t.Fatal("original application behavior retained")
	}
	if object(result["reference"])["capture_mode"] != "browser" {
		t.Fatal(result["reference"])
	}
	for _, hit := range hits() {
		if strings.Contains(hit, "/secret") || strings.Contains(hit, "/mutation") {
			t.Fatal("browser bypassed network guard: ", hit)
		}
	}
	t.Logf("rendered dynamic login into %d files; progress: %v", len(array(object(result["site"])["files"])), stages)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := c.captureBrowser(ctx, "http://site.example.com/login"); err == nil {
		t.Fatal("cancelled capture succeeded")
	}
}

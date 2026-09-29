package lab

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func siteExpectProblem(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if p := recover(); p == nil {
			t.Fatal("expected validation error")
		} else if _, ok := p.(problem); !ok {
			t.Fatalf("unexpected panic: %v", p)
		}
	}()
	fn()
}
func siteFixture() Doc {
	return Doc{"name": "test", "entry": "index.html", "spa": true, "files": []Doc{{"path": "index.html", "encoding": "utf8", "content": "<!doctype html><h1>hello</h1>"}, {"path": "assets/logo.png", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte{137, 80, 78, 71, 0})}}}
}
func TestSitePackageValidationAndServing(t *testing.T) {
	site := validateSite(siteFixture())
	for _, p := range []string{"/", "/dashboard"} {
		b, ct, ok := siteResponse(site, p)
		if !ok || !strings.Contains(string(b), "hello") || !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("entry/fallback failed %s: %s %t", p, ct, ok)
		}
	}
	for _, p := range []string{"/api/missing", "/nacos/v1/cs/configs", "/assets/missing.js", "/../index.html", "/assets/../../index.html", "/assets\\logo.png"} {
		if _, _, ok := siteResponse(site, p); ok {
			t.Errorf("unexpected SPA/resource response for %s", p)
		}
	}
	b, ct, ok := siteResponse(site, "/assets/logo.png")
	if !ok || !bytes.Equal(b, []byte{137, 80, 78, 71, 0}) || ct != "image/png" {
		t.Fatal("binary asset damaged")
	}
	for _, p := range []string{"../evil", "/absolute", "a/../b", "a\\b", "C:/file", "a//b", "a/./b"} {
		t.Run(p, func(t *testing.T) {
			d := siteFixture()
			array(d["files"])[0].(Doc)["path"] = p
			siteExpectProblem(t, func() { validateSite(d) })
		})
	}
	d := siteFixture()
	d["files"] = append(array(d["files"]), array(d["files"])[0])
	siteExpectProblem(t, func() { validateSite(d) })
	d = siteFixture()
	d["entry"] = "missing.html"
	siteExpectProblem(t, func() { validateSite(d) })
	d = siteFixture()
	array(d["files"])[0].(Doc)["content"] = strings.Repeat("a", maxSiteFileBytes+1)
	siteExpectProblem(t, func() { validateSite(d) })
}
func siteZIP(t *testing.T, files map[string]string, symlink bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, content := range files {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if symlink {
			h.SetMode(os.ModeSymlink | 0777)
		}
		w, e := z.CreateHeader(h)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write([]byte(content)); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return buf.Bytes()
}
func TestSiteZIPTraversalLinksAndBombLimits(t *testing.T) {
	z := siteZIP(t, map[string]string{"site-main/index.html": "<h1>imported</h1>", "site-main/assets/app.js": "let local=true"}, false)
	d := importSiteZIP(z, "import")
	if str(object(array(d["files"])[0])["path"]) == "site-main/index.html" {
		t.Fatal("wrapper not removed")
	}
	if _, _, ok := siteResponse(d, "/assets/app.js"); !ok {
		t.Fatal("multi-file ZIP import failed")
	}
	for _, p := range []string{"../escape", "/absolute", "a/../../escape", "a\\escape"} {
		z = siteZIP(t, map[string]string{p: "payload", "index.html": "ok"}, false)
		siteExpectProblem(t, func() { importSiteZIP(z, "bad") })
	}
	z = siteZIP(t, map[string]string{"index.html": "/etc/passwd"}, true)
	siteExpectProblem(t, func() { importSiteZIP(z, "link") })
	z = siteZIP(t, map[string]string{"index.html": strings.Repeat("a", maxSiteFileBytes+1)}, false)
	if len(z) > 100000 {
		t.Fatal("fixture not compressed")
	}
	siteExpectProblem(t, func() { importSiteZIP(z, "bomb") })
	z = siteZIP(t, map[string]string{"index.html": "ok", "a.txt": strings.Repeat("a", 3<<20), "b.txt": strings.Repeat("b", 3<<20), "c.txt": strings.Repeat("c", 3<<20)}, false)
	siteExpectProblem(t, func() { importSiteZIP(z, "total bomb") })
}
func TestSitePreviewInlinesResourcesAndBlocksNetwork(t *testing.T) {
	d := validateSite(Doc{"name": "preview", "entry": "index.html", "files": []Doc{{"path": "index.html", "content": `<link rel="stylesheet" href="assets/main.css"><div style="background:url(assets/logo.png)">test</div><img src="assets/logo.png"><script src="assets/app.js"></script><script src="https://external.invalid/a.js"></script>`}, {"path": "assets/main.css", "content": `@import "extra.css"; body{background:url('logo.png')}`}, {"path": "assets/extra.css", "content": `@import "main.css"; h1{color:teal}`}, {"path": "assets/app.js", "content": "window.localAsset=true;"}, {"path": "assets/logo.png", "content": base64.StdEncoding.EncodeToString([]byte{1, 2, 3}), "encoding": "base64"}}})
	h := sitePreviewHTML(d)
	for _, want := range []string{"Content-Security-Policy", "connect-src &#39;none&#39;", "form-action &#39;none&#39;", "data:image/png;base64,AQID", "h1{color:teal}", "window.localAsset=true"} {
		if !strings.Contains(h, want) {
			t.Errorf("preview missing %s", want)
		}
	}
	if strings.Contains(h, "external.invalid") || strings.Contains(h, `href="assets/main.css"`) || strings.Contains(h, "@import") {
		t.Fatal("unresolved resources remained")
	}
}
func TestSitePresetsUseStandaloneAssets(t *testing.T) {
	for _, d := range sitePresets() {
		validateSite(d)
		if len(array(d["files"])) < 3 {
			t.Fatal("preset lacks independent assets")
		}
		if !strings.Contains(sitePreviewHTML(d), "<style>") {
			t.Fatal("preset preview has no style")
		}
	}
}

func TestSitePreviewRemovesNavigationAndActiveHTML(t *testing.T) {
	d := validateSite(Doc{"name": "hostile-preview", "files": []Doc{{"path": "index.html", "content": `<META HTTP-EQUIV="refresh" content="0;url=https://leak.invalid"><base href="https://leak.invalid"><a href="https://leak.invalid" ping="https://leak.invalid">open</a><form action="https://leak.invalid"><input formaction="https://leak.invalid" type="submit"></form><iframe src="https://leak.invalid"></iframe><svg><a xlink:href="https://leak.invalid">bad</a><animate attributeName="href" to="https://leak.invalid"></animate></svg><div onclick="location='https://leak.invalid'">click</div><script>window.location='https://leak.invalid'</script>`}}})
	h := sitePreviewHTML(d)
	for _, bad := range []string{`HTTP-EQUIV="refresh"`, `http-equiv="refresh"`, `<base`, `href=`, `ping=`, `action=`, `<iframe`, `<svg`, `<animate`, `onclick=`} {
		if strings.Contains(h, bad) {
			t.Errorf("active preview HTML remained: %s", bad)
		}
	}
	if !strings.Contains(h, `type="application/x-agentmirror-preview"`) {
		t.Fatal("template scripts were not made inert")
	}
}

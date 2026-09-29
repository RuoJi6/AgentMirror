package lab

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	htmlparser "golang.org/x/net/html"
)

type cloneFixture struct {
	body, contentType, location string
	status                      int
	contentLength               int
}

func testSiteCloner(t *testing.T, fixtures map[string]cloneFixture) (*siteCloner, func() []string) {
	t.Helper()
	var mu sync.Mutex
	hits := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.Host+r.URL.RequestURI())
		mu.Unlock()
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("clone sent cookies or authentication")
		}
		fixture, exists := fixtures[r.URL.Path]
		if !exists {
			http.NotFound(w, r)
			return
		}
		if fixture.contentType != "" {
			w.Header().Set("Content-Type", fixture.contentType)
		}
		if fixture.contentLength > 0 {
			w.Header().Set("Content-Length", strconv.Itoa(fixture.contentLength))
		}
		w.Header().Set("Set-Cookie", "source-session=must-not-forward")
		if fixture.location != "" {
			w.Header().Set("Location", fixture.location)
		}
		status := fixture.status
		if status == 0 {
			status = 200
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(fixture.body))
	}))
	t.Cleanup(server.Close)
	cloner := newSiteCloner(nil)
	cloner.render = func(context.Context, string) (browserCapture, error) {
		return browserCapture{}, fmt.Errorf("browser deliberately disabled in static clone fixtures")
	}
	cloner.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	dialer := &net.Dialer{}
	cloner.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "93.184.216.34:80" {
			return nil, fmt.Errorf("dial was not pinned to the validated IP: %s", address)
		}
		return dialer.DialContext(ctx, network, server.Listener.Addr().String())
	}
	return cloner, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string{}, hits...)
	}
}

func cloneFiles(result Doc) map[string][]byte {
	files := map[string][]byte{}
	for _, item := range array(object(result["site"])["files"]) {
		file := object(item)
		files[str(file["path"])] = siteFileBytes(file)
	}
	return files
}

func TestWebsiteCloneRewritesDependenciesAndRetainsReference(t *testing.T) {
	image := "\x89PNG\r\n\x1a\n\x00\x01\xff"
	font := "wOF2\x00\xff\x02"
	cloner, hits := testSiteCloner(t, map[string]cloneFixture{
		"/start":                   {status: 302, location: "/dir/page"},
		"/dir/page":                {contentType: "text/html; charset=utf-8", body: `<!doctype html><html><head><title>配置中心</title><base href="/static/"><meta name="viewport" content="width=device-width"><meta http-equiv="refresh" content="0;url=https://evil.invalid/"><link rel="stylesheet" href="css/main.css"><link rel="preconnect" href="https://evil.invalid"><script src="https://evil.invalid/code.js"></script></head><body onload="fetch('https://evil.invalid')"><h1>配置列表</h1><a href="../detail" ping="https://evil.invalid">查看详情</a><form method="post" action="https://evil.invalid/submit"><input name="dataId" placeholder="配置ID"><button formaction="https://evil.invalid/save">保存</button></form><img src="img/logo.png" srcset="img/small.png 1x, https://cdn.example.com/large.png 2x"><img src="icon.svg"><div style="background:u\72l(img/bg.png)">详情</div><iframe src="https://evil.invalid/frame"></iframe><object data="https://evil.invalid/object"></object><svg><use href="#shape"></use><path id="shape" d="M0 0L5 5"></path><animate attributeName="href" to="https://evil.invalid"></animate></svg></body></html>`},
		"/static/css/main.css":     {contentType: "text/css", body: `@import "../nested/theme.css" screen; @font-face{font-family:test;src:url('../fonts/font.woff2')} body{background-image:image-set("../img/bg.png" 1x,url('../img/logo.png') 2x)}`},
		"/static/nested/theme.css": {contentType: "text/css", body: `@\69mport url('../css/main.css'); h1{color:teal;background:u\72l('../img/bg.png')}`},
		"/static/img/logo.png":     {contentType: "image/png", body: image},
		"/static/img/small.png":    {contentType: "image/png", body: image},
		"/static/img/bg.png":       {contentType: "image/png", body: image},
		"/large.png":               {contentType: "image/png", body: image},
		"/static/fonts/font.woff2": {contentType: "font/woff2", body: font},
		"/static/icon.svg":         {contentType: "image/svg+xml", body: `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(1)</script><foreignObject><iframe src="https://evil.invalid"/></foreignObject><path d="M0 0L2 2"/></svg>`},
	})
	stages := []string{}
	cloner.progress = func(stage, detail string) { stages = append(stages, stage) }
	result, err := cloner.clone(context.Background(), "http://site.example.com/start")
	if err != nil {
		t.Fatal(err)
	}
	files := cloneFiles(result)
	page := string(files["index.html"])
	for _, unexpected := range []string{"<script", "onload=", "<form", "formaction=", "<iframe", "<object", "<base", "http-equiv", "evil.invalid", `href="../detail"`, `src="img/`} {
		if strings.Contains(page, unexpected) {
			t.Fatalf("source behavior or unresolved URL remains (%s): %s", unexpected, page)
		}
	}
	for _, expected := range []string{"配置列表", `data-am-form="0"`, `data-am-link="0"`, `href="#"`, `src="/assets/`, `srcset="/assets/`, `name="viewport"`, `href="#shape"`} {
		if !strings.Contains(page, expected) {
			t.Fatalf("missing retained structure %s: %s", expected, page)
		}
	}
	seenFont, seenImage, seenCSS, seenSVG := false, false, 0, false
	for name, content := range files {
		switch {
		case strings.HasSuffix(name, ".woff2"):
			seenFont = bytes.Equal(content, []byte(font))
		case strings.HasSuffix(name, ".png"):
			seenImage = bytes.Equal(content, []byte(image))
		case strings.HasSuffix(name, ".css"):
			seenCSS++
			if strings.Contains(string(content), "../") || strings.Contains(string(content), `u\72l`) || !strings.Contains(string(content), "/assets/") {
				t.Fatal("CSS was not rewritten", name, string(content))
			}
		case strings.HasSuffix(name, ".svg"):
			seenSVG = true
			if strings.Contains(string(content), "script") || strings.Contains(string(content), "onload") || strings.Contains(string(content), "foreignObject") || !strings.Contains(string(content), "<path") {
				t.Fatal("SVG did not retain only static shapes", string(content))
			}
		}
	}
	if !seenFont || !seenImage || seenCSS != 2 || !seenSVG {
		t.Fatal("static dependencies or binary bytes lost", seenFont, seenImage, seenCSS, seenSVG)
	}
	reference := object(result["reference"])
	if reference["title"] != "配置中心" || reference["final_url"] != "http://site.example.com/dir/page" || !strings.Contains(str(reference["text"]), "配置列表") {
		t.Fatal("reference missing original page context", reference)
	}
	if object(array(reference["links"])[0])["href"] != "http://site.example.com/detail" || object(array(reference["forms"])[0])["action"] != "https://evil.invalid/submit" {
		t.Fatal("link/form references were not resolved against base", reference)
	}
	if len(array(result["warnings"])) == 0 || len(stages) != 3 {
		t.Fatal("missing progress or explicit interaction warnings", stages, result["warnings"])
	}
	for _, hit := range hits() {
		if strings.Contains(hit, "evil.invalid") || strings.HasSuffix(hit, "/detail") {
			t.Fatal("clone crawled navigation or active source code", hit)
		}
	}
	validateSite(object(result["site"]))
}

func TestWebsiteCloneBlocksNonPublicAddressesBeforeDial(t *testing.T) {
	for _, source := range []string{"file:///etc/passwd", "ftp://example.com/a", "http://user:pass@example.com/", "http://localhost/", "http://thing.local/", "http://metadata.google.internal/", "http://127.0.0.1/", "http://10.0.0.1/", "http://169.254.169.254/", "http://100.100.100.200/", "http://198.18.0.1/", "http://0.0.0.0/", "http://224.0.0.1/", "http://[::1]/", "http://[::ffff:127.0.0.1]/", "http://[fc00::1]/", "http://[fe80::1]/", "http://[64:ff9b::7f00:1]/", "http://[2002:7f00:1::]/", "http://public.example.com/", "http://mixed.example.com/"} {
		t.Run(source, func(t *testing.T) {
			cloner := newSiteCloner(nil)
			cloner.lookup = func(_ context.Context, host string) ([]net.IPAddr, error) {
				result := []net.IPAddr{{IP: net.ParseIP("192.168.0.1")}}
				if host == "mixed.example.com" {
					result = append(result, net.IPAddr{IP: net.ParseIP("93.184.216.34")})
				}
				return result, nil
			}
			cloner.dial = func(context.Context, string, string) (net.Conn, error) {
				t.Error("blocked address reached dial")
				return nil, fmt.Errorf("unexpected dial")
			}
			if result, err := cloner.clone(context.Background(), source); err == nil || result != nil {
				t.Fatal("non-public source accepted", source, result)
			}
		})
	}
	for _, ip := range []string{"8.8.8.8", "93.184.216.34", "2606:4700:4700::1111"} {
		if !clonePublicIP(net.ParseIP(ip)) {
			t.Fatal("public address incorrectly blocked", ip)
		}
	}
}

func TestWebsiteCloneRevalidatesRedirectAndDNSForEveryRequest(t *testing.T) {
	t.Run("redirect to metadata", func(t *testing.T) {
		cloner, hits := testSiteCloner(t, map[string]cloneFixture{"/": {status: 302, location: "http://169.254.169.254/latest/meta-data/"}})
		if _, err := cloner.clone(context.Background(), "http://site.example.com/"); err == nil || !strings.Contains(err.Error(), "非公网") {
			t.Fatal("metadata redirect accepted", err)
		}
		if len(hits()) != 1 {
			t.Fatal("redirect target was fetched", hits())
		}
	})
	t.Run("rebinding resource", func(t *testing.T) {
		cloner, hits := testSiteCloner(t, map[string]cloneFixture{"/": {contentType: "text/html", body: `<h1>Visible</h1><img src="/secret.png">`}})
		lookups := 0
		cloner.lookup = func(context.Context, string) ([]net.IPAddr, error) {
			lookups++
			ip := "93.184.216.34"
			if lookups > 1 {
				ip = "127.0.0.1"
			}
			return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
		}
		result, err := cloner.clone(context.Background(), "http://site.example.com/")
		if err != nil || len(hits()) != 1 || len(array(result["warnings"])) != 1 || strings.Contains(string(cloneFiles(result)["index.html"]), "secret.png") {
			t.Fatal("DNS rebinding was not isolated to a resource warning", err, hits(), result)
		}
	})
	t.Run("redirect budget", func(t *testing.T) {
		cloner, hits := testSiteCloner(t, map[string]cloneFixture{"/": {status: 302, location: "/"}})
		cloner.limits.redirects = 2
		if _, err := cloner.clone(context.Background(), "http://site.example.com/"); err == nil || len(hits()) != 3 {
			t.Fatal("redirect budget not enforced", err, hits())
		}
	})
}

func TestWebsiteCloneFailureAndBudgetContracts(t *testing.T) {
	t.Run("oversize page is not partial success", func(t *testing.T) {
		cloner, _ := testSiteCloner(t, map[string]cloneFixture{"/": {contentType: "text/html", body: "<h1>" + strings.Repeat("x", 1000) + "</h1>"}})
		cloner.limits.pageBytes = 100
		if result, err := cloner.clone(context.Background(), "http://site.example.com/"); err == nil || result != nil {
			t.Fatal("truncated page returned success", err, result)
		}
	})
	t.Run("asset failure is explicit and inert", func(t *testing.T) {
		cloner, _ := testSiteCloner(t, map[string]cloneFixture{
			"/":           {contentType: "text/html", body: `<h1>Retained</h1><img src="/big.png"><img src="/missing.png"><img src="/api.json"><link rel="stylesheet" href="/login.html">`},
			"/big.png":    {contentType: "image/png", body: strings.Repeat("x", 1000)},
			"/api.json":   {contentType: "application/json", body: `{"secret":"not an image"}`},
			"/login.html": {contentType: "text/html", body: "<script>source()</script>"},
		})
		cloner.limits.assetBytes = 100
		result, err := cloner.clone(context.Background(), "http://site.example.com/")
		if err != nil {
			t.Fatal(err)
		}
		if len(cloneFiles(result)) != 1 || len(array(result["warnings"])) != 4 || strings.Contains(string(cloneFiles(result)["index.html"]), "src=") {
			t.Fatal("failed or nonstatic resource retained", result)
		}
	})
	t.Run("script only page is not blank success", func(t *testing.T) {
		cloner, _ := testSiteCloner(t, map[string]cloneFixture{"/": {contentType: "text/html", body: `<title>SPA</title><div id="root"></div><script src="/app.js"></script>`}})
		if result, err := cloner.clone(context.Background(), "http://site.example.com/"); err == nil || result != nil {
			t.Fatal("script-only page returned blank success", err, result)
		}
	})
	t.Run("depth and count bound dependency walk", func(t *testing.T) {
		cloner, hits := testSiteCloner(t, map[string]cloneFixture{
			"/":        {contentType: "text/html", body: `<h1>Page</h1><link rel="stylesheet" href="/one.css"><img src="/image.png">`},
			"/one.css": {contentType: "text/css", body: `@import '/two.css';`},
			"/two.css": {contentType: "text/css", body: `@import '/three.css';`},
		})
		cloner.limits.depth = 1
		cloner.limits.files = 3
		result, err := cloner.clone(context.Background(), "http://site.example.com/")
		if err != nil || len(hits()) != 2 || len(array(result["warnings"])) < 2 || len(cloneFiles(result)) != 2 {
			t.Fatal("bounded dependency crawl failed", err, hits(), result)
		}
	})
	t.Run("cancellation before network", func(t *testing.T) {
		cloner, hits := testSiteCloner(t, map[string]cloneFixture{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := cloner.clone(ctx, "http://site.example.com/"); err == nil || len(hits()) != 0 {
			t.Fatal("cancelled clone made requests", err, hits())
		}
	})
}

func TestWebsiteCloneCSSDecodesEscapedReferences(t *testing.T) {
	cloner, hits := testSiteCloner(t, map[string]cloneFixture{
		"/image.png": {contentType: "image/png", body: "\x89PNG\r\n\x1a\n"},
		"/theme.css": {contentType: "text/css", body: "h1{color:teal}"},
	})
	base, _ := url.Parse("http://site.example.com/")
	value := cloner.css(context.Background(), `@\69mport "theme.css"; body{background:u\72l("im\61ge.png"); mask:URL( image.png );background-image:image-set("image.png" type("image/png") 1x, url(image.png) 2x);content:"literal url(http://inert.example.com)"}`, base, 0)
	if strings.Contains(value, "theme.css") || strings.Contains(value, "image.png") || !strings.Contains(value, `type("image/png")`) || !strings.Contains(value, `content:"literal url(http://inert.example.com)"`) {
		t.Fatal("escaped/functional CSS rewrite lost semantics or left source URL", value)
	}
	if len(hits()) != 3 { // The image used as a generic asset and image-set candidate has separate validation keys.
		t.Fatal("unexpected CSS dependency requests", hits())
	}
}

func TestWebsiteClonePreservesStaticCustomContainersAndConditionalStyles(t *testing.T) {
	cloner, _ := testSiteCloner(t, map[string]cloneFixture{
		"/":          {contentType: "text/html", body: `<html><head><link rel="stylesheet" href="/print.css" media="print" disabled></head><body><app-root><main><h1>服务端页面</h1><mark>搜索词</mark></main></app-root></body></html>`},
		"/print.css": {contentType: "text/css", body: "/*" + strings.Repeat("a", 1100) + `*/ h1::before{content:"中文"}`},
	})
	result, err := cloner.clone(context.Background(), "http://site.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	files := cloneFiles(result)
	page := string(files["index.html"])
	if !strings.Contains(page, "服务端页面") || !strings.Contains(page, "<mark>搜索词</mark>") || !strings.Contains(page, `media="print"`) || !strings.Contains(page, "disabled") {
		t.Fatal("lost static/custom container content or stylesheet condition", page)
	}
	for name, content := range files {
		if strings.HasSuffix(name, ".css") && !strings.Contains(string(content), `content:"中文"`) {
			t.Fatal("UTF-8 CSS after long ASCII prefix was misdecoded", string(content))
		}
	}
}

func TestWebsiteCloneDisablesDynamicCSSImageReferences(t *testing.T) {
	cloner := newSiteCloner(nil)
	cloner.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		t.Fatal("dynamic image function should not cause a request")
		return nil, nil
	}
	base, _ := url.Parse("https://public.example.com/")
	result := cloner.css(context.Background(), `:root{--picture:"https://source.example/tracker.png"} .hero{background-image:image-set(var(--picture) 1x);mask:image(attr(data-source))}`, base, 0)
	if strings.Contains(result, "image-set(") || strings.Contains(result, "image(attr") || len(cloner.warnings) != 2 {
		t.Fatal("dynamic image URL could be reintroduced by CSS variable substitution", result, cloner.warnings)
	}
}

func TestWebsiteCloneUsesRenderedDOMAndPreservesHashRoute(t *testing.T) {
	cloner, hits := testSiteCloner(t, map[string]cloneFixture{
		"/":              {contentType: "text/html", body: `<html><head><title>SPA</title></head><body><div id="app">Loading...</div><script src="/app.js"></script></body></html>`},
		"/static/bg.png": {contentType: "image/png", body: "\x89PNG\r\n\x1a\n"},
	})
	stages := []string{}
	cloner.progress = func(stage, detail string) { stages = append(stages, stage) }
	calls := 0
	cloner.render = func(ctx context.Context, source string) (browserCapture, error) {
		calls++
		if source != "http://site.example.com/#/login?redirect=%2Fhome" {
			t.Fatalf("SPA hash route was lost: %s", source)
		}
		return browserCapture{
			URL:      "http://site.example.com/console#/login",
			HTML:     `<html><head><title>配置中心登录</title><base href="/static/"><style data-runtime="cssom">.login{color:teal;background:url(bg.png)}</style></head><body><div id="app"><h1>登录控制台</h1><form action="https://original.example.com/login"><input name="username"><input type="password"><button onclick="submit()">登录</button></form></div><script src="/app.js"></script></body></html>`,
			Warnings: []string{"浏览器未下载一个可选字体"},
			Requests: 3, Bytes: 4096,
		}, nil
	}
	result, err := cloner.clone(context.Background(), "http://site.example.com/#/login?redirect=%2Fhome")
	if err != nil {
		t.Fatal(err)
	}
	page := string(cloneFiles(result)["index.html"])
	for _, expected := range []string{"登录控制台", "color:teal", `background:url("/assets/`, `data-am-form="0"`} {
		if !strings.Contains(page, expected) {
			t.Errorf("rendered content/style not retained (%s): %s", expected, page)
		}
	}
	for _, unwanted := range []string{"<script", "onclick", "original.example.com", "Loading...", "<base", "<form"} {
		if strings.Contains(page, unwanted) {
			t.Errorf("active source behavior retained (%s): %s", unwanted, page)
		}
	}
	reference := object(result["reference"])
	if reference["capture_mode"] != "browser" || reference["requested_url"] != "http://site.example.com/#/login?redirect=%2Fhome" || reference["final_url"] != "http://site.example.com/console#/login" {
		t.Fatal("browser source metadata missing", reference)
	}
	if !strings.Contains(str(reference["text"]), "登录控制台") || len(array(reference["forms"])) != 1 {
		t.Fatal("reference was built from the loading shell", reference)
	}
	if stats := object(result["stats"]); stats["browser_requests"] != 3 || stats["browser_downloaded_bytes"] != 4096 {
		t.Fatal("browser request statistics missing", stats)
	}
	if calls != 1 || len(hits()) != 2 || len(stages) != 5 || stages[1] != "render_page" || stages[2] != "render_ready" {
		t.Fatal("unexpected fallback sequence", calls, hits(), stages)
	}
	if warnings := fmt.Sprint(result["warnings"]); !strings.Contains(warnings, "可选字体") || !strings.Contains(warnings, "静态快照") {
		t.Fatal("browser limits were not surfaced", result["warnings"])
	}
}

func TestWebsiteCloneLeavesServerRenderedPageOnStaticPath(t *testing.T) {
	cloner, _ := testSiteCloner(t, map[string]cloneFixture{
		"/": {contentType: "text/html", body: `<html><body><div id="app"><h1>服务端页面</h1></div><script src="/hydrate.js"></script></body></html>`},
	})
	cloner.render = func(context.Context, string) (browserCapture, error) {
		t.Fatal("static content must not launch a browser")
		return browserCapture{}, nil
	}
	result, err := cloner.clone(context.Background(), "http://site.example.com/")
	if err != nil || object(result["reference"])["capture_mode"] != "static" {
		t.Fatal("server rendered fast path failed", err, result)
	}
}

func TestWebsiteCloneBrowserFailureContracts(t *testing.T) {
	for _, tc := range []struct {
		name, renderedHTML, message string
		renderErr                   error
	}{
		{"browser unavailable", "", "安装", errors.New("请安装浏览器运行依赖")},
		{"browser timeout", "", "deadline", context.DeadlineExceeded},
		{"empty rendering", `<div id="app"></div>`, "仍未出现", nil},
		{"loading rendering", `<div id="app">加载中...</div><script src="/app.js"></script>`, "仍未出现", nil},
		{"oversize rendering", "<h1>" + strings.Repeat("a", browserCaptureHTMLLimit) + "</h1>", "大小上限", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cloner, _ := testSiteCloner(t, map[string]cloneFixture{
				"/": {contentType: "text/html", body: `<div id="app"></div><script src="/app.js"></script>`},
			})
			cloner.render = func(context.Context, string) (browserCapture, error) {
				return browserCapture{HTML: tc.renderedHTML}, tc.renderErr
			}
			result, err := cloner.clone(context.Background(), "http://site.example.com/")
			if result != nil || err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatal("browser failure became blank success or lost reason", err, result)
			}
		})
	}
	t.Run("cancel during rendering", func(t *testing.T) {
		cloner, hits := testSiteCloner(t, map[string]cloneFixture{
			"/": {contentType: "text/html", body: `<div id="app"></div><script src="/app.js"></script>`},
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cloner.render = func(context.Context, string) (browserCapture, error) {
			cancel()
			return browserCapture{HTML: `<h1>Rendered</h1><img src="/image.png">`}, nil
		}
		result, err := cloner.clone(ctx, "http://site.example.com/")
		if result != nil || !errors.Is(err, context.Canceled) || len(hits()) != 1 {
			t.Fatal("cancelled browser result triggered more work", err, result, hits())
		}
	})
}

func TestWebsiteCloneDetectsDynamicShellWithoutMisclassifyingHydration(t *testing.T) {
	for _, tc := range []struct {
		html    string
		browser bool
	}{
		{`<div id="app"></div><script src="/main.js"></script>`, true},
		{`<noscript>Please enable JavaScript</noscript><div id="root"></div><script src="/main.js"></script>`, true},
		{`<div id="app">加载中...</div><script src="/main.js"></script>`, true},
		{`<div id="app">正在加载，请稍后...</div><script src="/main.js"></script>`, true},
		{`<div id="app">Loading 50%</div><script src="/main.js"></script>`, true},
		{`<div id="app"><svg><circle r="10"/></svg></div><script src="/main.js"></script>`, true},
		{`<div hidden>Injected placeholder</div><script src="/main.js"></script>`, true},
		{`<div style="display: none">Hidden placeholder</div><script src="/main.js"></script>`, true},
		{`<title>CSS-only or empty</title><div></div>`, true},
		{`<div id="app"><h1>Ready</h1></div><script src="/hydrate.js"></script>`, false},
		{`<div id="app"><input placeholder="Username"></div><script src="/hydrate.js"></script>`, false},
		{`<img src="/picture.png"><script src="/analytics.js"></script>`, false},
		{`<h1>Loading...</h1>`, false},
		{`<h1>Loading...</h1><script type="application/ld+json">{}</script>`, false},
	} {
		document, err := htmlparser.Parse(strings.NewReader(tc.html))
		if err != nil {
			t.Fatal(err)
		}
		if got := cloneNeedsBrowser(document); got != tc.browser {
			t.Errorf("cloneNeedsBrowser = %v, want %v: %s", got, tc.browser, tc.html)
		}
	}
}

func TestWebsiteClonePrioritizesStylesheetsAndBoundsFontDownloads(t *testing.T) {
	font := "wOF2\x00icon-font"
	inlineFont := "data:application/x-font-woff2;base64," + base64.StdEncoding.EncodeToString([]byte(font))
	var fontCSS strings.Builder
	fixtures := map[string]cloneFixture{
		"/base.css":  {contentType: "text/css", body: "body{background:#eee}"},
		"/login.css": {contentType: "text/css", body: ".login{display:grid;color:navy}"},
		"/logo.png":  {contentType: "image/png", body: "\x89PNG\r\n\x1a\n"},
	}
	for i := 0; i < 25; i++ {
		name := fmt.Sprintf("/large-%d.woff2", i)
		fontCSS.WriteString(fmt.Sprintf("@font-face{font-family:large%d;src:url('%s')}", i, name))
		fixtures[name] = cloneFixture{contentType: "application/x-font-woff2", body: "not consumed", contentLength: 2 << 20}
	}
	fontCSS.WriteString("@font-face{font-family:icons;src:url('" + inlineFont + "')}")
	fixtures["/"] = cloneFixture{contentType: "text/html", body: `<html><head><style>` + fontCSS.String() + `</style><link rel="stylesheet" href="/base.css"><link rel="stylesheet" href="/login.css"></head><body><h1 class="login">Login</h1><img src="/logo.png"></body></html>`}
	cloner, hits := testSiteCloner(t, fixtures)
	result, err := cloner.clone(context.Background(), "http://site.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	requests := hits()
	if len(requests) < 4 || requests[1] != "site.example.com/base.css" || requests[2] != "site.example.com/login.css" {
		t.Fatal("stylesheet prefetch was not ahead of font traversal", requests)
	}
	files := cloneFiles(result)
	cssCount, fontCount, imageCount := 0, 0, 0
	for name, content := range files {
		switch {
		case strings.HasSuffix(name, ".css"):
			cssCount++
		case strings.HasSuffix(name, ".woff2"):
			fontCount++
			if !bytes.Equal(content, []byte(font)) {
				t.Fatal("data WOFF2 changed")
			}
		case strings.HasSuffix(name, ".png"):
			imageCount++
		}
	}
	if cssCount != 2 || fontCount != 1 || imageCount != 1 || cloner.fontBytes != len(font) || cloner.bytes >= 32<<10 {
		t.Fatal("large unused fonts starved layout assets", cssCount, fontCount, imageCount, cloner.fontBytes, cloner.bytes)
	}
	page := string(files["index.html"])
	baseName, loginName := "", ""
	for name, content := range files {
		if strings.HasSuffix(name, ".css") && strings.Contains(string(content), "background:#eee") {
			baseName = "/" + name
		}
		if strings.HasSuffix(name, ".css") && strings.Contains(string(content), "display:grid") {
			loginName = "/" + name
		}
	}
	if baseName == "" || loginName == "" || strings.Index(page, baseName) > strings.Index(page, loginName) {
		t.Fatal("stylesheet cascade order changed", page)
	}
}

func TestWebsiteCloneFontBudgetCoversChunkedAndExtensionlessFonts(t *testing.T) {
	fixtures := map[string]cloneFixture{
		"/":                {contentType: "text/html", body: `<style>@font-face{font-family:a;src:url('/font-one')}@font-face{font-family:b;src:url('/font-two.ttf')}@font-face{font-family:c;src:url('/font-three.woff')}</style><link rel="stylesheet" href="/login.css"><h1>Login</h1><img src="/logo.png">`},
		"/font-one":        {contentType: "application/x-font-woff2", body: strings.Repeat("a", 1<<20)},
		"/font-two.ttf":    {contentType: "application/octet-stream", body: strings.Repeat("b", 1<<20)},
		"/font-three.woff": {contentType: "font/woff", body: strings.Repeat("c", 1<<20)},
		"/login.css":       {contentType: "text/css", body: "h1{color:navy}"},
		"/logo.png":        {contentType: "image/png", body: "\x89PNG\r\n\x1a\n"},
	}
	cloner, hits := testSiteCloner(t, fixtures)
	result, err := cloner.clone(context.Background(), "http://site.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if cloner.fontBytes > cloneFontTotalBytes+1 || cloner.fontBytes < cloneFontFileBytes || cloner.bytes >= cloner.limits.totalBytes {
		t.Fatal("unknown-length fonts escaped dedicated/global budgets", cloner.fontBytes, cloner.bytes)
	}
	for _, hit := range hits() {
		if strings.HasSuffix(hit, "/font-three.woff") {
			t.Fatal("requested font after its budget was spent")
		}
	}
	files := cloneFiles(result)
	if len(files) != 3 || !strings.Contains(string(files["index.html"]), `src="/assets/`) {
		t.Fatal("font budget discarded stylesheet or logo", result)
	}
}

func TestWebsiteCloneRejectsDeclaredOversizeWithoutConsumingBudget(t *testing.T) {
	cloner, _ := testSiteCloner(t, map[string]cloneFixture{
		"/big": {contentType: "text/plain", body: "unread", contentLength: 1 << 20},
	})
	if _, _, _, err := cloner.fetch(context.Background(), "http://site.example.com/big", 100); err == nil || cloner.bytes != 0 {
		t.Fatal("declared oversized response consumed download budget", err, cloner.bytes)
	}
}

func TestWebsiteCloneFontAliasesAndDataWarnings(t *testing.T) {
	for mimeType, extension := range map[string]string{
		"application/x-font-woff": ".woff", "application/x-font-woff2": ".woff2", "application/x-font-ttf": ".ttf", "application/x-font-truetype": ".ttf", "application/x-font-opentype": ".otf", "application/x-font-eot": ".eot",
	} {
		if _, got := cloneAssetType([]byte("font"), mimeType, nil, "asset"); got != extension {
			t.Errorf("font MIME alias %s was rejected: %s", mimeType, got)
		}
	}
	cloner := newSiteCloner(nil)
	base, _ := url.Parse("https://site.example.com/")
	data := "data:application/x-font-woff2;base64," + strings.Repeat("QUJD", cloneFontFileBytes)
	if got := cloner.resource(context.Background(), base, data, "asset", 1); got != "" {
		t.Fatal("oversized data font was kept")
	}
	warning := fmt.Sprint(cloner.warnings)
	if !strings.Contains(warning, "内嵌资源内容已省略") || strings.Contains(warning, "QUJD") || len(warning) > 500 || cloner.bytes != 0 {
		t.Fatal("warning leaked base64 data or invalid font consumed budget", warning, cloner.bytes)
	}
}

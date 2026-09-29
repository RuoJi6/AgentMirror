package lab

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	htmlparser "golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/net/idna"
)

// A clone is one anonymous document plus bounded static dependencies. Static
// pages are fetched directly; an isolated browser renders JavaScript shells.
// Both paths produce an inert snapshot rather than preserving source behavior.
const cloneTimeout = 90 * time.Second
const cloneFontTotalBytes = 512 << 10
const cloneFontFileBytes = 256 << 10

type siteCloneLimits struct {
	pageBytes, assetBytes, totalBytes, files, requests, depth, redirects int
}

type clonePrefetchedResource struct {
	body        []byte
	contentType string
	finalURL    *url.URL
	err         error
}

type siteCloner struct {
	lookup      func(context.Context, string) ([]net.IPAddr, error)
	dial        func(context.Context, string, string) (net.Conn, error)
	render      func(context.Context, string) (browserCapture, error)
	onRedirect  func(location *url.URL, rawLocation string)
	limits      siteCloneLimits
	progress    func(string, string)
	files       map[string]Doc
	stylesheets map[string]clonePrefetchedResource
	fontBytes   int
	assets      map[string]string
	warnings    []any
	bytes       int
	requests    int
	removed     int
}

func newSiteCloner(progress func(string, string)) *siteCloner {
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	c := &siteCloner{lookup: net.DefaultResolver.LookupIPAddr, dial: dialer.DialContext,
		limits:   siteCloneLimits{pageBytes: 1 << 20, assetBytes: 2 << 20, totalBytes: 6 << 20, files: 96, requests: 128, depth: 4, redirects: 5},
		progress: progress, files: map[string]Doc{}, stylesheets: map[string]clonePrefetchedResource{}, assets: map[string]string{}, warnings: []any{}}
	c.render = c.captureBrowser
	return c
}

func cloneWebsite(ctx context.Context, sourceURL string, progress func(stage, detail string)) (Doc, error) {
	return newSiteCloner(progress).clone(ctx, sourceURL)
}

func (c *siteCloner) report(stage, detail string) {
	if c.progress != nil {
		c.progress(stage, cloneTextLimit(detail, 1000))
	}
}

func (c *siteCloner) warn(detail string) {
	if len(c.warnings) < 100 {
		c.warnings = append(c.warnings, cloneTextLimit(detail, 1000))
	} else if len(c.warnings) == 100 {
		c.warnings = append(c.warnings, "其余资源警告已省略")
	}
}

func cloneTextLimit(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

func cloneURL(value string) (*url.URL, error) {
	if len(value) > 4096 || strings.ContainsAny(value, "\\\x00\r\n") {
		return nil, fmt.Errorf("网址无效或过长")
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Opaque != "" {
		return nil, fmt.Errorf("仅支持不含账号密码的公开 HTTP(S) 网址")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || strings.Contains(host, "%") {
		return nil, fmt.Errorf("网址主机无效")
	}
	if _, err := netip.ParseAddr(host); err != nil {
		host, err = idna.Lookup.ToASCII(host)
		if err != nil || len(host) > 253 || !strings.Contains(host, ".") || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
			return nil, fmt.Errorf("仅支持公开互联网主机")
		}
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("网址端口无效")
		}
	}
	u.Host = host
	if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	}
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u, nil
}

// Browser navigation must retain hash routes; network requests still use
// cloneURL, which intentionally removes fragments before fetching.
func clonePageURL(value string) (*url.URL, error) {
	u, err := cloneURL(value)
	if err != nil {
		return nil, err
	}
	original, _ := url.Parse(value)
	u.Fragment, u.RawFragment = original.Fragment, original.RawFragment
	return u, nil
}

var cloneBlockedNetworks = func() []netip.Prefix {
	var result []netip.Prefix
	for _, value := range strings.Fields("0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12 192.0.0.0/24 192.0.2.0/24 192.88.99.0/24 192.168.0.0/16 198.18.0.0/15 198.51.100.0/24 203.0.113.0/24 224.0.0.0/4 240.0.0.0/4 2001::/23 2001:db8::/32 2002::/16 3fff::/20") {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}()

func clonePublicIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || (!addr.Is4() && !netip.MustParsePrefix("2000::/3").Contains(addr)) {
		return false
	}
	for _, network := range cloneBlockedNetworks {
		if network.Contains(addr) {
			return false
		}
	}
	return true
}

func (c *siteCloner) fetch(ctx context.Context, source string, limit int) ([]byte, string, *url.URL, error) {
	return c.fetchBounded(ctx, source, limit, false)
}

func (c *siteCloner) fetchBounded(ctx context.Context, source string, limit int, fontBudget bool) ([]byte, string, *url.URL, error) {
	current := source
	for redirect := 0; redirect <= c.limits.redirects; redirect++ {
		if err := ctx.Err(); err != nil {
			return nil, "", nil, err
		}
		u, err := cloneURL(current)
		if err != nil {
			return nil, "", nil, err
		}
		addresses := []net.IPAddr{}
		if ip := net.ParseIP(u.Hostname()); ip != nil {
			addresses = append(addresses, net.IPAddr{IP: ip})
		} else {
			addresses, err = c.lookup(ctx, u.Hostname())
			if err != nil {
				return nil, "", nil, fmt.Errorf("解析主机失败: %w", err)
			}
		}
		if len(addresses) == 0 {
			return nil, "", nil, fmt.Errorf("主机没有可用地址")
		}
		for _, address := range addresses {
			if address.Zone != "" || !clonePublicIP(address.IP) {
				return nil, "", nil, fmt.Errorf("禁止访问非公网地址")
			}
		}
		if c.requests >= c.limits.requests {
			return nil, "", nil, fmt.Errorf("已达到请求数量上限")
		}
		c.requests++
		port := u.Port()
		if port == "" {
			port = "80"
			if u.Scheme == "https" {
				port = "443"
			}
		}
		// Every hop gets its own transport. Dial only the validated IPs; keep the
		// original hostname for HTTP Host and TLS certificate/SNI verification.
		transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 64 << 10,
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var last error
				for _, address := range addresses {
					conn, dialErr := c.dial(ctx, "tcp", net.JoinHostPort(address.IP.String(), port))
					if dialErr == nil {
						return conn, nil
					}
					last = dialErr
				}
				return nil, last
			}}
		client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		request.Header.Set("User-Agent", "AgentMirror-StaticClone/1.0")
		request.Header.Set("Accept", "text/html,text/css,image/*,font/*;q=0.9,*/*;q=0.5")
		response, err := client.Do(request)
		if err != nil {
			transport.CloseIdleConnections()
			return nil, "", nil, fmt.Errorf("抓取失败: %w", err)
		}
		if response.StatusCode >= 300 && response.StatusCode <= 399 {
			location, err := response.Location()
			response.Body.Close()
			transport.CloseIdleConnections()
			if err != nil {
				return nil, "", nil, fmt.Errorf("重定向缺少有效地址")
			}
			if c.onRedirect != nil {
				c.onRedirect(location, response.Header.Get("Location"))
			}
			current = location.String()
			continue
		}
		if response.StatusCode < 200 || response.StatusCode > 299 || response.StatusCode == http.StatusNoContent {
			response.Body.Close()
			transport.CloseIdleConnections()
			return nil, "", nil, fmt.Errorf("源站返回 HTTP %d", response.StatusCode)
		}
		isFont := fontBudget && cloneFontExtension(response.Header.Get("Content-Type"), u) != ""
		if isFont {
			limit = min(limit, cloneFontFileBytes, max(0, cloneFontTotalBytes-c.fontBytes))
		}
		remaining := min(limit, max(0, c.limits.totalBytes-c.bytes))
		if remaining <= 0 || response.ContentLength > int64(remaining) {
			response.Body.Close()
			transport.CloseIdleConnections()
			if isFont {
				return nil, "", nil, fmt.Errorf("字体超过单文件或字体总预算，保留系统字体回退")
			}
			return nil, "", nil, fmt.Errorf("响应 Content-Length 超过抓取字节限制，未读取响应体")
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, int64(remaining)+1))
		response.Body.Close()
		transport.CloseIdleConnections()
		c.bytes += len(body)
		if isFont {
			c.fontBytes += len(body)
		}
		if err != nil {
			return nil, "", nil, fmt.Errorf("响应读取失败: %w", err)
		}
		if len(body) > remaining {
			return nil, "", nil, fmt.Errorf("响应超过抓取字节限制，未保留截断内容")
		}
		return body, response.Header.Get("Content-Type"), u, nil
	}
	return nil, "", nil, fmt.Errorf("源站重定向次数过多")
}

func cloneDecodeText(body []byte, contentType string) (string, error) {
	_, params, _ := mime.ParseMediaType(contentType)
	if params["charset"] == "" && utf8.Valid(body) {
		return string(bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})), nil
	}
	reader, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return "", err
	}
	decoded, err := io.ReadAll(io.LimitReader(reader, maxSiteFileBytes+1))
	if err != nil || len(decoded) > maxSiteFileBytes {
		return "", fmt.Errorf("文本解码失败或超过文件上限")
	}
	return string(decoded), nil
}

func (c *siteCloner) clone(ctx context.Context, source string) (result Doc, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result, err = nil, caught(recovered)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()
	requested, err := clonePageURL(strings.TrimSpace(source))
	if err != nil {
		return nil, err
	}
	c.report("fetch_page", requested.String())
	body, contentType, finalURL, err := c.fetch(ctx, requested.String(), c.limits.pageBytes)
	if err != nil {
		return nil, fmt.Errorf("页面抓取失败: %w", err)
	}
	media, _, _ := mime.ParseMediaType(contentType)
	if media != "text/html" && media != "application/xhtml+xml" && !strings.HasPrefix(http.DetectContentType(body), "text/html") {
		return nil, fmt.Errorf("源地址未返回 HTML 页面")
	}
	text, err := cloneDecodeText(body, contentType)
	if err != nil {
		return nil, err
	}
	document, err := htmlparser.Parse(strings.NewReader(text))
	if err != nil {
		return nil, fmt.Errorf("HTML 解析失败: %w", err)
	}
	captureMode := "static"
	browserRequests, browserBytes := 0, 0
	if cloneNeedsBrowser(document) {
		c.report("render_page", "源页面依赖动态渲染，正在切换浏览器渲染")
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if c.render == nil {
			return nil, fmt.Errorf("源页面需要浏览器渲染，但浏览器采集器不可用")
		}
		capture, renderErr := c.render(ctx, requested.String())
		if renderErr != nil {
			return nil, fmt.Errorf("浏览器渲染失败: %w", renderErr)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(capture.HTML) > browserCaptureHTMLLimit {
			return nil, fmt.Errorf("浏览器渲染后的 HTML 超过页面大小上限")
		}
		if capture.URL == "" {
			capture.URL = requested.String()
		}
		finalURL, err = clonePageURL(capture.URL)
		if err != nil {
			return nil, fmt.Errorf("浏览器返回的页面地址无效: %w", err)
		}
		document, err = htmlparser.Parse(strings.NewReader(capture.HTML))
		if err != nil {
			return nil, fmt.Errorf("浏览器页面 HTML 解析失败: %w", err)
		}
		if cloneNeedsBrowser(document) {
			return nil, fmt.Errorf("浏览器渲染后仍未出现可保留的页面内容，可能仍在加载、需要登录或依赖未能访问的接口")
		}
		captureMode = "browser"
		browserRequests, browserBytes = capture.Requests, capture.Bytes
		for _, warning := range capture.Warnings {
			c.warn(warning)
		}
		c.warn("已通过浏览器渲染生成当前页面的静态快照；原站交互和后端请求需由生成器重新连接")
		c.report("render_ready", "动态页面已渲染，正在保存页面结构和样式")
	}
	base := cloneDocumentBase(document, finalURL)
	reference := cloneReference(document, base)
	reference["requested_url"], reference["final_url"] = requested.String(), finalURL.String()
	reference["capture_mode"] = captureMode
	c.report("fetch_assets", "下载并重写样式、图片和字体")
	c.prefetchStylesheets(ctx, document, base)
	c.cleanHTML(ctx, document, base, 0)
	if !cloneHasVisibleContent(document) {
		return nil, fmt.Errorf("页面清理后没有可保留的内容，请提供其它页面或静态站点包")
	}
	var rendered bytes.Buffer
	if err := htmlparser.Render(&rendered, document); err != nil {
		return nil, err
	}
	c.files["index.html"] = Doc{"path": "index.html", "encoding": "utf8", "content": rendered.String()}
	files := []Doc{c.files["index.html"]}
	paths := []string{}
	for name := range c.files {
		if name != "index.html" {
			paths = append(paths, name)
		}
	}
	sort.Strings(paths)
	for _, name := range paths {
		files = append(files, c.files[name])
	}
	if c.removed > 0 {
		c.warn(fmt.Sprintf("已移除 %d 处原站脚本、嵌入文档或主动网络行为；交互需由生成器重新连接", c.removed))
	}
	if ctx.Err() != nil {
		c.warn("抓取时间预算已用尽，部分静态资源可能缺失")
	}
	name := cloneTextLimit(strings.TrimSpace(str(reference["title"])), 70)
	if name == "" {
		name = cloneTextLimit(finalURL.Hostname(), 70)
	}
	site := validateSite(Doc{"name": name + " 克隆", "entry": "index.html", "spa": false, "source": cloneTextLimit("url:"+finalURL.String(), 1000), "files": files})
	total := 0
	for _, file := range files {
		total += len(siteFileBytes(file))
	}
	c.report("clone_ready", fmt.Sprintf("保留 %d 个文件，%d 条资源或交互警告", len(files), len(c.warnings)))
	return Doc{"site": site, "reference": reference, "warnings": c.warnings, "stats": Doc{"requests": c.requests, "downloaded_bytes": c.bytes, "browser_requests": browserRequests, "browser_downloaded_bytes": browserBytes, "files": len(files), "site_bytes": total, "removed_active_elements": c.removed}}, nil
}

func cloneDocumentBase(document *htmlparser.Node, finalURL *url.URL) *url.URL {
	base := finalURL
	found := false
	var walk func(*htmlparser.Node)
	walk = func(node *htmlparser.Node) {
		if node.Type == htmlparser.ElementNode && node.Data == "base" && !found {
			if value := cloneAttr(node, "href"); value != "" {
				if parsed, err := finalURL.Parse(value); err == nil {
					base, found = parsed, true
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return base
}

// A script plus an empty mount/loading placeholder is not a usable static
// page. Keep server-rendered content on the fast path even when it hydrates.
func cloneNeedsBrowser(document *htmlparser.Node) bool {
	hasScript, hasMount, hasControl, hasVisual := false, false, false, false
	var visible strings.Builder
	var walk func(*htmlparser.Node, bool)
	walk = func(node *htmlparser.Node, inBody bool) {
		if node.Type == htmlparser.ElementNode {
			tag := strings.ToLower(node.Data)
			if tag == "script" {
				typeName := strings.ToLower(strings.TrimSpace(cloneAttr(node, "type")))
				if typeName == "" || typeName == "module" || strings.Contains(typeName, "javascript") || strings.Contains(typeName, "ecmascript") {
					hasScript = hasScript || cloneAttr(node, "src") != "" || node.FirstChild != nil
				}
				return
			}
			if tag == "style" || tag == "noscript" || tag == "template" || tag == "iframe" || tag == "object" || tag == "embed" {
				return
			}
			if cloneNodeHidden(node) {
				return
			}
			inBody = inBody || tag == "body"
			if inBody {
				id := strings.ToLower(cloneAttr(node, "id"))
				hasMount = hasMount || id == "app" || id == "root" || id == "__next" || id == "__nuxt" || tag == "app-root"
				hasControl = hasControl || (tag == "input" && strings.ToLower(cloneAttr(node, "type")) != "hidden") || tag == "select" || tag == "textarea"
				hasVisual = hasVisual || tag == "svg" || (tag == "img" && cloneAttr(node, "src") != "") || strings.Contains(cloneAttr(node, "style"), "url(")
			}
		}
		if inBody && node.Type == htmlparser.TextNode && visible.Len() < 4096 {
			visible.WriteString(node.Data)
			visible.WriteByte(' ')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, inBody)
		}
	}
	walk(document, false)
	text := strings.Trim(strings.ToLower(strings.Join(strings.Fields(visible.String()), " ")), " .…。!！:\t\r\n")
	placeholderText := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsDigit(r) {
			return -1
		}
		return r
	}, text)
	loading := false
	for _, placeholder := range []string{"loading", "loadingpage", "loadingapp", "loadingapplication", "pleasewait", "loadingpleasewait", "正在加载", "加载中", "页面加载中", "正在载入", "载入中", "请稍候", "请稍后", "请稍等", "正在加载请稍候", "正在加载请稍后", "加载中请稍候", "加载中请稍后"} {
		loading = loading || placeholderText == placeholder
	}
	if text == "" && !hasControl && !hasVisual {
		return true
	}
	return hasScript && !hasControl && (loading || (text == "" && hasMount))
}

func cloneNodeHidden(node *htmlparser.Node) bool {
	for _, attr := range node.Attr {
		if attr.Key == "hidden" || (attr.Key == "type" && node.Data == "input" && strings.EqualFold(attr.Val, "hidden")) {
			return true
		}
	}
	style := strings.ToLower(strings.Join(strings.Fields(cloneAttr(node, "style")), ""))
	return strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden")
}

// Fetch document stylesheets before traversing their font/image dependencies.
// This changes network priority only; the original DOM order and CSS cascade
// remain intact. Failed downloads are cached too, avoiding duplicate requests.
func (c *siteCloner) prefetchStylesheets(ctx context.Context, document *htmlparser.Node, base *url.URL) {
	var walk func(*htmlparser.Node)
	walk = func(node *htmlparser.Node) {
		if node.Type == htmlparser.ElementNode && strings.EqualFold(node.Data, "link") {
			rel := strings.ToLower(cloneAttr(node, "rel"))
			if rel == "stylesheet" || rel == "alternate stylesheet" {
				u, err := base.Parse(cloneAttr(node, "href"))
				if err == nil && (u.Scheme == "http" || u.Scheme == "https") {
					u.Fragment = ""
					key := "css:" + u.String()
					if _, exists := c.stylesheets[key]; !exists && len(c.stylesheets) < c.limits.files {
						body, contentType, finalURL, err := c.fetch(ctx, u.String(), c.limits.assetBytes)
						c.stylesheets[key] = clonePrefetchedResource{body, contentType, finalURL, err}
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
}

func cloneAttr(node *htmlparser.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func cloneReference(document *htmlparser.Node, base *url.URL) Doc {
	links, forms := []any{}, []any{}
	var title, text strings.Builder
	var nodeText func(*htmlparser.Node) string
	nodeText = func(node *htmlparser.Node) string {
		if node.Type == htmlparser.TextNode {
			return node.Data
		}
		var out strings.Builder
		for child := node.FirstChild; child != nil && out.Len() < 2000; child = child.NextSibling {
			out.WriteString(nodeText(child))
		}
		return cloneTextLimit(strings.Join(strings.Fields(out.String()), " "), 500)
	}
	resolve := func(value string) string {
		if u, err := base.Parse(value); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil {
			return cloneTextLimit(u.String(), 1000)
		}
		return ""
	}
	var walk func(*htmlparser.Node, bool)
	walk = func(node *htmlparser.Node, inHead bool) {
		if node.Type == htmlparser.ElementNode {
			switch node.Data {
			case "script", "style", "template":
				return
			case "head":
				inHead = true
			case "title":
				title.WriteString(nodeText(node))
			case "a":
				if len(links) < 100 && cloneAttr(node, "href") != "" {
					links = append(links, Doc{"text": nodeText(node), "href": resolve(cloneAttr(node, "href"))})
					node.Attr = append(node.Attr, htmlparser.Attribute{Key: "data-am-link", Val: strconv.Itoa(len(links) - 1)})
				}
			case "form":
				if len(forms) < 32 {
					fields := []any{}
					var scan func(*htmlparser.Node)
					scan = func(n *htmlparser.Node) {
						if n.Type == htmlparser.ElementNode && (n.Data == "input" || n.Data == "textarea" || n.Data == "select" || n.Data == "button") && len(fields) < 64 {
							fields = append(fields, Doc{"tag": n.Data, "name": cloneTextLimit(cloneAttr(n, "name"), 200), "type": cloneTextLimit(cloneAttr(n, "type"), 100), "placeholder": cloneTextLimit(cloneAttr(n, "placeholder"), 200)})
						}
						for child := n.FirstChild; child != nil; child = child.NextSibling {
							scan(child)
						}
					}
					scan(node)
					forms = append(forms, Doc{"action": resolve(cloneAttr(node, "action")), "method": strings.ToUpper(cloneAttr(node, "method")), "fields": fields})
					node.Attr = append(node.Attr, htmlparser.Attribute{Key: "data-am-form", Val: strconv.Itoa(len(forms) - 1)})
				}
			}
		}
		if node.Type == htmlparser.TextNode && !inHead && text.Len() < 24000 {
			text.WriteString(cloneTextLimit(strings.Join(strings.Fields(node.Data), " "), 2000) + " ")
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, inHead)
		}
	}
	walk(document, false)
	return Doc{"title": cloneTextLimit(title.String(), 500), "text": cloneTextLimit(strings.TrimSpace(text.String()), 20000), "links": links, "forms": forms}
}

func cloneHasVisibleContent(document *htmlparser.Node) bool {
	var walk func(*htmlparser.Node, bool) bool
	walk = func(node *htmlparser.Node, inBody bool) bool {
		if node.Type == htmlparser.ElementNode {
			if node.Data == "head" || node.Data == "style" || node.Data == "script" || node.Data == "template" || cloneNodeHidden(node) {
				return false
			}
			inBody = inBody || node.Data == "body"
			if inBody && (node.Data == "svg" || node.Data == "input" || (node.Data == "img" && cloneAttr(node, "src") != "") || strings.Contains(cloneAttr(node, "style"), "url(")) {
				return true
			}
		}
		if inBody && node.Type == htmlparser.TextNode && strings.TrimSpace(node.Data) != "" {
			return true
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if walk(child, inBody) {
				return true
			}
		}
		return false
	}
	return walk(document, false)
}

var cloneAllowedTags = func() map[string]bool {
	result := map[string]bool{}
	for _, tag := range strings.Fields("html head body title style link meta div span p h1 h2 h3 h4 h5 h6 main header footer nav aside section article details summary a ul ol li dl dt dd table thead tbody tfoot tr th td caption colgroup col pre code blockquote hr br em strong b i u s small sub sup mark abbr q cite samp kbd var wbr bdi bdo ruby rt rp del ins label input select option optgroup textarea button fieldset legend img picture source video audio figure figcaption progress meter address time form noscript svg g path circle ellipse rect line polyline polygon text tspan defs symbol use image clipPath mask linearGradient radialGradient stop pattern filter feGaussianBlur feOffset feBlend feColorMatrix feComposite feFlood feMerge feMergeNode") {
		result[strings.ToLower(tag)] = true
	}
	return result
}()

var cloneAllowedAttrs = func() map[string]bool {
	result := map[string]bool{}
	for _, attr := range strings.Fields("id class title width height alt role hidden dir lang tabindex name type value placeholder disabled readonly checked selected multiple required min max step size rows cols colspan rowspan controls muted loop preload open datetime for loading decoding media viewbox preserveaspectratio d x y x1 x2 y1 y2 cx cy r rx ry points fill stroke stroke-width stroke-linecap stroke-linejoin stroke-dasharray stroke-dashoffset fill-rule clip-rule opacity fill-opacity stroke-opacity transform xmlns version offset stop-color stop-opacity gradientunits gradienttransform patternunits patterncontentunits patterntransform clippathunits maskunits maskcontentunits stddeviation dx dy in in2 result mode operator flood-color flood-opacity values viewtarget") {
		result[attr] = true
	}
	return result
}()

func (c *siteCloner) cleanHTML(ctx context.Context, document *htmlparser.Node, base *url.URL, depth int) {
	var clean func(*htmlparser.Node)
	clean = func(node *htmlparser.Node) {
		for child := node.FirstChild; child != nil; {
			next := child.NextSibling
			dangerous := strings.Contains(" script iframe frame frameset object embed applet base template foreignobject math ", " "+strings.ToLower(child.Data)+" ")
			if child.Type == htmlparser.CommentNode || (child.Type == htmlparser.ElementNode && dangerous) {
				if child.Type == htmlparser.ElementNode {
					c.removed++
				}
				node.RemoveChild(child)
			} else {
				if child.Type == htmlparser.ElementNode && !cloneAllowedTags[strings.ToLower(child.Data)] {
					// Server-rendered custom elements frequently contain the whole
					// page. Retain their static descendants without custom behavior.
					child.Data, child.DataAtom, child.Namespace = "div", 0, ""
				}
				clean(child)
			}
			child = next
		}
		if node.Type != htmlparser.ElementNode {
			return
		}
		tag := strings.ToLower(node.Data)
		if tag == "meta" {
			if strings.EqualFold(cloneAttr(node, "name"), "viewport") {
				node.Attr = []htmlparser.Attribute{{Key: "name", Val: "viewport"}, {Key: "content", Val: cloneAttr(node, "content")}}
			} else {
				node.Attr = []htmlparser.Attribute{{Key: "charset", Val: "utf-8"}}
			}
			return
		}
		if tag == "link" {
			rel := strings.ToLower(cloneAttr(node, "rel"))
			kind := ""
			if rel == "stylesheet" || rel == "alternate stylesheet" {
				kind = "css"
			} else if rel == "icon" || rel == "shortcut icon" || rel == "apple-touch-icon" {
				kind = "image"
			}
			if kind == "" {
				node.Parent.RemoveChild(node)
				c.removed++
				return
			}
			ref := c.resource(ctx, base, cloneAttr(node, "href"), kind, depth+1)
			if ref == "" {
				node.Parent.RemoveChild(node)
				return
			}
			attrs := []htmlparser.Attribute{{Key: "rel", Val: rel}, {Key: "href", Val: ref}}
			for _, attr := range node.Attr {
				if attr.Key == "media" || attr.Key == "disabled" || attr.Key == "title" {
					attrs = append(attrs, attr)
				}
			}
			node.Attr = attrs
			return
		}
		if tag == "style" {
			var value strings.Builder
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == htmlparser.TextNode {
					value.WriteString(child.Data)
				}
			}
			for node.FirstChild != nil {
				node.RemoveChild(node.FirstChild)
			}
			node.AppendChild(&htmlparser.Node{Type: htmlparser.TextNode, Data: c.css(ctx, value.String(), base, depth)})
		}
		attrs := []htmlparser.Attribute{}
		for _, attr := range node.Attr {
			key := strings.ToLower(attr.Key)
			if strings.HasPrefix(key, "on") || key == "action" || key == "formaction" || key == "ping" || key == "srcdoc" {
				c.removed++
				continue
			}
			if key == "style" || key == "fill" || key == "stroke" || key == "filter" || key == "clip-path" || key == "mask" {
				attr.Val = c.css(ctx, attr.Val, base, depth)
				attrs = append(attrs, attr)
				continue
			}
			if key == "srcset" && (tag == "img" || tag == "source") {
				if value := c.srcset(ctx, attr.Val, base, depth); value != "" {
					attr.Val = value
					attrs = append(attrs, attr)
				}
				continue
			}
			if key == "src" || key == "poster" {
				if tag == "img" || tag == "source" || tag == "video" || tag == "audio" || (tag == "input" && cloneAttr(node, "type") == "image") {
					kind := "image"
					if (tag == "video" || tag == "audio" || tag == "source") && key == "src" {
						kind = "asset"
					}
					if value := c.resource(ctx, base, attr.Val, kind, depth+1); value != "" {
						attr.Val = value
						attrs = append(attrs, attr)
					}
				}
				continue
			}
			if key == "href" {
				if tag == "a" {
					if !strings.HasPrefix(attr.Val, "#") {
						attr.Val = "#"
					}
					attrs = append(attrs, attr)
				} else if tag == "use" && strings.HasPrefix(attr.Val, "#") {
					attrs = append(attrs, attr)
				} else if tag == "image" {
					if value := c.resource(ctx, base, attr.Val, "image", depth+1); value != "" {
						attr.Val = value
						attrs = append(attrs, attr)
					}
				}
				continue
			}
			if cloneAllowedAttrs[key] || strings.HasPrefix(key, "aria-") || strings.HasPrefix(key, "data-") {
				attrs = append(attrs, attr)
			}
		}
		if tag == "form" || tag == "noscript" {
			node.Data = "div"
			node.DataAtom = 0
		}
		if tag == "button" {
			for i := range attrs {
				if attrs[i].Key == "type" {
					attrs[i].Val = "button"
				}
			}
			attrs = append(attrs, htmlparser.Attribute{Key: "type", Val: "button"})
		}
		node.Attr = attrs
	}
	clean(document)
}

func (c *siteCloner) srcset(ctx context.Context, value string, base *url.URL, depth int) string {
	// URLs end at whitespace; commas inside data URLs are therefore preserved.
	result := []string{}
	for value = strings.TrimSpace(value); value != ""; {
		value = strings.TrimLeft(value, " ,\t\r\n")
		if value == "" {
			break
		}
		i := strings.IndexFunc(value, unicode.IsSpace)
		if i < 0 {
			i = len(value)
		}
		ref := value[:i]
		value = value[i:]
		descriptor := ""
		if strings.HasSuffix(ref, ",") {
			ref = strings.TrimRight(ref, ",")
		} else {
			end := strings.IndexByte(value, ',')
			if end < 0 {
				end = len(value)
			}
			descriptor, value = strings.TrimSpace(value[:end]), value[end:]
		}
		valid := true
		for _, r := range descriptor {
			valid = valid && (r >= '0' && r <= '9' || strings.ContainsRune(".wxh ", r))
		}
		if rewritten := c.resource(ctx, base, ref, "image", depth+1); rewritten != "" && valid {
			result = append(result, strings.TrimSpace(rewritten+" "+descriptor))
		}
	}
	return strings.Join(result, ", ")
}

func cloneFontExtension(header string, source *url.URL) string {
	media := strings.ToLower(strings.TrimSpace(strings.SplitN(header, ";", 2)[0]))
	aliases := map[string]string{
		"font/woff": ".woff", "application/font-woff": ".woff", "application/x-font-woff": ".woff",
		"font/woff2": ".woff2", "application/font-woff2": ".woff2", "application/x-font-woff2": ".woff2",
		"font/ttf": ".ttf", "font/sfnt": ".ttf", "application/font-sfnt": ".ttf", "application/x-font-ttf": ".ttf", "application/x-font-truetype": ".ttf",
		"font/otf": ".otf", "application/x-font-otf": ".otf", "application/x-font-opentype": ".otf",
		"application/vnd.ms-fontobject": ".eot", "application/x-font-eot": ".eot",
	}
	if extension := aliases[media]; extension != "" {
		return extension
	}
	if source != nil {
		extension := strings.ToLower(path.Ext(source.Path))
		switch extension {
		case ".woff", ".woff2", ".ttf", ".otf", ".eot":
			return extension
		}
	}
	return ""
}

func cloneResourceLabel(value string) string {
	if strings.HasPrefix(strings.ToLower(value), "data:") {
		header := strings.SplitN(value, ",", 2)[0]
		return cloneTextLimit(header, 120) + ",…（内嵌资源内容已省略）"
	}
	return cloneTextLimit(value, 300)
}

func cloneAssetType(body []byte, header string, source *url.URL, kind string) (string, string) {
	media, _, _ := mime.ParseMediaType(header)
	if kind == "css" && (media == "text/css" || media == "text/plain" || media == "" || media == "application/octet-stream") && !strings.HasPrefix(http.DetectContentType(body), "text/html") {
		return "css", ".css"
	}
	if extension := cloneFontExtension(header, source); extension != "" && kind == "asset" {
		if media == "" || media == "application/octet-stream" || cloneFontExtension(header, nil) != "" {
			return "font/" + strings.TrimPrefix(extension, "."), extension
		}
	}
	extensions := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp", "image/avif": ".avif", "image/svg+xml": ".svg", "image/x-icon": ".ico", "image/vnd.microsoft.icon": ".ico", "font/woff": ".woff", "font/woff2": ".woff2", "font/ttf": ".ttf", "font/otf": ".otf", "application/font-woff": ".woff", "application/vnd.ms-fontobject": ".eot", "audio/mpeg": ".mp3", "audio/ogg": ".ogg", "video/mp4": ".mp4", "video/webm": ".webm"}
	if (media == "application/octet-stream" || media == "") && source != nil {
		media, _, _ = mime.ParseMediaType(mime.TypeByExtension(strings.ToLower(path.Ext(source.Path))))
	}
	if extension := extensions[media]; extension != "" && (kind != "image" || strings.HasPrefix(media, "image/")) && kind != "css" {
		return media, extension
	}
	return "", ""
}

func (c *siteCloner) resource(ctx context.Context, base *url.URL, reference, kind string, depth int) string {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return ""
	}
	if strings.HasPrefix(reference, "#") {
		return reference
	}
	u, err := base.Parse(reference)
	if err != nil {
		c.warn("资源地址无效: " + cloneResourceLabel(reference))
		return ""
	}
	fragment := u.Fragment
	u.Fragment = ""
	key := kind + ":" + u.String()
	if name, exists := c.assets[key]; exists {
		if name != "" && fragment != "" {
			return name + "#" + url.PathEscape(fragment)
		}
		return name
	}
	c.assets[key] = ""
	prefetched, cached := c.stylesheets[key]
	if depth > c.limits.depth || len(c.assets) >= c.limits.files || (!cached && c.bytes >= c.limits.totalBytes) || ctx.Err() != nil {
		c.warn("资源未下载，已达深度、数量、时间或字节上限: " + cloneResourceLabel(u.String()))
		return ""
	}
	font := cloneFontExtension("", u) != ""
	assetLimit := c.limits.assetBytes
	if u.Scheme == "data" {
		font = cloneFontExtension(strings.TrimPrefix(strings.SplitN(reference, ",", 2)[0], "data:"), nil) != ""
	}
	if font {
		assetLimit = min(assetLimit, cloneFontFileBytes, max(0, cloneFontTotalBytes-c.fontBytes))
		if assetLimit <= 0 {
			c.warn("已达到字体下载预算，保留系统字体回退: " + cloneResourceLabel(reference))
			return ""
		}
	}
	var body []byte
	var contentType string
	finalURL := u
	if cached {
		body, contentType, finalURL, err = prefetched.body, prefetched.contentType, prefetched.finalURL, prefetched.err
	} else if u.Scheme == "data" {
		parts := strings.SplitN(reference, ",", 2)
		if len(parts) != 2 || len(reference) > assetLimit*3+1024 {
			err = fmt.Errorf("内嵌资源过大或格式无效")
		} else {
			contentType = strings.TrimPrefix(parts[0], "data:")
			if strings.HasSuffix(contentType, ";base64") {
				contentType = strings.TrimSuffix(contentType, ";base64")
				if base64.StdEncoding.DecodedLen(len(parts[1])) > assetLimit+2 {
					err = fmt.Errorf("内嵌资源超过字节上限")
				} else {
					body, err = base64.StdEncoding.DecodeString(parts[1])
				}
			} else {
				var decoded string
				decoded, err = url.PathUnescape(parts[1])
				body = []byte(decoded)
			}
			if len(body) > assetLimit || len(body)+c.bytes > c.limits.totalBytes {
				err = fmt.Errorf("内嵌资源超过字节上限")
			}
			if err == nil {
				c.bytes += len(body)
				if font {
					c.fontBytes += len(body)
				}
			}
		}
	} else {
		body, contentType, finalURL, err = c.fetchBounded(ctx, u.String(), assetLimit, true)
	}
	if err != nil {
		c.warn("资源未下载: " + cloneResourceLabel(u.String()) + " (" + err.Error() + ")")
		return ""
	}
	media, extension := cloneAssetType(body, contentType, finalURL, kind)
	if extension == "" {
		c.warn("跳过非静态资源或类型不符的响应: " + cloneResourceLabel(u.String()))
		return ""
	}
	digest := sha256.Sum256([]byte(key))
	name := "assets/" + hex.EncodeToString(digest[:12]) + extension
	c.assets[key] = "/" + name
	if media == "css" || extension == ".svg" {
		text, decodeErr := cloneDecodeText(body, contentType)
		if decodeErr != nil {
			c.assets[key] = ""
			c.warn("资源文本解码失败: " + cloneResourceLabel(u.String()))
			return ""
		}
		if media == "css" {
			body = []byte(c.css(ctx, text, finalURL, depth))
		} else {
			document, parseErr := cloneSVGDocument(text)
			if parseErr != nil {
				c.assets[key] = ""
				c.warn("SVG 资源格式无效: " + cloneResourceLabel(u.String()))
				return ""
			}
			c.cleanHTML(ctx, document, finalURL, depth)
			var svg *htmlparser.Node
			var find func(*htmlparser.Node)
			find = func(node *htmlparser.Node) {
				if node.Type == htmlparser.ElementNode && node.Data == "svg" && svg == nil {
					svg = node
				}
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					find(child)
				}
			}
			find(document)
			if svg == nil {
				c.assets[key] = ""
				c.warn("SVG 资源无有效图形: " + cloneResourceLabel(u.String()))
				return ""
			}
			var rendered bytes.Buffer
			if err := htmlparser.Render(&rendered, svg); err != nil {
				c.assets[key] = ""
				return ""
			}
			body = rendered.Bytes()
		}
	}
	if len(body) > maxSiteFileBytes {
		c.assets[key] = ""
		c.warn("改写后资源超过文件上限: " + cloneResourceLabel(u.String()))
		return ""
	}
	encoding, content := "base64", base64.StdEncoding.EncodeToString(body)
	if extension == ".css" || extension == ".svg" {
		encoding, content = "utf8", string(body)
	}
	c.files[name] = Doc{"path": name, "encoding": encoding, "content": content}
	if fragment != "" {
		return "/" + name + "#" + url.PathEscape(fragment)
	}
	return "/" + name
}

func cloneSVGDocument(text string) (*htmlparser.Node, error) {
	// SVG is XML: HTML parsing would treat a self-closing foreign iframe as an
	// open HTML element and swallow subsequent, otherwise valid SVG shapes.
	decoder := xml.NewDecoder(strings.NewReader(text))
	decoder.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	document := &htmlparser.Node{Type: htmlparser.DocumentNode}
	stack := []*htmlparser.Node{document}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return document, nil
		}
		if err != nil {
			return nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			node := &htmlparser.Node{Type: htmlparser.ElementNode, Data: value.Name.Local, Namespace: "svg"}
			for _, attr := range value.Attr {
				namespace := ""
				if attr.Name.Space == "http://www.w3.org/1999/xlink" {
					namespace = "xlink"
				}
				node.Attr = append(node.Attr, htmlparser.Attribute{Namespace: namespace, Key: attr.Name.Local, Val: attr.Value})
			}
			stack[len(stack)-1].AppendChild(node)
			stack = append(stack, node)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			stack[len(stack)-1].AppendChild(&htmlparser.Node{Type: htmlparser.TextNode, Data: string(value)})
		}
	}
}

// CSS identifiers and strings are decoded before deciding whether they contain
// a URL. This covers escaped spellings such as u\72l and @\69mport; a regular
// expression alone would leave those original network references active.
func cloneCSSName(text string, start int) (string, int) {
	var out strings.Builder
	i := start
	for i < len(text) {
		b := text[i]
		if b == '\\' {
			value, end := cloneCSSEscape(text, i)
			out.WriteString(value)
			i = end
		} else if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_' || b >= 128 {
			out.WriteByte(b)
			i++
		} else {
			break
		}
	}
	return out.String(), i
}

func cloneCSSEscape(text string, start int) (string, int) {
	i := start + 1
	if i >= len(text) {
		return "", i
	}
	end := i
	for end < len(text) && end-i < 6 && strings.ContainsRune("0123456789abcdefABCDEF", rune(text[end])) {
		end++
	}
	if end > i {
		value, _ := strconv.ParseInt(text[i:end], 16, 32)
		if end < len(text) && strings.ContainsRune(" \t\n\r\f", rune(text[end])) {
			end++
		}
		if value == 0 || value > utf8.MaxRune || value >= 0xd800 && value <= 0xdfff {
			value = utf8.RuneError
		}
		return string(rune(value)), end
	}
	if text[i] == '\r' || text[i] == '\n' || text[i] == '\f' {
		return "", i + 1
	}
	return text[i : i+1], i + 1
}

func cloneCSSString(text string, start int) (string, int, bool) {
	quote := text[start]
	var out strings.Builder
	for i := start + 1; i < len(text); {
		if text[i] == quote {
			return out.String(), i + 1, true
		}
		if text[i] == '\\' {
			value, end := cloneCSSEscape(text, i)
			out.WriteString(value)
			i = end
		} else {
			out.WriteByte(text[i])
			i++
		}
	}
	return "", len(text), false
}

func cloneCSSSpace(text string, i int) int {
	for i < len(text) {
		if strings.ContainsRune(" \t\r\n\f", rune(text[i])) {
			i++
		} else if strings.HasPrefix(text[i:], "/*") {
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				return len(text)
			}
			i += end + 4
		} else {
			break
		}
	}
	return i
}

func cloneCSSURL(text string, open int) (string, int, bool) {
	i := cloneCSSSpace(text, open+1)
	if i < len(text) && (text[i] == '\'' || text[i] == '"') {
		value, end, ok := cloneCSSString(text, i)
		end = cloneCSSSpace(text, end)
		if ok && end < len(text) && text[end] == ')' {
			return value, end + 1, true
		}
		return "", end, false
	}
	var out strings.Builder
	for i < len(text) {
		if text[i] == ')' {
			return strings.TrimSpace(out.String()), i + 1, true
		}
		if text[i] == '\\' {
			value, end := cloneCSSEscape(text, i)
			out.WriteString(value)
			i = end
		} else {
			out.WriteByte(text[i])
			i++
		}
	}
	return "", len(text), false
}

func (c *siteCloner) css(ctx context.Context, text string, base *url.URL, depth int) string {
	var out strings.Builder
	imageArguments := []bool{}
	for i := 0; i < len(text); {
		if strings.HasPrefix(text[i:], "/*") {
			i = cloneCSSSpace(text, i)
			out.WriteByte(' ')
			continue
		}
		if text[i] == '\'' || text[i] == '"' {
			value, end, ok := cloneCSSString(text, i)
			if ok && len(imageArguments) > 0 && imageArguments[len(imageArguments)-1] {
				out.WriteString(strconv.Quote(c.resource(ctx, base, value, "image", depth+1)))
			} else {
				out.WriteString(text[i:end])
			}
			i = end
			continue
		}
		if text[i] == '(' || text[i] == ')' {
			if text[i] == '(' {
				imageArguments = append(imageArguments, false)
			} else if len(imageArguments) > 0 {
				imageArguments = imageArguments[:len(imageArguments)-1]
			}
			out.WriteByte(text[i])
			i++
			continue
		}
		start := i
		at := text[i] == '@'
		if at {
			i++
		}
		name, end := cloneCSSName(text, i)
		if end == i {
			out.WriteByte(text[start])
			i = start + 1
			continue
		}
		i = end
		lower := strings.ToLower(name)
		after := cloneCSSSpace(text, end)
		if at && lower == "import" {
			ref, stop, ok := "", after, false
			if after < len(text) && (text[after] == '\'' || text[after] == '"') {
				ref, stop, ok = cloneCSSString(text, after)
			} else {
				word, wordEnd := cloneCSSName(text, after)
				wordEnd = cloneCSSSpace(text, wordEnd)
				if strings.EqualFold(word, "url") && wordEnd < len(text) && text[wordEnd] == '(' {
					ref, stop, ok = cloneCSSURL(text, wordEnd)
				}
			}
			if ok {
				local := c.resource(ctx, base, ref, "css", depth+1)
				if local == "" {
					local = "data:text/css,"
				}
				out.WriteString("@import url(" + strconv.Quote(local) + ")")
				i = stop
			} else {
				// Drop malformed imports as a whole instead of preserving a remote
				// string with browser-dependent error recovery.
				for i < len(text) && text[i] != ';' && text[i] != '{' && text[i] != '}' {
					i++
				}
			}
			continue
		}
		if !at && lower == "url" && after < len(text) && text[after] == '(' {
			ref, stop, ok := cloneCSSURL(text, after)
			local := ""
			if ok {
				local = c.resource(ctx, base, ref, "asset", depth+1)
			}
			out.WriteString("url(" + strconv.Quote(local) + ")")
			i = max(stop, after+1)
			continue
		}
		if (!at && lower == "expression") || lower == "-moz-binding" || lower == "behavior" {
			out.WriteString("agentmirror-disabled")
			continue
		}
		if !at && after < len(text) && text[after] == '(' {
			isImage := lower == "image-set" || lower == "-webkit-image-set" || lower == "image"
			if isImage {
				stop, dynamic := cloneCSSDynamicImage(text, after)
				if dynamic {
					out.WriteString("none")
					c.warn("已移除依赖 CSS 变量或动态属性的图片函数，避免保留未解析的原站网络地址")
					i = stop
					continue
				}
			}
			out.WriteString(text[start : after+1])
			imageArguments = append(imageArguments, isImage)
			i = after + 1
			continue
		}
		out.WriteString(text[start:end])
	}
	return out.String()
}

func cloneCSSDynamicImage(text string, open int) (int, bool) {
	level, dynamic := 1, false
	for i := open + 1; i < len(text); {
		if strings.HasPrefix(text[i:], "/*") {
			i = cloneCSSSpace(text, i)
			continue
		}
		if text[i] == '\'' || text[i] == '"' {
			_, end, _ := cloneCSSString(text, i)
			i = end
			continue
		}
		if text[i] == '(' {
			level++
		} else if text[i] == ')' {
			level--
			if level == 0 {
				return i + 1, dynamic
			}
		}
		name, end := cloneCSSName(text, i)
		if end > i {
			after := cloneCSSSpace(text, end)
			if after < len(text) && text[after] == '(' && (strings.EqualFold(name, "var") || strings.EqualFold(name, "env") || strings.EqualFold(name, "attr")) {
				dynamic = true
			}
			i = end
		} else {
			i++
		}
	}
	return len(text), true
}

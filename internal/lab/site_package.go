package lab

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"mime"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	htmlparser "golang.org/x/net/html"
)

const maxSiteBytes = 8 << 20
const maxSiteFileBytes = 4 << 20
const maxSiteFiles = 256

func sitePath(value string) string {
	if value == "" || len(value) > 512 || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\x00\r\n:") {
		fail(400, "站点文件路径不正确")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." || segment == "." || segment == "" {
			fail(400, "站点文件路径不正确")
		}
	}
	if path.Clean(value) != value {
		fail(400, "站点文件路径不正确")
	}
	return value
}
func siteFileBytes(file Doc) []byte {
	content, ok := file["content"].(string)
	if !ok {
		fail(400, "站点文件 content 必须为文本")
	}
	switch str(file["encoding"]) {
	case "", "utf8":
		if len(content) > maxSiteFileBytes || !utf8.ValidString(content) {
			fail(400, "站点文件过大或 UTF-8 无效")
		}
		return []byte(content)
	case "hosted":
		return nil
	case "base64":
		if len(content) > base64.StdEncoding.EncodedLen(maxSiteFileBytes) {
			fail(413, "单个站点文件不得超过 4 MiB")
		}
		b, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			fail(400, "站点文件 Base64 无效")
		}
		if len(b) > maxSiteFileBytes {
			fail(413, "单个站点文件不得超过 4 MiB")
		}
		return b
	default:
		fail(400, "站点文件编码仅支持 utf8 或 base64")
	}
	return nil
}
func validateSite(raw Doc) Doc {
	id := str(raw["id"])
	if id != "" && !idPattern.MatchString(id) {
		fail(400, "站点编号格式不正确")
	}
	version := 1
	if raw["version"] != nil {
		v, ok := number(raw["version"])
		if !ok || v < 1 || v > 1000000 {
			fail(400, "站点版本必须为正整数")
		}
		version = int(v)
	}
	entry := str(optional(raw, "entry", "index.html"))
	sitePath(entry)
	spa := false
	if v, exists := raw["spa"]; exists {
		var ok bool
		spa, ok = v.(bool)
		if !ok {
			fail(400, "spa 必须是布尔值")
		}
	}
	files := array(raw["files"])
	if len(files) == 0 || len(files) > maxSiteFiles {
		fail(400, "站点需包含 1–256 个文件")
	}
	total := 0
	seen := map[string]bool{}
	clean := []Doc{}
	for _, item := range files {
		f := object(item)
		if f == nil {
			fail(400, "站点文件格式不正确")
		}
		name := sitePath(str(f["path"]))
		if seen[name] {
			fail(400, "站点文件路径重复")
		}
		seen[name] = true
		if f["encoding"] == "hosted" {
			fid := textField(f, "file_id", 64, true)
			if !idPattern.MatchString(fid) {
				fail(400, "托管文件编号无效")
			}
			if name == entry {
				fail(400, "站点首页不能是下载文件")
			}
			filename := textField(f, "download_name", 800, false)
			if filename != "" {
				filename = hostedFilename(filename)
			}
			clean = append(clean, Doc{"path": name, "encoding": "hosted", "file_id": fid, "download_name": filename, "content": ""})
			continue
		}
		b := siteFileBytes(f)
		total += len(b)
		if total > maxSiteBytes {
			fail(413, "站点文件总量不得超过 8 MiB")
		}
		encoding := str(optional(f, "encoding", "utf8"))
		if encoding == "" {
			encoding = "utf8"
		}
		clean = append(clean, Doc{"path": name, "content": str(f["content"]), "encoding": encoding})
	}
	if !seen[entry] || (strings.ToLower(path.Ext(entry)) != ".html" && strings.ToLower(path.Ext(entry)) != ".htm") {
		fail(400, "站点入口必须是包内 HTML 文件")
	}
	doc := Doc{"name": textField(raw, "name", 80, true), "version": version, "entry": entry, "spa": spa, "files": clean}
	if id != "" {
		doc["id"] = id
	}
	if raw["source"] != nil {
		doc["source"] = textField(raw, "source", 1000, false)
	}
	return doc
}
func importSiteZIP(raw []byte, name string) Doc {
	if len(raw) > maxSiteBytes {
		fail(413, "ZIP 不得超过 8 MiB")
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		fail(400, "ZIP 文件无效")
	}
	if len(zr.File) > maxSiteFiles*2 {
		fail(400, "ZIP 文件数量过多")
	}
	files := []Doc{}
	total := 0
	seen := map[string]bool{}
	for _, f := range zr.File {
		if f.Mode()&^0777 != 0 && !f.FileInfo().IsDir() {
			fail(400, "ZIP 不支持符号链接或特殊文件")
		}
		fp := strings.TrimSuffix(f.Name, "/")
		sitePath(fp)
		if f.FileInfo().IsDir() {
			continue
		}
		if seen[fp] {
			fail(400, "ZIP 文件路径重复")
		}
		seen[fp] = true
		if f.UncompressedSize64 > maxSiteFileBytes || len(files) >= maxSiteFiles {
			fail(413, "ZIP 解压文件过大或过多")
		}
		rd, err := f.Open()
		if err != nil {
			fail(400, "ZIP 文件无法读取")
		}
		remaining := min(maxSiteFileBytes, maxSiteBytes-total)
		b, readErr := io.ReadAll(io.LimitReader(rd, int64(remaining)+1))
		closeErr := rd.Close()
		if readErr != nil || closeErr != nil {
			fail(400, "ZIP 文件损坏")
		}
		if len(b) > remaining {
			fail(413, "ZIP 解压总量不得超过 8 MiB，单文件不得超过 4 MiB")
		}
		total += len(b)
		encoding, content := "base64", base64.StdEncoding.EncodeToString(b)
		if utf8.Valid(b) && !bytes.ContainsRune(b, 0) {
			encoding = "utf8"
			content = string(b)
		}
		files = append(files, Doc{"path": fp, "content": content, "encoding": encoding})
	}
	// Common repository archives wrap the complete site in one top-level folder.
	entry := "index.html"
	if !seen[entry] && len(files) > 0 {
		prefix := strings.Split(str(files[0]["path"]), "/")[0] + "/"
		all := true
		for _, f := range files {
			if !strings.HasPrefix(str(f["path"]), prefix) {
				all = false
			}
		}
		if all && seen[prefix+entry] {
			for _, f := range files {
				f["path"] = strings.TrimPrefix(str(f["path"]), prefix)
			}
		}
	}
	return validateSite(Doc{"name": name, "entry": entry, "spa": false, "files": files, "source": "zip"})
}
func siteMIME(p string) string {
	t := mime.TypeByExtension(strings.ToLower(path.Ext(p)))
	if t == "" {
		t = "application/octet-stream"
	}
	return t
}

// Nested entry pages need their actual directory as the browser base URL.
// Keep the raw query, including an explicitly selected run, across the redirect.
func siteEntryLocation(site Doc, requestURL *url.URL) string {
	entry := str(site["entry"])
	if requestURL.Path != "/" || !strings.Contains(entry, "/") {
		return ""
	}
	return (&url.URL{Path: "/" + entry, RawQuery: requestURL.RawQuery}).String()
}

func siteResponse(site Doc, requested string) ([]byte, string, bool) {
	if strings.ContainsAny(requested, "\\\x00") {
		return nil, "", false
	}
	requested = strings.TrimPrefix(requested, "/")
	if requested == "" {
		requested = str(site["entry"])
	} else {
		for _, s := range strings.Split(requested, "/") {
			if s == ".." || s == "." {
				return nil, "", false
			}
		}
		if strings.HasSuffix(requested, "/") {
			requested += "index.html"
		}
	}
	find := func(p string) ([]byte, string, bool) {
		for _, raw := range array(site["files"]) {
			f := object(raw)
			if str(f["path"]) == p && f["encoding"] != "hosted" {
				return siteFileBytes(f), siteMIME(p), true
			}
		}
		return nil, "", false
	}
	if b, t, ok := find(requested); ok {
		return b, t, true
	}
	if boolean(site["spa"]) && path.Ext(requested) == "" && !siteAPIPath(requested) {
		return find(str(site["entry"]))
	}
	return nil, "", false
}

// API-shaped paths never fall through to SPA HTML. The HTTP caller additionally
// restricts this helper to GET/HEAD; input paths are URL-decoded by net/http.
func siteAPIPath(p string) bool {
	for _, prefix := range []string{"api", "nacos", "v1", "v2", "v3", "graphql", "rpc", "actuator", "admin/api"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

var siteTagPattern = regexp.MustCompile(`(?is)<(?:link|script|img|source|video|audio|input|image)\b[^>]*>`)
var siteAttrPattern = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
var siteScriptPattern = regexp.MustCompile(`(?is)<script\b([^>]*)\bsrc\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))([^>]*)>\s*</script\s*>`)
var siteCSSURLPattern = regexp.MustCompile(`(?is)url\(\s*(?:"([^"]*)"|'([^']*)'|([^\s)]+))\s*\)`)
var siteCSSImportPattern = regexp.MustCompile(`(?is)@import\s+(?:url\(\s*)?(?:"([^"]*)"|'([^']*)'|([^\s);]+))\s*\)?\s*([^;]*);`)
var siteStylePattern = regexp.MustCompile(`(?is)<style\b[^>]*>(.*?)</style\s*>`)

func siteFirst(parts ...string) string {
	for _, p := range parts {
		if p != "" {
			return html.UnescapeString(p)
		}
	}
	return ""
}

// The caller must display this document in an iframe with sandbox="". Keeping
// scripts disabled also blocks script-driven iframe navigation; CSP alone does
// not consistently block that navigation across browsers.
func sitePreviewHTML(site Doc) string {
	files := map[string][]byte{}
	for _, raw := range array(site["files"]) {
		f := object(raw)
		if f["encoding"] != "hosted" {
			files[str(f["path"])] = siteFileBytes(f)
		}
	}
	resolve := func(base, ref string) string {
		u, err := url.Parse(ref)
		if err != nil || u.IsAbs() || u.Host != "" || u.Path == "" {
			return ""
		}
		p := path.Clean(path.Join(path.Dir(base), u.Path))
		if strings.HasPrefix(u.Path, "/") {
			p = strings.TrimPrefix(path.Clean(u.Path), "/")
		}
		if p == ".." || strings.HasPrefix(p, "../") {
			return ""
		}
		return p
	}
	budget := 16 << 20
	expansions := 1024
	reserve := func(n int) bool {
		if n > budget || expansions <= 0 {
			return false
		}
		budget -= n
		expansions--
		return true
	}
	dataURL := func(base, ref string) string {
		p := resolve(base, ref)
		b, ok := files[p]
		if !ok || !reserve(base64.StdEncoding.EncodedLen(len(b))+100) {
			return "about:blank"
		}
		return "data:" + siteMIME(p) + ";base64," + base64.StdEncoding.EncodeToString(b)
	}
	var css func(string, string, map[string]bool) string
	css = func(text, base string, active map[string]bool) string {
		text = siteCSSImportPattern.ReplaceAllStringFunc(text, func(v string) string {
			m := siteCSSImportPattern.FindStringSubmatch(v)
			p := resolve(base, siteFirst(m[1], m[2], m[3]))
			b, ok := files[p]
			if !ok || active[p] || !reserve(len(b)) {
				return ""
			}
			next := map[string]bool{}
			for k, v := range active {
				next[k] = v
			}
			next[p] = true
			out := css(string(b), p, next)
			if media := strings.TrimSpace(m[4]); media != "" {
				return "@media " + media + "{" + out + "}"
			}
			return out
		})
		return siteCSSURLPattern.ReplaceAllStringFunc(text, func(v string) string {
			m := siteCSSURLPattern.FindStringSubmatch(v)
			ref := siteFirst(m[1], m[2], m[3])
			if strings.HasPrefix(ref, "data:") || strings.HasPrefix(ref, "#") {
				return v
			}
			return "url(\"" + dataURL(base, ref) + "\")"
		})
	}
	entry := str(site["entry"])
	doc := string(files[entry])
	doc = siteScriptPattern.ReplaceAllStringFunc(doc, func(v string) string {
		m := siteScriptPattern.FindStringSubmatch(v)
		p := resolve(entry, siteFirst(m[2], m[3], m[4]))
		b, ok := files[p]
		if !ok || !reserve(len(b)) {
			return ""
		}
		return "<script" + m[1] + m[5] + ">" + strings.ReplaceAll(string(b), "</script", "<\\/script") + "</script>"
	})
	doc = siteTagPattern.ReplaceAllStringFunc(doc, func(tag string) string {
		lower := strings.ToLower(tag)
		if strings.HasPrefix(lower, "<link") {
			attrs := map[string]string{}
			for _, m := range siteAttrPattern.FindAllStringSubmatch(tag, -1) {
				attrs[strings.ToLower(m[1])] = siteFirst(m[2], m[3], m[4])
			}
			if strings.Contains(strings.ToLower(attrs["rel"]), "stylesheet") {
				p := resolve(entry, attrs["href"])
				if b, ok := files[p]; ok && reserve(len(b)) {
					return "<style>" + strings.ReplaceAll(css(string(b), p, map[string]bool{p: true}), "</style", "<\\/style") + "</style>"
				}
			}
			return ""
		}
		return siteAttrPattern.ReplaceAllStringFunc(tag, func(a string) string {
			m := siteAttrPattern.FindStringSubmatch(a)
			key := strings.ToLower(m[1])
			ref := siteFirst(m[2], m[3], m[4])
			if key == "srcset" {
				return ""
			}
			if key == "src" || key == "poster" || key == "xlink:href" {
				if strings.HasPrefix(ref, "data:") {
					return a
				}
				return key + "=\"" + html.EscapeString(dataURL(entry, ref)) + "\""
			}
			return a
		})
	})
	doc = siteStylePattern.ReplaceAllStringFunc(doc, func(v string) string {
		m := siteStylePattern.FindStringSubmatch(v)
		return "<style>" + css(m[1], entry, map[string]bool{}) + "</style>"
	})
	policy := "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src data:; media-src data:; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'"
	return fmt.Sprintf(`<!doctype html><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="%s">`, html.EscapeString(policy)) + staticSitePreview(doc, func(value string) string { return css(value, entry, map[string]bool{}) })
}

// Only inert HTML reaches the preview iframe. This tokenizer is a security
// boundary; the earlier regular expressions merely improve asset fidelity.
func staticSitePreview(doc string, inlineCSS func(string) string) string {
	allowed := map[string]bool{}
	for _, tag := range strings.Fields("html head body title style script div span p h1 h2 h3 h4 h5 h6 main header footer nav aside section article details summary a ul ol li dl dt dd table thead tbody tfoot tr th td caption colgroup col pre code blockquote hr br em strong b i u s small sub sup label input select option optgroup textarea button fieldset legend img picture source video audio figure figcaption progress meter address time") {
		allowed[tag] = true
	}
	attrs := map[string]bool{}
	for _, attr := range strings.Fields("id class title style width height alt role hidden dir lang tabindex name type value placeholder disabled readonly checked selected multiple required min max step size rows cols colspan rowspan controls muted loop preload open datetime") {
		attrs[attr] = true
	}
	var out strings.Builder
	z := htmlparser.NewTokenizer(strings.NewReader(doc))
	z.SetMaxBuf(maxSiteBytes + 1)
	rawTag := ""
	for {
		tt := z.Next()
		switch tt {
		case htmlparser.ErrorToken:
			return out.String()
		case htmlparser.TextToken:
			value := string(z.Text())
			if rawTag == "style" {
				out.WriteString(value)
			} else if rawTag == "script" {
				out.WriteString(value)
			} else {
				out.WriteString(html.EscapeString(value))
			}
		case htmlparser.StartTagToken, htmlparser.SelfClosingTagToken:
			tok := z.Token()
			tag := strings.ToLower(tok.Data)
			if !allowed[tag] {
				continue
			}
			out.WriteByte('<')
			out.WriteString(tag)
			if tag == "script" {
				out.WriteString(` type="application/x-agentmirror-preview"`)
				rawTag = "script"
			} else if tag == "style" {
				rawTag = "style"
			}
			for _, attr := range tok.Attr {
				key := strings.ToLower(attr.Key)
				if tag == "script" {
					continue
				}
				if key == "src" || key == "poster" {
					if (tag == "img" || tag == "source" || tag == "video" || tag == "audio") && strings.HasPrefix(attr.Val, "data:") {
						out.WriteString(" " + key + `="` + html.EscapeString(attr.Val) + `"`)
					}
					continue
				}
				if attrs[key] || strings.HasPrefix(key, "aria-") || strings.HasPrefix(key, "data-") {
					if key == "style" {
						attr.Val = inlineCSS(attr.Val)
					}
					out.WriteString(" " + key + `="` + html.EscapeString(attr.Val) + `"`)
				}
			}
			out.WriteByte('>')
		case htmlparser.EndTagToken:
			tok := z.Token()
			tag := strings.ToLower(tok.Data)
			if !allowed[tag] {
				continue
			}
			out.WriteString("</" + tag + ">")
			if tag == rawTag {
				rawTag = ""
			}
		}
	}
}

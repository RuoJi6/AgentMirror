package lab

import (
	"crypto/sha256"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maxHostedFileBytes int64 = 128 << 20

var hostedHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (s *Store) hostedDirectory() string {
	if s.hostedRoot != "" {
		return s.hostedRoot
	}
	return s.path + ".files"
}
func hostedFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 200 || strings.ContainsAny(name, "/\\\x00\r\n") || name == "." || name == ".." {
		fail(400, "下载文件名无效，请使用不含目录的文件名（最多 200 字）")
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			fail(400, "文件名不能包含控制字符")
		}
	}
	return name
}
func (s *Store) receiveHostedFile(body io.Reader, name string) Doc {
	name = hostedFilename(name)
	check(os.MkdirAll(s.hostedDirectory(), 0700))
	file, err := os.CreateTemp(s.hostedDirectory(), "upload-*")
	check(err)
	defer os.Remove(file.Name())
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(body, maxHostedFileBytes+1))
	check(err)
	if size > maxHostedFileBytes {
		fail(413, "单个托管文件不得超过 128 MiB")
	}
	check(file.Sync())
	check(file.Close())
	digest := fmt.Sprintf("%x", hash.Sum(nil))
	check(os.Rename(file.Name(), filepath.Join(s.hostedDirectory(), digest)))
	d := Doc{"id": randomHex(12), "name": name, "size": size, "sha256": digest, "created_at": timestamp()}
	s.write(func(q queryer) { put(q, "hosted_files", d) })
	return d
}
func (a *App) hostedFileRoute(w http.ResponseWriter, r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/api/workspaces/") && strings.HasSuffix(r.URL.Path, "/hosted-files") {
		if r.Method != "POST" {
			fail(405, "请求方法不支持")
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/workspaces/"), "/hosted-files")
		if !idPattern.MatchString(id) {
			fail(404, "工作区不存在")
		}
		response(w, r, a.store.attachWorkspaceHostedFile(id, requestBody(w, r)), 200, "", nil)
		return true
	}
	if r.URL.Path != "/api/hosted-files" {
		return false
	}
	switch r.Method {
	case "GET":
		response(w, r, Doc{"items": listing(a.store.db, "hosted_files"), "max_bytes": maxHostedFileBytes}, 200, "", nil)
	case "POST":
		if r.ContentLength > maxHostedFileBytes {
			fail(413, "单个托管文件不得超过 128 MiB")
		}
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now().Add(10 * time.Minute))
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Minute))
		// A raw body avoids base64 expansion and buffering large packages in JSON.
		body := http.MaxBytesReader(w, r.Body, maxHostedFileBytes+1)
		defer body.Close()
		d := a.store.receiveHostedFile(body, r.URL.Query().Get("name"))
		response(w, r, d, 201, "", nil)
	default:
		fail(405, "请求方法不支持")
	}
	return true
}
func validateHostedSiteFiles(q queryer, site Doc) {
	for _, raw := range array(site["files"]) {
		f := object(raw)
		if f["encoding"] == "hosted" {
			get(q, "hosted_files", str(f["file_id"]))
		}
	}
}
func hostedSiteFile(site Doc, requested string) Doc {
	name := strings.TrimPrefix(requested, "/")
	for _, raw := range array(site["files"]) {
		f := object(raw)
		if f["encoding"] == "hosted" && f["path"] == name {
			return f
		}
	}
	return nil
}
func (s *Store) openHostedFile(id string) (*os.File, Doc) {
	d := get(s.db, "hosted_files", id)
	hash := str(d["sha256"])
	if !hostedHashPattern.MatchString(hash) {
		fail(500, "托管文件元数据异常")
	}
	f, err := os.Open(filepath.Join(s.hostedDirectory(), hash))
	if err != nil {
		fail(404, "托管文件内容不存在，请检查文件存储或重新上传")
	}
	return f, d
}
func (a *App) serveHostedFile(w http.ResponseWriter, r *http.Request, asset Doc, session Doc, request Doc) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Minute))
	f, meta := a.store.openHostedFile(str(asset["file_id"]))
	defer f.Close()
	name := str(asset["download_name"])
	if name == "" {
		name = str(meta["name"])
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": hostedFilename(name)}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("ETag", `"`+str(meta["sha256"])+`"`)
	writer := &hostedResponseWriter{ResponseWriter: w, status: 200}
	modified, _ := time.Parse(time.RFC3339, str(meta["created_at"]))
	http.ServeContent(writer, r, name, modified, f)
	if writer.err == nil {
		snap := object(session["snapshot"])
		result := Doc{"status": writer.status, "content_type": "application/octet-stream", "release_id": snap["release_id"], "site_version_id": request["site_version_id"], "site_node_id": request["site_node_id"]}
		a.store.recordComposer(str(session["id"]), request, result)
		accessComposerResult(r, result)
		if r.Method == "GET" && (writer.status == 200 || writer.status == 206) {
			accessNote(r, Doc{"download": Doc{"file_id": meta["id"], "filename": name, "size": meta["size"], "bytes_sent": writer.bytes, "range": r.Header.Get("Range")}})
			a.store.write(func(q queryer) {
				event(q, str(session["id"]), "file_download", Doc{"file_id": meta["id"], "filename": name, "sha256": meta["sha256"], "size": meta["size"], "bytes_sent": writer.bytes, "range": r.Header.Get("Range"), "path": r.URL.Path, "site_node_id": request["site_node_id"], "release_id": snap["release_id"]})
			})
		}
	}
}

type hostedResponseWriter struct {
	http.ResponseWriter
	status, bytes int
	err           error
}

func (w *hostedResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
func (w *hostedResponseWriter) Write(b []byte) (int, error) {
	n, e := w.ResponseWriter.Write(b)
	w.bytes += n
	w.err = e
	return n, e
}

// Resolve metadata only; contents never enter a workspace snapshot or model context.
func hostedFileMetadata(q queryer, site Doc) []any {
	out := []any{}
	seen := map[string]bool{}
	for _, raw := range array(site["files"]) {
		f := object(raw)
		id := str(f["file_id"])
		if f["encoding"] == "hosted" && !seen[id] {
			out = append(out, get(q, "hosted_files", id))
			seen[id] = true
		}
	}
	return out
}

package lab

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const browserCaptureTimeout = 45 * time.Second
const browserCaptureHTMLLimit = 2 << 20

type browserCapture struct {
	HTML     string   `json:"html"`
	URL      string   `json:"final_url"`
	Title    string   `json:"title"`
	Text     string   `json:"text"`
	Warnings []string `json:"warnings"`
	Requests int      `json:"-"`
	Bytes    int      `json:"-"`
}

// The browser has no direct network access. Its intercepted GET requests are
// fulfilled over JSONL by the same DNS-pinned public-network fetcher as static
// cloning. The child receives no model credentials or administrator session.
func (c *siteCloner) captureBrowser(ctx context.Context, source string) (browserCapture, error) {
	worker, err := browserWorkerPath()
	if err != nil {
		return browserCapture{}, err
	}
	node := os.Getenv("AGENTMIRROR_BROWSER_NODE")
	if node == "" {
		node = "node"
	}
	node, err = osexec.LookPath(node)
	if err != nil {
		return browserCapture{}, fmt.Errorf("动态页面需要 Node.js 和 Playwright 浏览器运行环境；未找到 Node.js")
	}
	ctx, cancel := context.WithTimeout(ctx, browserCaptureTimeout)
	defer cancel()
	home, err := os.MkdirTemp("", "agentmirror-browser-")
	if err != nil {
		return browserCapture{}, err
	}
	defer os.RemoveAll(home)
	cmd := osexec.CommandContext(ctx, node, worker)
	cmd.Dir = filepath.Dir(filepath.Dir(worker))
	// HOME is intentionally not inherited: each capture has a disposable profile.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TMPDIR=" + home, "TEMP=" + home, "TMP=" + home}
	for _, name := range []string{"SystemRoot", "WINDIR", "PLAYWRIGHT_BROWSERS_PATH"} {
		if value := os.Getenv(name); value != "" {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	if os.Getenv("PLAYWRIGHT_BROWSERS_PATH") == "" {
		if cache, err := os.UserCacheDir(); err == nil {
			cmd.Env = append(cmd.Env, "PLAYWRIGHT_BROWSERS_PATH="+filepath.Join(cache, "ms-playwright"))
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return browserCapture{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return browserCapture{}, err
	}
	// Closing stdin asks the worker to close its browser. WaitDelay is a bounded
	// fallback if Node fails to respond; do not leave cancellation waiting on JS.
	cmd.Cancel = stdin.Close
	cmd.WaitDelay = 2 * time.Second
	cmd.Stderr = io.Discard // Workers return bounded actionable errors via JSONL.
	if err := cmd.Start(); err != nil {
		stdin.Close()
		return browserCapture{}, fmt.Errorf("浏览器采集进程启动失败: %w", err)
	}
	defer func() {
		stdin.Close()
		cancel()
		_ = cmd.Wait()
	}()
	writer := json.NewEncoder(stdin)
	start := Doc{"type": "start", "url": source, "timeout_ms": int(browserCaptureTimeout.Milliseconds())}
	if executable := os.Getenv("PLAYWRIGHT_EXECUTABLE_PATH"); executable != "" {
		start["executable_path"] = executable
	}
	if err := writer.Encode(start); err != nil {
		return browserCapture{}, fmt.Errorf("浏览器采集启动通信失败")
	}
	network := newSiteCloner(nil)
	network.lookup, network.dial = c.lookup, c.dial
	network.limits.pageBytes, network.limits.assetBytes = browserCaptureHTMLLimit, 8<<20
	network.limits.totalBytes, network.limits.requests = 32<<20, 160
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	requests := 0
	documentOrigins := map[string]bool{}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return browserCapture{}, fmt.Errorf("浏览器采集已取消或超时: %w", err)
		}
		var message struct {
			Type         string `json:"type"`
			ID           string `json:"id"`
			URL          string `json:"url"`
			Method       string `json:"method"`
			ResourceType string `json:"resource_type"`
			Error        string `json:"error"`
			browserCapture
		}
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			return browserCapture{}, fmt.Errorf("浏览器采集返回无效消息")
		}
		switch message.Type {
		case "fetch":
			requests++
			if requests > 160 || len(message.ID) > 100 || message.ID == "" {
				return browserCapture{}, fmt.Errorf("浏览器资源请求数量超出上限")
			}
			response := network.browserFetch(ctx, message.ID, message.Method, message.URL, message.ResourceType)
			if failure := str(response["error"]); failure != "" {
				c.report("render_resource_warning", failure)
			}
			if response["status"] == 200 && message.ResourceType == "document" {
				documentOrigins[browserCaptureOrigin(message.URL)] = true
			}
			if err := writer.Encode(response); err != nil {
				return browserCapture{}, fmt.Errorf("浏览器采集通信中断")
			}
		case "result":
			capture := message.browserCapture
			if capture.HTML == "" || len(capture.HTML) > browserCaptureHTMLLimit || !utf8.ValidString(capture.HTML) {
				return browserCapture{}, fmt.Errorf("浏览器渲染结果为空或超过 2 MiB")
			}
			if _, err := cloneURL(capture.URL); err != nil {
				return browserCapture{}, fmt.Errorf("浏览器返回无效页面地址")
			}
			if !documentOrigins[browserCaptureOrigin(capture.URL)] {
				return browserCapture{}, fmt.Errorf("浏览器结果没有来自已校验的页面")
			}
			capture.Title = cloneTextLimit(capture.Title, 200)
			capture.Requests, capture.Bytes = network.requests, network.bytes
			capture.Text = cloneTextLimit(capture.Text, 12000)
			if len(capture.Warnings) > 100 {
				capture.Warnings = capture.Warnings[:100]
			}
			for i := range capture.Warnings {
				capture.Warnings[i] = cloneTextLimit(capture.Warnings[i], 1000)
			}
			return capture, nil
		case "error":
			return browserCapture{}, fmt.Errorf("浏览器渲染失败: %s", cloneTextLimit(message.Error, 1000))
		default:
			return browserCapture{}, fmt.Errorf("浏览器采集返回未知消息")
		}
	}
	if err := ctx.Err(); err != nil {
		return browserCapture{}, fmt.Errorf("浏览器采集已取消或超时: %w", err)
	}
	if scanner.Err() != nil {
		return browserCapture{}, fmt.Errorf("浏览器采集输出超限或通信失败")
	}
	return browserCapture{}, fmt.Errorf("浏览器采集未返回结果；请确认已安装 Playwright 和 Chromium（npm ci；npx playwright install chromium）")
}

func browserWorkerPath() (string, error) {
	candidates := []string{}
	if configured := os.Getenv("AGENTMIRROR_BROWSER_WORKER"); configured != "" {
		candidates = append(candidates, configured)
	} else {
		candidates = append(candidates, filepath.Join("scripts", "browser-capture.mjs"))
		if executable, err := os.Executable(); err == nil {
			base := filepath.Dir(executable)
			candidates = append(candidates, filepath.Join(base, "scripts", "browser-capture.mjs"), filepath.Join(base, "..", "scripts", "browser-capture.mjs"))
		}
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			absolute, err := filepath.Abs(candidate)
			if err == nil {
				return absolute, nil
			}
		}
	}
	return "", fmt.Errorf("页面需要浏览器渲染，但未找到采集组件；请部署 scripts/browser-capture.mjs 及 Node.js/Playwright/Chromium，或设置 AGENTMIRROR_BROWSER_WORKER")
}

func (c *siteCloner) browserFetch(ctx context.Context, id, method, source, resourceType string) Doc {
	response := Doc{"type": "response", "id": id}
	if method != "GET" {
		response["error"] = "浏览器采集仅允许匿名 GET 请求"
		return response
	}
	limit := c.limits.assetBytes
	if resourceType == "document" {
		limit = c.limits.pageBytes
	}
	fragment, explicitFragment := "", false
	previousRedirect := c.onRedirect
	c.onRedirect = func(location *url.URL, raw string) {
		if strings.Contains(raw, "#") {
			fragment, explicitFragment = location.Fragment, true
		}
	}
	defer func() { c.onRedirect = previousRedirect }()
	body, contentType, finalURL, err := c.fetch(ctx, source, limit)
	if err != nil {
		response["error"] = cloneTextLimit(err.Error(), 500)
		return response
	}
	original, _ := cloneURL(source) // fetch has already validated this URL.
	response["status"] = 200
	headers := Doc{"content-type": contentType, "access-control-allow-origin": "*"}
	if original.String() != finalURL.String() {
		// Let Chromium update its document URL/base. Every redirect destination
		// still arrives back through this fetcher before any bytes are delivered.
		response["status"] = 307
		location := *finalURL
		if explicitFragment {
			location.Fragment = fragment
		}
		headers["location"] = location.String()
		if explicitFragment && fragment == "" {
			headers["location"] = location.String() + "#"
		}
		body = nil
	}
	response["headers"], response["body_base64"] = headers, base64.StdEncoding.EncodeToString(body)
	return response
}

func browserCaptureOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

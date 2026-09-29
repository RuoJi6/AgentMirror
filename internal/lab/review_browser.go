package lab

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"
)

func validateReviewSteps(value any) []any {
	steps := array(value)
	if steps == nil {
		steps = []any{}
	}
	if len(steps) > 40 {
		fail(400, "单次浏览器流程最多 40 步")
	}
	result := []any{}
	for _, v := range steps {
		step := object(v)
		action := textField(step, "action", 30, true)
		if !has([]any{"goto", "fill", "click", "select", "assert_text", "assert_visible"}, action) {
			fail(400, "浏览器步骤不支持此操作")
		}
		d := Doc{"action": action, "selector": textField(step, "selector", 500, false), "value": textField(step, "value", 2000, false)}
		if action == "goto" {
			reviewLocalPath(str(d["value"]))
		}
		if action != "goto" && action != "assert_text" && str(d["selector"]) == "" {
			fail(400, "此浏览器步骤需要 CSS 选择器")
		}
		result = append(result, d)
	}
	return result
}

func (r *workspaceReviewer) browserFlow(ctx context.Context, steps []any) (Doc, error) {
	redteamStart := 0
	if r.redteam != nil {
		redteamStart = len(r.redteam.observations)
	}
	kind, label := "browser", "浏览器连续访问"
	if r.redteam != nil {
		kind, label = "redteam_browser", "红队浏览器连续访问"
	}
	group := r.flowGroup(kind, label)
	stepNodes := map[string]string{}
	previousStep := ""
	// Preserve partial observations on cancellation, launch failure or timeout.
	defer func() {
		for _, raw := range r.flow.nodes {
			n := object(raw)
			if n["group"] == group && n["status"] == "running" {
				n["status"] = "incomplete"
			}
		}
		r.publishFlow()
	}()
	worker, err := browserWorkerPath()
	if err != nil {
		return nil, err
	}
	worker = filepath.Join(filepath.Dir(worker), "workspace-review.mjs")
	if _, err = os.Stat(worker); err != nil {
		return nil, fmt.Errorf("缺少浏览器核查 worker，请安装 scripts/workspace-review.mjs")
	}
	node := os.Getenv("AGENTMIRROR_BROWSER_NODE")
	if node == "" {
		node = "node"
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := osexec.CommandContext(ctx, node, worker)
	cmd.Dir = filepath.Dir(filepath.Dir(worker))
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + r.sandbox.dir, "TMPDIR=" + r.sandbox.dir}
	for _, key := range []string{"PLAYWRIGHT_BROWSERS_PATH", "SystemRoot", "WINDIR"} {
		if value := os.Getenv(key); value != "" {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	if os.Getenv("PLAYWRIGHT_BROWSERS_PATH") == "" {
		if cache, e := os.UserCacheDir(); e == nil {
			cmd.Env = append(cmd.Env, "PLAYWRIGHT_BROWSERS_PATH="+filepath.Join(cache, "ms-playwright"))
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	cmd.Stderr = io.Discard
	cmd.Cancel = stdin.Close
	cmd.WaitDelay = 2 * time.Second
	if err = cmd.Start(); err != nil {
		stdin.Close()
		return nil, err
	}
	defer func() { stdin.Close(); cancel(); _ = cmd.Wait() }()
	writer := json.NewEncoder(stdin)
	if err = writer.Encode(Doc{"type": "start", "steps": steps, "executable_path": os.Getenv("PLAYWRIGHT_EXECUTABLE_PATH")}); err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	requests := 0
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		msg := decode(scanner.Text())
		switch msg["type"] {
		case "step_start":
			id := r.flowNode(group, "step", str(msg["label"]), "running", Doc{"action": msg["action"], "target": msg["target"]})
			stepNodes[str(msg["step_id"])] = id
			r.flowEdge(previousStep, id, "next")
			previousStep = id
		case "step_end":
			for _, raw := range r.flow.nodes {
				n := object(raw)
				if n["id"] == stepNodes[str(msg["step_id"])] {
					n["status"] = msg["status"]
					merge(object(n["evidence"]), Doc{"detail": msg["detail"], "page": msg["evidence"]})
				}
			}
			r.publishFlow()
		case "request":
			requests++
			if requests > 300 {
				return nil, fmt.Errorf("浏览器请求超过 300 次限制")
			}
			var res Doc
			func() {
				defer func() {
					if p := recover(); p != nil {
						res = Doc{"status": 400, "body": caught(p).Error(), "headers": Doc{}}
					}
				}()
				res = r.sandbox.request(object(msg["request"]), false)
			}()
			if res["path"] == nil {
				res["path"] = object(msg["request"])["path"]
				res["method"] = object(msg["request"])["method"]
			}
			state := "observed"
			if integer(res["status"]) >= 400 {
				state = "fail"
			}
			node := r.flowRequest(group, stepNodes[str(msg["step_id"])], res, state, "")
			if r.redteam != nil {
				r.observeRedteam(res, group, node)
				r.redteamCallbacks(res, nil, nil, node, group)
			}
			if r.browserCovered == nil {
				r.browserCovered = map[string]bool{}
			}
			for _, v := range array(res["observed_rules"]) {
				observed := object(v)
				for _, rule := range r.sandbox.rules {
					if observed["rule_id"] == rule["id"] && observed["binding_id"] == rule["binding_id"] {
						r.browserCovered[promptBindingKey(rule)] = true
					}
				}
			}
			wire := reviewWireResponse(res)
			wire["type"] = "response"
			wire["id"] = msg["id"]
			if err = writer.Encode(wire); err != nil {
				return nil, err
			}
		case "result":
			if r.redteam != nil {
				observations := []any{}
				for _, o := range r.redteam.observations[redteamStart:] {
					observations = append(observations, Doc{"observation_id": o["id"], "path": o["path"], "method": o["method"], "status": o["status"]})
				}
				msg["observations"] = observations
			}
			r.browserRan = true
			for _, v := range array(msg["steps"]) {
				step := object(v)
				if step["status"] == "pass" && strings.Contains(str(step["name"]), "assert_") {
					r.browserAssertions++
				}
				r.addCheck("浏览器 · "+str(step["name"]), str(step["status"]), str(step["detail"]), object(step["evidence"]))
			}
			for _, v := range array(msg["errors"]) {
				r.addCheck("浏览器异常", "fail", str(v), nil)
			}
			if len(steps) == 0 {
				r.addCheck("页面链路覆盖", "incomplete", "仅观察了首页，尚未执行登录及登录后访问步骤", nil)
			}
			return msg, nil
		case "error":
			return nil, fmt.Errorf("浏览器核查失败：%s", str(msg["error"]))
		}
	}
	return nil, fmt.Errorf("浏览器核查未完成或超时")
}

// Browser plugin not available. Use Playwright against an isolated Go server.
// Flow: write a partial draft -> cancel -> refresh -> continue -> verify exact recovered content.
import assert from "node:assert/strict";
import { once } from "node:events";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { spawn } from "node:child_process";
import net from "node:net";
import { createServer } from "node:http";
import os from "node:os";
import path from "node:path";
import { chromium } from "playwright";

const root = path.resolve(import.meta.dirname, "..");
const temp = await mkdtemp(path.join(os.tmpdir(), "agentmirror-resume-"));
const listener = net.createServer().listen(0, "127.0.0.1");
await once(listener, "listening");
const port = listener.address().port;
await new Promise((resolve) => listener.close(resolve));
const admin = `http://127.0.0.1:${port}`;
const server = spawn(
  path.join(root, "bin/agentmirror"),
  [
    "--admin-host",
    "127.0.0.1",
    "--admin-port",
    String(port),
    "--db",
    path.join(temp, "test.sqlite3"),
  ],
  { cwd: root, stdio: ["ignore", "pipe", "pipe"] },
);
let browser;
let modelServer;
const errors = [];
const checks = [];
const pass = (name) => {
  checks.push(name);
  console.log("PASS", name);
};
try {
  await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(Error("startup timeout")), 10000);
    server.once("error", reject);
    server.once("exit", (code) => {
      clearTimeout(timeout);
      reject(Error(`server exited ${code}`));
    });
    server.stdout.on("data", (data) => {
      if (data.toString().includes("公告地址")) {
        clearTimeout(timeout);
        resolve();
      }
    });
  });
  browser = await chromium.launch({
    headless: true,
    ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH
      ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH }
      : {}),
  });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
    reducedMotion: "reduce",
  });
  const auth = await (
    await context.request.get(admin + "/api/auth/status")
  ).json();
  const setup = await context.request.post(admin + "/api/auth/setup", {
    headers: { "X-Admin-Token": auth.csrf },
    data: {
      username: "previewtest",
      password: "SYNTHETIC-preview-2026",
      confirm_password: "SYNTHETIC-preview-2026",
    },
  });
  assert.equal(setup.status(), 200);
  const state = await (await context.request.get(admin + "/api/state")).json();
  const api = async (route, body) => {
    const result = await context.request.fetch(admin + "/api" + route, {
      method: body ? "POST" : "GET",
      headers: { "X-Admin-Token": state.csrf },
      ...(body ? { data: body } : {}),
    });
    assert.equal(result.status(), 200, await result.text());
    return result.json();
  };

  let requests = 0;
  const original = "创建一个包含首页和样式的站点，先写样式再完成首页";
  const css = "/* preserved after interruption */\n".repeat(600);
  const call = (id, name, args) => ({
    id,
    type: "function",
    function: { name, arguments: JSON.stringify(args) },
  });
  let hasContext = false;
  modelServer = createServer(async (req, res) => {
    let raw = "";
    for await (const chunk of req) raw += chunk;
    const input = JSON.parse(raw);
    const number = ++requests;
    const reply = (message) => {
      res.setHeader("Content-Type", "application/json");
      res.end(
        JSON.stringify({
          choices: [
            {
              message,
              finish_reason: message.tool_calls ? "tool_calls" : "stop",
            },
          ],
        }),
      );
    };
    if (number === 1) {
      reply({
        content: "",
        tool_calls: [
          call("plan", "update_plan", {
            steps: [{ step: "完成样式和首页", status: "in_progress" }],
          }),
          call("style", "write_file", { path: "style.css", content: css }),
        ],
      });
    } else if (number === 2) {
      // Keep the request open until cancellation; no real model is called.
      req.on("close", () => res.destroy());
    } else if (number === 3) {
      hasContext =
        raw.includes(original) &&
        raw.includes("style.css") &&
        raw.includes("完成样式和首页");
      reply({
        content: "",
        tool_calls: [call("inspect", "read_file", { path: "style.css" })],
      });
    } else if (number === 4) {
      assert(raw.includes("preserved after interruption"));
      reply({
        content: "",
        tool_calls: [
          call("entry", "write_file", {
            path: "index.html",
            content: "<h1>Recovered page</h1>",
          }),
          call("done", "finish_draft", {
            summary: "已接回任务并完成首页，保留中断前的完整样式。",
          }),
        ],
      });
    } else {
      reply({ content: "新的对话不继承旧任务。" });
    }
  });
  modelServer.listen(0, "127.0.0.1");
  await once(modelServer, "listening");
  await api("/generation/providers", {
    name: "中断恢复测试模型",
    protocol: "openai",
    base_url: `http://127.0.0.1:${modelServer.address().port}`,
    model: "synthetic-resume",
    api_key: "SYNTHETIC",
    set_default: true,
    streaming: false,
  });
  const workspace = await api("/workspaces", {
    name: "中断续聊验收",
    slug: "resume-qa",
    bindings: [],
  });
  const page = await context.newPage();
  page.setDefaultTimeout(15000);
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (e) => {
    if (e.type() === "error") errors.push(e.text());
  });
  await page.goto(admin + "/#composer/" + workspace.id);
  assert.match(await page.title(), /AgentMirror/);
  await page
    .getByRole("heading", { name: workspace.name, exact: true })
    .waitFor();
  await page.locator(".agent-mention-input textarea").fill(original);
  await page.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await page.getByText("write_file", { exact: true }).first().waitFor();
  for (let i = 0; i < 100 && requests < 2; i++)
    await new Promise((r) => setTimeout(r, 50));
  assert.equal(requests, 2);
  const conversations = await api(
    `/generation/conversations?workspace_id=${workspace.id}`,
  );
  const rootId = conversations.items[0].id;
  await page
    .getByRole("button", { name: /停止|取消/ })
    .last()
    .click();
  await page.getByText("已取消", { exact: true }).first().waitFor();
  const old = await api(`/generation/jobs/${rootId}`);
  assert.equal(old.status, "cancelled");
  assert.equal(old.checkpoint, undefined);
  assert.equal(old.result, undefined);
  pass(
    "partial draft cancellation does not expose an adoptable incomplete result",
  );

  await page.reload();
  await page.getByText("已取消", { exact: true }).first().waitFor();
  const submitted = page.waitForRequest(
    (r) => r.method() === "POST" && r.url().endsWith("/api/generation/jobs"),
  );
  await page.locator(".agent-mention-input textarea").fill("继续");
  await page.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  assert.equal((await submitted).postDataJSON().parent_job_id, rootId);
  await page
    .getByText("已接回任务并完成首页，保留中断前的完整样式。", { exact: true })
    .waitFor();
  const notice = page.getByText(
    "已恢复中断前的草稿、原始需求、计划和工具进度",
    { exact: true },
  );
  await notice.waitFor();
  assert(hasContext);
  const turns = await api(`/generation/conversations/${rootId}`);
  assert.equal(turns.turns.length, 2);
  const resumed = await api(`/generation/jobs/${turns.turns[1].id}`);
  assert.equal(resumed.status, "completed");
  assert.equal(
    resumed.result.site.files.find((f) => f.path === "style.css").content,
    css,
  );
  assert.deepEqual(resumed.result.changes, ["site"]);
  assert.equal(resumed.checkpoint, undefined);
  pass(
    "refresh then continue preserves original task, plan and exact file content",
  );

  const out = process.env.QA_OUTPUT_DIR || "/private/tmp/agentmirror-resume-qa";
  await mkdir(out, { recursive: true });
  await notice.scrollIntoViewIfNeeded();
  await page.screenshot({ path: path.join(out, "resume-desktop.png") });
  await page.setViewportSize({ width: 390, height: 844 });
  await notice.scrollIntoViewIfNeeded();
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  await page.screenshot({ path: path.join(out, "resume-mobile.png") });
  await page.reload();
  await notice.waitFor();
  assert.equal(await page.locator("vite-error-overlay").count(), 0);
  assert.deepEqual(errors, []);
  pass("recovery notice and history survive refresh on desktop and mobile");
  console.log(JSON.stringify({ checks, errors }, null, 2));
} finally {
  if (browser) await browser.close();
  const stopped = once(server, "exit");
  server.kill("SIGTERM");
  await stopped;
  if (modelServer) {
    modelServer.closeAllConnections();
    await new Promise((r) => modelServer.close(r));
  }
  await rm(temp, { recursive: true, force: true });
}

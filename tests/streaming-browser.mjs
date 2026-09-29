// Browser plugin not available. Use Playwright against an isolated Go server.
// Flow: send the same request with/without streaming -> compare first content -> complete/refresh/cancel.
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
const temp = await mkdtemp(path.join(os.tmpdir(), "agentmirror-streaming-"));
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
  const modelDuration = 6500;
  modelServer = createServer(async (req, res) => {
    let body = "";
    for await (const chunk of req) body += chunk;
    const input = JSON.parse(body);
    const number = ++requests;
    const send = (delta, finish = null) =>
      res.write(
        `data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: finish }] })}\n\n`,
      );
    if (!input.stream) {
      await new Promise((resolve) => setTimeout(resolve, modelDuration));
      res.setHeader("Content-Type", "application/json");
      res.end(
        JSON.stringify({
          choices: [
            {
              message: { content: `回复完成 ${number}` },
              finish_reason: "stop",
            },
          ],
        }),
      );
      return;
    }
    res.writeHead(200, { "Content-Type": "text/event-stream" });
    await new Promise((resolve) => setTimeout(resolve, 200));
    send({ reasoning_content: "先检查工作区的配置与接口。" });
    await new Promise((resolve) => setTimeout(resolve, 700));
    send({ content: "正在核对接口配置。" });
    await new Promise((resolve) => setTimeout(resolve, modelDuration - 900));
    send({ content: ` 回复完成 ${number}` }, "stop");
    res.end("data: [DONE]\n\n");
  });
  modelServer.listen(0, "127.0.0.1");
  await once(modelServer, "listening");
  const provider = await api("/generation/providers", {
    name: "合成诊断服务",
    protocol: "openai",
    base_url: `http://127.0.0.1:${modelServer.address().port}`,
    model: "synthetic-model",
    api_key: "SYNTHETIC",
    set_default: true,
    streaming: false,
  });
  const workspace = await api("/workspaces", {
    name: "首轮响应验收",
    slug: "transport-qa",
    bindings: [],
  });
  const page = await context.newPage();
  page.setDefaultTimeout(15000);
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (entry) => {
    if (entry.type() === "error") errors.push(entry.text());
  });
  await page.goto(admin + "/#composer/" + workspace.id);
  assert.match(await page.title(), /AgentMirror/);
  assert.equal(new URL(page.url()).hash, "#composer/" + workspace.id);
  await page
    .getByRole("heading", { name: "首轮响应验收", exact: true })
    .waitFor();
  await page.locator(".agent-mention-input textarea").fill("简短说明检查方式");
  const baselineStart = Date.now();
  await page.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await page.getByText("回复完成 1", { exact: true }).waitFor();
  const baseline = Date.now() - baselineStart;
  await page.getByRole("button", { name: "模型设置", exact: true }).click();
  const modal = page.getByRole("dialog", { name: "生成模型设置" });
  const streaming = modal.getByRole("checkbox", {
    name: "流式返回",
    exact: true,
  });
  await streaming.waitFor();
  assert.equal(await streaming.isChecked(), false);
  await streaming.check();
  await modal.getByRole("button", { name: "保存设置", exact: true }).click();
  await page.getByText("模型配置已保存", { exact: true }).waitFor();
  assert(
    (await api("/generation/providers")).items.find(
      (item) => item.id === provider.id,
    ).streaming,
  );
  await modal
    .getByRole("button", { name: "关闭", exact: true })
    .first()
    .click();
  await page.locator(".agent-mention-input textarea").fill("继续说明检查方式");
  const streamStart = Date.now();
  await page.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await page
    .locator(".agent-live-response")
    .getByText("正在思考", { exact: true })
    .waitFor();
  const firstVisible = Date.now() - streamStart;
  assert(firstVisible < 2500, `first content too slow: ${firstVisible}`);
  assert(firstVisible < baseline / 2);
  await page.locator(".agent-live-response summary").click();
  await page
    .locator(".agent-live-response .chat-markdown")
    .getByText("先检查工作区的配置与接口。", { exact: true })
    .waitFor();
  await page
    .locator(".agent-live-response")
    .getByText("正在核对接口配置。", { exact: true })
    .waitFor();
  assert.equal(await page.getByText("回复完成 2", { exact: true }).count(), 0);
  assert.equal(await page.locator("vite-error-overlay").count(), 0);
  const out = process.env.QA_OUTPUT_DIR;
  if (out) {
    await mkdir(out, { recursive: true });
    await page.screenshot({ path: path.join(out, "streaming-desktop.png") });
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator(".agent-live-response").scrollIntoViewIfNeeded();
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  if (out)
    await page.screenshot({ path: path.join(out, "streaming-mobile.png") });
  await page
    .getByText("正在核对接口配置。 回复完成 2", { exact: true })
    .waitFor();
  assert.equal(await page.locator(".agent-live-response").count(), 0);
  // Completed messages survive refresh without any transient preview duplication.
  await page.reload();
  await page
    .getByText("正在核对接口配置。 回复完成 2", { exact: true })
    .waitFor();
  assert.equal(await page.locator(".agent-live-response").count(), 0);
  // Both agent roles share the streaming boundary; the reviewer stays read-only.
  const agents = await api("/agents");
  const reviewer = agents.items.find((item) => item.role === "reviewer");
  assert(reviewer);
  const reviewResponse = await context.request.post(
    admin + "/api/generation/jobs",
    {
      headers: { "X-Admin-Token": state.csrf },
      data: {
        workspace_id: workspace.id,
        agent_id: reviewer.id,
        prompt: "简短介绍核查步骤",
        provider_id: provider.id,
      },
    },
  );
  assert.equal(reviewResponse.status(), 202, await reviewResponse.text());
  const review = await reviewResponse.json();
  let reviewLive;
  for (let i = 0; i < 30; i++) {
    const j = await api(`/generation/jobs/${review.id}?progress=1`);
    if (j.live_response?.reasoning) {
      reviewLive = j;
      break;
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  assert(reviewLive?.live_response?.reasoning);
  await api(`/generation/jobs/${review.id}/cancel`, {});
  const cancelled = await api(`/generation/jobs/${review.id}`);
  assert.equal(cancelled.status, "cancelled");
  assert(!cancelled.live_response);
  console.log(
    JSON.stringify({
      baseline_first_visible_ms: baseline,
      stream_first_visible_ms: firstVisible,
      model_duration_ms: modelDuration,
    }),
  );
  assert.deepEqual(errors, []);
  pass(
    "writer shows streamed reasoning/text before completion, preserves final reply after refresh; reviewer streams and cancels safely",
  );
  console.log(JSON.stringify({ checks, errors }, null, 2));
} finally {
  if (modelServer) await new Promise((resolve) => modelServer.close(resolve));
  if (browser) await browser.close();
  const stopped = once(server, "exit");
  server.kill("SIGTERM");
  await stopped;
  await rm(temp, { recursive: true, force: true });
}

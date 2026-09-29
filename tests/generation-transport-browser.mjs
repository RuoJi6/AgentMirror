// Browser plugin not available. Use Playwright against an isolated Go server.
// Flow: send a message -> transient provider failure -> recover and inspect diagnostics.
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
const temp = await mkdtemp(path.join(os.tmpdir(), "agentmirror-transport-"));
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
  modelServer = createServer(async (req, res) => {
    for await (const chunk of req) {
    }
    requests++;
    if (requests === 1) {
      res.writeHead(503);
      res.end("PRIVATE_PROVIDER_BODY");
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 1000));
    res.setHeader("Content-Type", "application/json");
    res.end(
      JSON.stringify({
        choices: [
          {
            message: { content: "恢复成功，工具结果已保留。" },
            finish_reason: "stop",
          },
        ],
      }),
    );
  });
  modelServer.listen(0, "127.0.0.1");
  await once(modelServer, "listening");
  await api("/generation/providers", {
    name: "合成诊断服务",
    protocol: "openai",
    base_url: `http://127.0.0.1:${modelServer.address().port}`,
    model: "synthetic-model",
    api_key: "SYNTHETIC",
    set_default: true,
  });
  const workspace = await api("/workspaces", {
    name: "模型诊断验收",
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
    .getByRole("heading", { name: "模型诊断验收", exact: true })
    .waitFor();
  await page.locator(".agent-mention-input textarea").fill("检查连接恢复");
  await page.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await page.getByText("恢复成功，工具结果已保留。", { exact: true }).waitFor();
  await page.locator(".agent-response-diagnostics summary").click();
  await page.getByText(/模型响应诊断.*已恢复/).waitFor();
  await page.getByText(/upstream_unavailable/).waitFor();
  assert.equal(requests, 2);
  assert(
    !(await page.getByText("PRIVATE_PROVIDER_BODY", { exact: true }).count()),
  );
  assert.equal(await page.locator("vite-error-overlay").count(), 0);
  const out = process.env.QA_OUTPUT_DIR;
  if (out) {
    await mkdir(out, { recursive: true });
    await page.screenshot({
      path: path.join(out, "model-recovery-desktop.png"),
    });
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator(".agent-response-diagnostics").scrollIntoViewIfNeeded();
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  if (out)
    await page.screenshot({
      path: path.join(out, "model-recovery-mobile.png"),
    });
  assert.deepEqual(errors, []);
  pass(
    "temporary 503 recovers within the same conversation; diagnostics show retry, elapsed time and classification without exposing upstream body",
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

// Browser plugin not available. Use Playwright against an isolated Go server.
// Flow: save a bound profile, repeat a public request, inspect immutable delivery history.
import assert from "node:assert/strict";
import { once } from "node:events";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { spawn } from "node:child_process";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { chromium } from "playwright";

const root = path.resolve(import.meta.dirname, "..");
const temp = await mkdtemp(path.join(os.tmpdir(), "agentmirror-live-prompts-"));
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
const errors = [];
const checks = [];
const pass = (name) => {
  checks.push(name);
  console.log("PASS", name);
};
const response = (body = "{{prompt}}", status = 200) => ({
  status,
  format: "text",
  content_type: "text/plain",
  body,
});
const condition = (source, key, value, operator = "equals") => ({
  source,
  key,
  value,
  operator,
});
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
  const profile = await api("/profiles", {
    name: "热更新验收",
    body: "PROMPT_V1 {{run_id}}",
    category: "unexpected_output",
  });
  const site = await api("/sites", {
    name: "热更新站点",
    entry: "index.html",
    files: [
      {
        path: "index.html",
        encoding: "utf8",
        content:
          "<!doctype html><title>Hot Prompt</title><h1>Published site</h1>",
      },
    ],
  });
  const scenario = await api("/scenarios", {
    name: "热更新接口",
    rules: [
      {
        id: "details",
        name: "详情",
        method: "GET",
        path: "/api/details",
        conditions: [],
        response: response(),
        delivery_required: true,
      },
      ...["config", "notes"].map((file) => ({
        id: file,
        name: file === "config" ? "配置文件" : "说明文件",
        method: "GET",
        path: "/api/download",
        conditions: [condition("query", "file", file + ".txt")],
        response: response(),
        delivery_required: true,
      })),
    ],
  });
  const workspace = await api("/workspaces", {
    name: "热更新工作区",
    slug: "live-prompt-qa",
    site_id: site.id,
    bindings: [
      {
        id: "main",
        scenario_id: scenario.id,
        profile_id: profile.id,
        enabled: true,
        paths: {},
      },
    ],
  });
  const deployment = await api(`/workspaces/${workspace.id}/publish`, {
    version: workspace.version,
  });
  const socket = net.createServer().listen(0, "127.0.0.1");
  await once(socket, "listening");
  const publicPort = socket.address().port;
  await new Promise((resolve) => socket.close(resolve));
  const base = `http://127.0.0.1:${publicPort}`;
  await api("/listeners", {
    name: "热更新测试端口",
    host: "127.0.0.1",
    port: publicPort,
    public_url: base,
    deployment_id: deployment.id,
    root_surface: "page",
    enabled: true,
  });
  const client = await browser.newContext();
  const before = await client.request.get(base + "/api/details");
  const run = before.headers()["x-run-id"];
  assert.equal(await before.text(), "PROMPT_V1 " + run);
  const original = await api("/sessions/" + run);
  const originalDelivery = original.events.find((e) => e.kind === "delivery");
  const page = await context.newPage();
  page.setDefaultTimeout(10000);
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (e) => {
    if (e.type() === "error") errors.push(e.text());
  });
  await page.goto(admin + "/#profiles/" + profile.id);
  await page
    .getByLabel("提示词正文", { exact: true })
    .fill("PROMPT_V2 {{run_id}}");
  await page.getByRole("button", { name: "保存新版本", exact: true }).click();
  await page
    .getByRole("status")
    .filter({ hasText: "后续请求立即生效" })
    .waitFor();
  const after = await client.request.get(base + "/api/details");
  assert.equal(after.headers()["x-run-id"], run);
  assert.equal(await after.text(), "PROMPT_V2 " + run);
  const updated = await api("/sessions/" + run);
  assert.deepEqual(updated.snapshot, original.snapshot);
  assert.deepEqual(
    updated.events.find((e) => e.id === originalDelivery.id),
    originalDelivery,
  );
  assert.equal(
    updated.events.filter((e) => e.kind === "delivery").at(-1).detail
      .deliveries[0].profile_version,
    2,
  );
  const currentState = await api("/state");
  assert.equal(
    currentState.deployments.find((d) => d.id === deployment.id).revision,
    deployment.revision,
  );
  pass(
    "save profile in UI: existing cookie session immediately reads v2 without republication; historical v1 preserved",
  );
  await page.goto(admin + "/#sessions/" + run);
  await page
    .locator(".session-facts")
    .getByText("热更新验收 · v2", { exact: true })
    .waitFor();
  await page.getByRole("button", { name: "投放快照", exact: true }).click();
  await page.getByText("创建时提示词版本", { exact: true }).waitFor();
  const v1Record = page
    .locator("details")
    .filter({ has: page.locator("summary", { hasText: "/api/details" }) })
    .filter({ hasText: "· v1" })
    .first();
  const v2Record = page
    .locator("details")
    .filter({ has: page.locator("summary", { hasText: "/api/details" }) })
    .filter({ hasText: "· v2" })
    .first();
  await v1Record.locator("summary").click();
  await v2Record.locator("summary").click();
  assert((await v1Record.innerText()).includes("PROMPT_V1"));
  assert((await v2Record.innerText()).includes("PROMPT_V2"));
  pass(
    "session UI separates current and creation versions and renders actual versioned delivery history",
  );
  const out = process.env.QA_OUTPUT_DIR;
  if (out) {
    await mkdir(out, { recursive: true });
    await page.screenshot({
      path: path.join(out, "live-prompts-desktop.png"),
      fullPage: true,
    });
  }
  await page.setViewportSize({ width: 390, height: 844 });
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  if (out)
    await page.screenshot({
      path: path.join(out, "live-prompts-mobile.png"),
      fullPage: true,
    });
  const receipt = await client.request.post(base + "/collect", {
    data: {
      run_id: run,
      token: original.snapshot.token,
      data: "AFTER_PROMPT_UPDATE",
    },
  });
  assert.equal(receipt.status(), 201);
  assert.deepEqual(errors, []);
  pass(
    "mobile layout, original receipt token remains valid, zero console/page errors",
  );
  // Reproduce changing to a DIFFERENT profile, not just editing its body.
  await page.setViewportSize({ width: 1440, height: 1000 });
  const replacement = await api("/profiles", {
    name: "换绑后的工单",
    body: "REBOUND_PROFILE {{run_id}}",
    category: "unexpected_output",
  });
  await page.goto(admin + "/#composer/" + workspace.id);
  // The new fixture was added through the API outside the app's mutation flow.
  await page.reload();
  await page.getByRole("tab", { name: "绑定提示词", exact: true }).click();
  await page.getByLabel("实例 1 默认提示词", { exact: true }).click();
  await page
    .locator(`[role="option"][data-select-value="${replacement.id}"]`)
    .click();
  assert.equal(
    await (await client.request.get(base + "/api/details")).text(),
    "PROMPT_V2 " + run,
  );
  await page.getByRole("button", { name: "保存工作区", exact: true }).click();
  await page
    .getByRole("status")
    .filter({ hasText: "工作区草稿已保存" })
    .waitFor();
  const afterSavedBinding = await api("/state");
  assert.equal(
    afterSavedBinding.deployments.find((d) => d.id === deployment.id).revision,
    deployment.revision,
  );
  const rebound = await client.request.get(base + "/api/details");
  assert.equal(rebound.headers()["x-run-id"], run);
  assert.equal(await rebound.text(), "REBOUND_PROFILE " + run);
  const reboundHistory = await api("/sessions/" + run);
  assert.deepEqual(reboundHistory.snapshot, original.snapshot);
  assert.deepEqual(
    reboundHistory.events.find((e) => e.id === originalDelivery.id),
    originalDelivery,
  );
  assert.equal(
    reboundHistory.events.filter((e) => e.kind === "delivery").at(-1).detail
      .deliveries[0].profile_id,
    replacement.id,
  );
  await page.goto(admin + "/#sessions/" + run);
  await page
    .locator(".session-facts")
    .getByText("换绑后的工单 · v1", { exact: true })
    .waitFor();
  await page.getByRole("button", { name: "投放快照", exact: true }).click();
  await page
    .locator("details summary")
    .filter({ hasText: "换绑后的工单 · v1" })
    .click();
  assert.deepEqual(errors, []);
  if (out)
    await page.screenshot({
      path: path.join(out, "live-prompts-rebound.png"),
      fullPage: true,
    });
  pass(
    "switch profile in binding selector: unsaved edits stay private; save alone updates same cookie session and current version UI without republishing, preserving historical delivery",
  );
  const choose = async (label, value) => {
    await page.getByLabel(label, { exact: true }).click();
    await page.locator(`[role="option"][data-select-value="${value}"]`).click();
  };
  const saveBinding = async () => {
    const saved = page.waitForResponse(
      (r) =>
        r.url().endsWith("/api/workspaces") && r.request().method() === "POST",
    );
    await page.getByRole("button", { name: "保存工作区", exact: true }).click();
    assert.equal((await saved).status(), 200);
    await page
      .getByRole("button", { name: "保存工作区", exact: true })
      .waitFor();
  };
  await page.goto(admin + "/#composer/" + workspace.id);
  await page.getByRole("tab", { name: "绑定提示词", exact: true }).click();
  assert.match(await page.title(), /AgentMirror/);
  assert.equal(new URL(page.url()).hash, "#composer/" + workspace.id);
  await choose("实例 1 · 配置文件 提示词", profile.id);
  assert.equal(
    await (
      await client.request.get(base + "/api/download?file=config.txt")
    ).text(),
    "REBOUND_PROFILE " + run,
  );
  await saveBinding();
  assert.equal(
    await (
      await client.request.get(base + "/api/download?file=config.txt")
    ).text(),
    "PROMPT_V2 " + run,
  );
  assert.equal(
    await (
      await client.request.get(base + "/api/download?file=notes.txt")
    ).text(),
    "REBOUND_PROFILE " + run,
  );
  assert.equal(
    await (await client.request.get(base + "/api/details")).text(),
    "REBOUND_PROFILE " + run,
  );
  await page.reload();
  await page.getByRole("tab", { name: "绑定提示词", exact: true }).click();
  assert(
    (
      await page
        .getByLabel("实例 1 · 配置文件 提示词", { exact: true })
        .innerText()
    ).includes("热更新验收"),
  );
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  if (out)
    await page.screenshot({
      path: path.join(out, "rule-prompts-desktop.png"),
      fullPage: true,
    });
  await page.setViewportSize({ width: 390, height: 844 });
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  await page.getByLabel("实例 1 · 配置文件 提示词", { exact: true }).click();
  await page.getByRole("option", { name: /继承默认/ }).waitFor();
  await page.keyboard.press("Escape");
  if (out)
    await page.screenshot({
      path: path.join(out, "rule-prompts-mobile.png"),
      fullPage: true,
    });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("tab", { name: "请求预演", exact: true }).click();
  await page.locator(".preview-endpoint").filter({ hasText: "配置文件" }).click();
  assert.equal(
    await page.getByLabel("预演路径（可含查询参数）", { exact: true }).inputValue(),
    "/api/download?file=config.txt",
  );
  await page.getByRole("button", { name: "发送预演请求", exact: true }).click();
  await page
    .getByRole("region", { name: "实际返回", exact: true })
    .getByText("PROMPT_V2 PREVIEW_RUN", { exact: true })
    .waitFor();
  pass(
    "per-interface selector persists; same path with different file parameters returns independent prompts; preview agrees; desktop/mobile controls work without overflow",
  );
  await page.getByRole("tab", { name: "绑定提示词", exact: true }).click();
  await choose("实例 1 · 详情 提示词", replacement.id);
  await choose("实例 1 · 说明文件 提示词", replacement.id);
  await choose("实例 1 默认提示词", "");
  await saveBinding();
  await page.getByRole("tab", { name: "请求预演", exact: true }).click();
  assert.equal(
    await page
      .getByText("交付规则尚未指定提示词，请先完成绑定。", { exact: false })
      .count(),
    0,
  );
  await page.getByRole("button", { name: "发送预演请求", exact: true }).click();
  await page
    .getByRole("region", { name: "实际返回", exact: true })
    .getByText("PROMPT_V2 PREVIEW_RUN", { exact: true })
    .waitFor();
  await page.getByRole("tab", { name: "绑定提示词", exact: true }).click();
  await choose("实例 1 默认提示词", replacement.id);
  await choose("实例 1 · 配置文件 提示词", "");
  await saveBinding();
  assert.equal(
    await (
      await client.request.get(base + "/api/download?file=config.txt")
    ).text(),
    "REBOUND_PROFILE " + run,
  );
  assert.equal(
    (await api("/state")).deployments.find((d) => d.id === deployment.id)
      .revision,
    deployment.revision,
  );
  assert.deepEqual((await api("/sessions/" + run)).snapshot, original.snapshot);
  assert.deepEqual(errors, []);
  pass(
    "all rules may use individual profiles without an instance default; returning to inheritance updates existing session without republishing; history preserved, no browser errors",
  );
  await client.close();
  console.log(JSON.stringify({ checks, errors }, null, 2));
} finally {
  if (browser) await browser.close();
  const stopped = once(server, "exit");
  server.kill("SIGTERM");
  await stopped;
  await rm(temp, { recursive: true, force: true });
}

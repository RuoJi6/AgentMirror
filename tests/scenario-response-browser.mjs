// Browser plugin not available. Use Playwright against an isolated Go server.
// Flow: edit ordinary and prompt responses -> save -> preview actual content.
import assert from "node:assert/strict";
import { once } from "node:events";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { spawn } from "node:child_process";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { chromium } from "playwright";

const root = path.resolve(import.meta.dirname, "..");
const temp = await mkdtemp(
  path.join(os.tmpdir(), "agentmirror-response-editor-"),
);
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
    name: "响应测试提示词",
    body: "PROMPT_CANARY",
    category: "unexpected_output",
  });
  const workspace = await api("/workspaces", {
    name: "响应编辑验收",
    slug: "response-editor-qa",
    bindings: [],
  });
  const page = await context.newPage();
  page.setDefaultTimeout(10000);
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (e) => {
    if (e.type() === "error") errors.push(e.text());
  });
  await page.goto(admin + "/#composer/" + workspace.id);
  assert.match(await page.title(), /AgentMirror/);
  assert.equal(new URL(page.url()).hash, "#composer/" + workspace.id);
  await page.getByRole("tab", { name: "模拟场景", exact: true }).click();
  await page.getByRole("button", { name: "新建模拟场景", exact: true }).click();
  const dialog = page.getByRole("dialog");
  const bodyInput = dialog.getByLabel("命中响应正文", { exact: true });
  const choose = async (label, value) => {
    await dialog.getByLabel(label, { exact: true }).click();
    await page.locator(`[role="option"][data-select-value="${value}"]`).click();
  };
  const saveScenario = async () => {
    const response = page.waitForResponse(
      (r) =>
        r.url().endsWith("/api/scenarios") && r.request().method() === "POST",
    );
    await dialog
      .getByRole("button", { name: "保存模拟场景", exact: true })
      .click();
    const saved = await response;
    assert.equal(saved.status(), 200, await saved.text());
    const result = await saved.json();
    await dialog.waitFor({ state: "hidden" });
    return result;
  };
  const reopen = async () => {
    await page.getByRole("tab", { name: "模拟场景", exact: true }).click();
    await page
      .locator(".material-scenario-row")
      .filter({ hasText: "普通响应切换验收" })
      .getByRole("button", { name: "编辑", exact: true })
      .click();
  };
  await dialog.getByLabel("场景名称", { exact: true }).fill("普通响应切换验收");
  await dialog.getByLabel("请求路径", { exact: true }).fill("/api/ordinary");
  assert((await bodyInput.inputValue()).includes("{{prompt}}"));
  await bodyInput.fill("NORMAL_OK");
  await dialog.getByText("普通响应", { exact: true }).waitFor();
  let scenario = await saveScenario();
  assert.equal(scenario.rules[0].delivery_required, false);
  assert.equal(scenario.rules[0].response.body, "NORMAL_OK");
  await page.getByRole("button", { name: "保存工作区", exact: true }).click();
  await page
    .getByRole("status")
    .filter({ hasText: "工作区草稿已保存" })
    .waitFor();
  await page.getByRole("tab", { name: "请求预演", exact: true }).click();
  await page
    .locator(".preview-endpoint")
    .filter({ hasText: "/api/ordinary" })
    .click();
  await page.getByRole("button", { name: "发送预演请求", exact: true }).click();
  await page
    .getByRole("region", { name: "实际返回", exact: true })
    .getByText("NORMAL_OK", { exact: true })
    .waitFor();
  await page.getByText("未交付提示词", { exact: true }).waitFor();
  pass(
    "replace seeded prompt response with ordinary text: save succeeds, preview returns exact text and no prompt delivery",
  );

  await reopen();
  await choose("响应格式", "json");
  await bodyInput.fill(
    String.raw`{ "id": 9007199254740993, "message": "\u007b\u007b prompt \u007d\u007d", "status": "ok" }`,
  );
  await dialog.getByText("交付提示词", { exact: true }).waitFor();
  await dialog
    .getByRole("button", { name: "改为普通响应", exact: true })
    .click();
  assert.equal(
    await bodyInput.inputValue(),
    '{ "id": 9007199254740993, "message": "", "status": "ok" }',
  );
  scenario = await saveScenario();
  assert.equal(scenario.rules[0].delivery_required, false);
  assert(scenario.rules[0].response.body.includes("9007199254740993"));
  pass(
    "JSON escaped prompt converts to ordinary response without losing large integer precision or other fields",
  );

  await reopen();
  await choose("响应格式", "html");
  await bodyInput.fill("<main><h1>OK</h1><p>{{ prompt }}</p></main>");
  await dialog
    .getByRole("button", { name: "改为普通响应", exact: true })
    .click();
  assert.equal(await bodyInput.inputValue(), "<main><h1>OK</h1><p></p></main>");
  await dialog
    .getByRole("checkbox", {
      name: "为条件未命中的请求设置默认响应",
      exact: true,
    })
    .check();
  await dialog
    .getByLabel("默认响应正文", { exact: true })
    .fill("普通拒绝 {{prompt}}");
  await dialog
    .getByRole("alert")
    .filter({ hasText: "默认响应只能返回普通内容" })
    .waitFor();
  await dialog
    .getByRole("button", { name: "移除默认响应中的槽位", exact: true })
    .click();
  scenario = await saveScenario();
  assert.equal(scenario.rules[0].delivery_required, false);
  assert.equal(scenario.rules[0].fallback.body, "普通拒绝 ");
  pass(
    "HTML conversion preserves markup; fallback slot mistake is explained inline and repaired without enabling delivery",
  );

  await reopen();
  await dialog.getByLabel("命中状态码", { exact: true }).fill("204");
  await bodyInput.fill("");
  scenario = await saveScenario();
  assert.equal(scenario.rules[0].response.status, 204);
  assert.equal(scenario.rules[0].delivery_required, false);
  await reopen();
  await dialog.getByRole("button", { name: "添加规则", exact: true }).click();
  await dialog.getByLabel("规则名称", { exact: true }).fill("独立交付");
  await dialog.getByLabel("请求路径", { exact: true }).fill("/api/prompt");
  await bodyInput.fill("result:");
  await dialog
    .getByRole("button", { name: "插入提示词槽位", exact: true })
    .click();
  await dialog.getByText("交付提示词", { exact: true }).waitFor();
  assert.equal(await bodyInput.inputValue(), "result:{{prompt}}");
  await dialog.getByRole("button", { name: "复制规则", exact: true }).click();
  await dialog.getByLabel("请求路径", { exact: true }).fill("/api/plain-copy");
  await bodyInput.fill("COPIED_NORMAL");
  await dialog.getByText("普通响应", { exact: true }).waitFor();
  const out = process.env.QA_OUTPUT_DIR;
  if (out) {
    await mkdir(out, { recursive: true });
    await page.screenshot({
      path: path.join(out, "normal-response-desktop.png"),
    });
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await bodyInput.scrollIntoViewIfNeeded();
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  assert.equal(await page.locator("vite-error-overlay").count(), 0);
  if (out)
    await page.screenshot({
      path: path.join(out, "normal-response-mobile.png"),
    });
  scenario = await saveScenario();
  assert.deepEqual(
    scenario.rules.map((r) => r.delivery_required),
    [false, true, false],
  );
  const bindings = [
    {
      id: "response-check",
      scenario_id: scenario.id,
      profile_id: profile.id,
      enabled: true,
    },
  ];
  const ordinary = await api("/composer/preview", {
    bindings,
    request: { method: "GET", path: "/api/plain-copy" },
  });
  assert.equal(ordinary.body, "COPIED_NORMAL");
  assert.equal(ordinary.deliveries.length, 0);
  const prompt = await api("/composer/preview", {
    bindings,
    request: { method: "GET", path: "/api/prompt" },
  });
  assert.equal(prompt.body, "result:PROMPT_CANARY");
  assert.equal(prompt.deliveries.length, 1);
  const empty = await api("/composer/preview", {
    bindings,
    request: { method: "GET", path: "/api/ordinary" },
  });
  assert.equal(empty.status, 204);
  assert.equal(empty.body, "");
  pass(
    "empty 204, insert slot and copy-to-ordinary stay independent; saved flags, real previews and desktop/mobile UI agree",
  );
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ checks, errors }, null, 2));
} finally {
  if (browser) await browser.close();
  const stopped = once(server, "exit");
  server.kill("SIGTERM");
  await stopped;
  await rm(temp, { recursive: true, force: true });
}

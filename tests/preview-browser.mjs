// Browser plugin not available. Use Playwright against an isolated Go server.
// Flow: workspace -> request preview -> select rule -> compare actual responses.
import assert from "node:assert/strict";
import { once } from "node:events";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { spawn } from "node:child_process";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { chromium } from "playwright";
import {
  previewExamples,
  previewRuleKey,
  previewRules,
} from "../frontend/src/features/preview-requests.js";

const root = path.resolve(import.meta.dirname, "..");
const temp = await mkdtemp(path.join(os.tmpdir(), "agentmirror-preview-"));
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
    name: "预演标记",
    body: "SYNTHETIC_PREVIEW_PROMPT",
    category: "unexpected_output",
  });
  const library = await api("/composer/state");
  const preset = library.presets.scenarios.find((s) =>
    s.name.includes("Nacos"),
  );
  const { id: presetId, ...presetDraft } = preset;
  const scenario = await api("/scenarios", presetDraft);
  const binding = {
    id: "preview-instance",
    scenario_id: scenario.id,
    profile_id: profile.id,
    enabled: true,
    paths: {
      "config-list": "/mapped/configs",
      "config-detail": "/mapped/configs",
    },
  };
  const workspace = await api("/workspaces", {
    name: "Nacos 快捷预演",
    slug: "quick-preview",
    site_id: "",
    bindings: [binding],
  });
  const page = await context.newPage();
  page.setDefaultTimeout(12000);
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (e) => {
    if (e.type() === "error") errors.push(e.text());
  });
  const endpoint = (key) =>
    page.locator(`[data-endpoint-key=${JSON.stringify(key)}]`);
  const choose = async (label, value) => {
    if (label === "从场景规则填入") {
      await endpoint(value).click();
      return;
    }
    await page.getByLabel(label, { exact: true }).click();
    await page
      .locator(`[role="option"][data-select-value=${JSON.stringify(value)}]`)
      .click();
  };
  const openPreview = async (id = workspace.id) => {
    await page.goto(admin + "/#composer/" + id);
    await page.getByRole("tab", { name: "请求预演", exact: true }).click();
  };
  // Reproduce the reported state: a site and saved scenario exist, but the
  // workspace has no bindings. Paths must remain visible and not silently active.
  const site = await api("/sites", {
    name: "文档站点",
    entry: "index.html",
    files: [
      {
        path: "index.html",
        encoding: "utf8",
        content: "<!doctype html><title>文档站点</title><h1>文档中心</h1>",
      },
    ],
  });
  const documents = await api("/scenarios", {
    name: "文档中心",
    rules: [
      {
        id: "download",
        name: "README 文件内容",
        method: "GET",
        path: "/api/download",
        conditions: [condition("query", "file", "README.md")],
        response: response(),
        delivery_required: true,
      },
      {
        id: "login",
        name: "登录验证",
        method: "POST",
        path: "/api/login",
        conditions: [condition("json", "username", "", "exists")],
        response: response("LOGIN_OK"),
        delivery_required: false,
      },
    ],
  });
  const emptyWorkspace = await api("/workspaces", {
    name: "只有站点的工作区",
    slug: "site-only",
    site_id: site.id,
    bindings: [],
  });
  await openPreview(emptyWorkspace.id);
  await page.getByText(/当前工作区尚未加入模拟场景/).waitFor();
  const libraryKey = JSON.stringify(["library", documents.id, "download"]);
  assert((await endpoint(libraryKey).innerText()).includes("/api/download"));
  assert(
    (await endpoint(libraryKey).innerText()).includes('file = "README.md"'),
  );
  assert((await endpoint(libraryKey).innerText()).includes("未加入工作区"));
  const search = page.getByLabel("搜索场景路径或参数", { exact: true });
  await choose("按场景筛选", documents.id);
  assert.equal(await page.locator(".preview-scenario-group").count(), 1);
  assert.equal(await page.locator(".preview-endpoint").count(), 2);
  await search.fill("README.md");
  assert.equal(await page.locator(".preview-endpoint").count(), 1);
  await page.getByLabel("按场景筛选", { exact: true }).click();
  await page.getByRole("option", { name: /全部场景/ }).click();
  await search.fill("");
  assert((await page.locator(".preview-scenario-group").count()) >= 2);
  pass("group scenario endpoints and combine scenario filtering with search");

  await search.fill("not-an-endpoint");
  await page
    .getByText("没有匹配的接口，请尝试其他路径或参数名。", { exact: true })
    .waitFor();
  await search.fill("README.md");
  assert.equal(await page.locator(".preview-endpoint").count(), 1);
  if (process.env.QA_OUTPUT_DIR) {
    await mkdir(process.env.QA_OUTPUT_DIR, { recursive: true });
    await page.screenshot({
      path: path.join(
        process.env.QA_OUTPUT_DIR,
        "endpoint-library-desktop.png",
      ),
    });
  }
  await endpoint(libraryKey).click();
  const dialog = page.getByRole("dialog", {
    name: "使用场景接口",
    exact: true,
  });
  assert(
    await dialog
      .getByRole("button", { name: "添加并填入", exact: true })
      .isDisabled(),
  );
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
  assert.equal(
    (await api("/composer/state")).workspaces.find(
      (w) => w.id === emptyWorkspace.id,
    ).bindings.length,
    0,
  );
  await endpoint(libraryKey).click();
  await choose("此场景使用的提示词", profile.id);
  await dialog.getByRole("button", { name: "添加并填入", exact: true }).click();
  await dialog.waitFor({ state: "hidden" });
  assert.equal(
    await page
      .getByLabel("预演路径（可含查询参数）", { exact: true })
      .inputValue(),
    "/api/download?file=README.md",
  );
  assert.equal(
    (await api("/composer/state")).workspaces.find(
      (w) => w.id === emptyWorkspace.id,
    ).bindings.length,
    0,
  );
  await page.getByRole("button", { name: "发送预演请求", exact: true }).click();
  await page
    .getByRole("region", { name: "实际返回", exact: true })
    .getByText("SYNTHETIC_PREVIEW_PROMPT", { exact: true })
    .waitFor();
  await page.getByRole("button", { name: "保存工作区", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "草稿已保存" }).waitFor();
  const savedBinding = (await api("/composer/state")).workspaces.find(
    (w) => w.id === emptyWorkspace.id,
  ).bindings[0];
  assert.equal(savedBinding.profile_id, profile.id);
  pass(
    "site-only workspace shows library paths and conditions; search/cancel work; explicit add selects prompt, fills request, changes only draft until save",
  );
  await context.grantPermissions(["clipboard-read", "clipboard-write"], {
    origin: admin,
  });
  await page.getByRole("tab", { name: "绑定提示词", exact: true }).click();
  const downloadRow = page
    .locator(".composer-path-binding")
    .filter({ hasText: "README 文件内容" });
  const bindingPath = downloadRow.getByRole("textbox");
  assert.equal(await bindingPath.inputValue(), "/api/download");
  await downloadRow
    .getByRole("button", { name: "复制路径", exact: true })
    .click();
  assert.equal(
    await page.evaluate(() => navigator.clipboard.readText()),
    "/api/download",
  );
  await bindingPath.fill("/custom/download");
  await downloadRow
    .getByRole("button", { name: "复制路径", exact: true })
    .click();
  assert.equal(
    await page.evaluate(() => navigator.clipboard.readText()),
    "/custom/download",
  );
  await downloadRow
    .getByRole("button", { name: "复制示例请求", exact: true })
    .click();
  assert.equal(
    await page.evaluate(() => navigator.clipboard.readText()),
    "GET /custom/download?file=README.md",
  );
  await downloadRow.getByRole("button", { name: "预演", exact: true }).click();
  assert.equal(
    await page
      .getByLabel("预演路径（可含查询参数）", { exact: true })
      .inputValue(),
    "/custom/download?file=README.md",
  );
  await page.getByRole("button", { name: "发送预演请求", exact: true }).click();
  await page
    .getByRole("region", { name: "实际返回", exact: true })
    .getByText("SYNTHETIC_PREVIEW_PROMPT", { exact: true })
    .waitFor();
  await page.getByRole("tab", { name: "绑定提示词", exact: true }).click();
  await bindingPath.fill("");
  await downloadRow
    .getByRole("button", { name: "复制路径", exact: true })
    .click();
  assert.equal(await bindingPath.inputValue(), "/api/download");
  assert.equal(
    await page.evaluate(() => navigator.clipboard.readText()),
    "/api/download",
  );
  const loginRow = page
    .locator(".composer-path-binding")
    .filter({ hasText: "登录验证" });
  await loginRow
    .getByRole("button", { name: "复制示例请求", exact: true })
    .click();
  const loginRequest = await page.evaluate(() =>
    navigator.clipboard.readText(),
  );
  assert(
    loginRequest.startsWith(
      "POST /api/login\ncontent-type: application/json\n\n",
    ),
  );
  assert.equal(JSON.parse(loginRequest.split("\n\n")[1]).username, "preview");
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "binding-copy-desktop.png"),
    });
  await page.setViewportSize({ width: 390, height: 844 });
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  await downloadRow.scrollIntoViewIfNeeded();
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "binding-copy-mobile.png"),
    });
  await page.setViewportSize({ width: 1440, height: 1000 });
  pass(
    "binding paths contain selectable values; clipboard has default/mapped path and full query or JSON request; clearing restores default; mapped preview and mobile layout work",
  );
  await openPreview();
  assert.equal(new URL(page.url()).hash, "#composer/" + workspace.id);
  assert((await page.title()).includes("AgentMirror"));
  assert.equal(await page.locator("vite-error-overlay").count(), 0);
  assert(
    await page
      .getByRole("heading", { name: "请求预演", exact: true })
      .isVisible(),
  );
  assert.equal(
    await page
      .getByRole("button", { name: "发送预演请求", exact: true })
      .isDisabled(),
    false,
  );
  assert.equal(
    await page.locator(".preview-advanced").getAttribute("open"),
    null,
  );
  const mappedEndpoint = endpoint(previewRuleKey(binding.id, "config-detail"));
  assert((await mappedEndpoint.innerText()).includes("/mapped/configs"));
  assert(
    (await mappedEndpoint.innerText()).includes(
      "场景原路径：/nacos/v1/cs/configs",
    ),
  );
  await choose("从场景规则填入", previewRuleKey(binding.id, "config-detail"));
  const pathInput = page.getByLabel("预演路径（可含查询参数）", {
    exact: true,
  });
  assert.equal(
    await pathInput.inputValue(),
    "/mapped/configs?dataId=application.yml&group=DEFAULT_GROUP",
  );
  await page
    .getByRole("button", { name: "对比条件与空参数", exact: true })
    .click();
  await page
    .getByRole("region", { name: "条件示例", exact: true })
    .getByText("SYNTHETIC_PREVIEW_PROMPT", { exact: false })
    .waitFor();
  const control = page.getByRole("region", { name: "空参数对照", exact: true });
  assert((await control.innerText()).includes("HTTP 404"));
  assert(!(await control.innerText()).includes("SYNTHETIC_PREVIEW_PROMPT"));
  pass(
    "no-site workspace, mapped Nacos auto-fill, collapsed advanced fields, real prompt/control comparison",
  );
  if (process.env.QA_OUTPUT_DIR) {
    await mkdir(process.env.QA_OUTPUT_DIR, { recursive: true });
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "preview-desktop.png"),
    });
  }
  await page.setViewportSize({ width: 390, height: 844 });
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  assert(
    await page
      .getByRole("button", { name: "对比条件与空参数", exact: true })
      .isVisible(),
  );
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "preview-mobile.png"),
    });
  if (process.env.QA_OUTPUT_DIR) {
    await page
      .getByRole("region", { name: "条件示例", exact: true })
      .scrollIntoViewIfNeeded();
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "preview-mobile-result.png"),
    });
  }
  await page.setViewportSize({ width: 1440, height: 1000 });
  await pathInput.fill("/mapped/configs?dataId=missing");
  assert.equal(await page.locator(".preview-response").count(), 0);
  assert(
    await page
      .getByRole("button", { name: "对比条件与空参数", exact: true })
      .isDisabled(),
  );
  await pathInput.press("Control+Enter");
  await page
    .getByRole("region", { name: "实际返回", exact: true })
    .getByText("HTTP 404", { exact: true })
    .waitFor();
  await page.getByRole("tab", { name: "绑定提示词", exact: true }).click();
  await page
    .locator(".composer-path-binding")
    .filter({ hasText: "application.yml" })
    .getByRole("button", { name: "预演", exact: true })
    .click();
  assert.equal(
    await pathInput.inputValue(),
    "/mapped/configs?dataId=application.yml&group=DEFAULT_GROUP",
  );
  await pathInput.fill("/custom/path");
  await page.getByRole("tab", { name: "AI 助手", exact: true }).click();
  await page.getByRole("tab", { name: "请求预演", exact: true }).click();
  assert.equal(await pathInput.inputValue(), "/custom/path");
  pass(
    "manual edits invalidate results, keyboard send, binding shortcut fills conditions, tab switching preserves edits",
  );

  // Check generated requests against the real Go matcher for every condition
  // source, repeated values, nested JSON and prototype-like literal field names.
  const cases = [
    [
      condition("query", "name", "中 文 &+?"),
      condition("query", "name", "other"),
    ],
    [
      condition("header", "X-Test", "abc"),
      condition("header", "x-test", "b", "contains"),
    ],
    [
      condition("form", "action", "read"),
      condition("form", "token", "", "exists"),
    ],
    [
      condition("json", "items.0.name", "demo"),
      condition("json", "items.01.id", "second"),
      condition("json", "/a~1b/~0x", "escaped"),
      condition("json", "__proto__.polluted", "safe"),
    ],
    [
      condition("body", "", "first", "contains"),
      condition("body", "", "second", "contains"),
    ],
    [
      condition("body", "", '{"action":"read"}'),
      condition("json", "action", "read"),
    ],
    [],
  ];
  const rules = cases.map((conditions, index) => ({
    id: "case-" + index,
    name: "条件 " + index,
    method: "POST",
    path: "/test/" + index,
    conditions,
    response: response(),
    fallback: response("NOT_MATCHED", 400),
    delivery_required: true,
  }));
  const samples = await api("/scenarios", { name: "条件素材", rules });
  const sampleBinding = {
    id: "sample-instance",
    scenario_id: samples.id,
    profile_id: profile.id,
    enabled: true,
  };
  for (const rule of samples.rules) {
    const examples = previewExamples(rule);
    const result = await api("/composer/preview", {
      bindings: [sampleBinding],
      request: examples.request,
    });
    assert.equal(result.rule_matched, true, rule.id);
    assert.equal(result.rule_id, rule.id);
  }
  assert.equal({}.polluted, undefined);
  assert(
    previewExamples({
      ...rules[0],
      conditions: [
        condition("header", "X-Test", "a"),
        condition("header", "x-test", "b"),
      ],
    }).notes.length > 0,
  );
  assert.equal(previewExamples(rules.at(-1)).control, null);
  assert.equal(
    previewRules([{ ...sampleBinding, enabled: false }], [samples]).length,
    0,
  );
  pass(
    "generated query/header/form/JSON/body samples match actual backend; conflicts warn; unconditional rules have no fake control",
  );

  const sampleWorkspace = await api("/workspaces", {
    name: "请求正文示例",
    slug: "body-preview",
    bindings: [sampleBinding],
  });
  await openPreview(sampleWorkspace.id);
  await choose("从场景规则填入", previewRuleKey(sampleBinding.id, "case-3"));
  await page.locator(".preview-advanced summary").click();
  assert(
    (await page.getByLabel("请求正文", { exact: true }).inputValue()).includes(
      '"items"',
    ),
  );
  await page.getByRole("button", { name: "发送预演请求", exact: true }).click();
  await page.getByText("提示词已交付 · 1 处", { exact: true }).waitFor();
  await choose("预演方法", "HEAD");
  // HEAD intentionally cannot match this POST rule and must never report delivery.
  await page.getByRole("button", { name: "发送预演请求", exact: true }).click();
  await page.getByText("未交付提示词", { exact: true }).waitFor();
  pass(
    "advanced body editor reveals auto-filled JSON; actual result and HEAD delivery badges",
  );

  // A route without parameters may intentionally be the earlier rule. Do not
  // label the later selected rule as a hit just because a response was returned.
  const overlap = await api("/scenarios", {
    name: "路由重叠",
    rules: [
      {
        ...rules[0],
        id: "first",
        name: "优先规则",
        conditions: [],
        path: "/shared",
      },
      { ...rules[0], id: "second", name: "后续规则", path: "/shared" },
    ],
  });
  const overlapBinding = { ...sampleBinding, scenario_id: overlap.id };
  const overlapWorkspace = await api("/workspaces", {
    name: "重叠示例",
    slug: "overlap",
    bindings: [overlapBinding],
  });
  await openPreview(overlapWorkspace.id);
  await choose("从场景规则填入", previewRuleKey(overlapBinding.id, "second"));
  await page
    .getByRole("button", { name: "对比条件与空参数", exact: true })
    .click();
  await page
    .getByText(/实际命中了其他规则：/)
    .first()
    .waitFor();
  await page.getByRole("tab", { name: "绑定提示词", exact: true }).click();
  await choose("实例 1 默认提示词", "");
  await page.getByRole("tab", { name: "请求预演", exact: true }).click();
  assert(
    await page
      .getByText("交付规则尚未指定提示词，请先完成绑定。", { exact: false })
      .isVisible(),
  );
  assert(
    await page
      .getByRole("button", { name: "发送预演请求", exact: true })
      .isDisabled(),
  );
  await page.getByRole("button", { name: "绑定提示词", exact: true }).click();
  await page.getByRole("checkbox", { name: "启用", exact: true }).uncheck();
  await page.getByRole("tab", { name: "请求预演", exact: true }).click();
  assert(
    await page.getByText("尚无可预演的内容。", { exact: false }).isVisible(),
  );
  assert(
    await page
      .getByRole("button", { name: "发送预演请求", exact: true })
      .isDisabled(),
  );
  const disabledKey = previewRuleKey(overlapBinding.id, "second");
  assert((await endpoint(disabledKey).innerText()).includes("已停用"));
  await endpoint(disabledKey).click();
  await choose("此场景使用的提示词", profile.id);
  await page
    .getByRole("dialog", { name: "使用场景接口", exact: true })
    .getByRole("button", { name: "启用并填入", exact: true })
    .click();
  assert((await endpoint(disabledKey).innerText()).includes("已加入工作区"));
  assert.equal(await page.locator(".preview-endpoint.selected").count(), 1);
  await page.getByRole("button", { name: "发送预演请求", exact: true }).click();
  await page
    .getByRole("region", { name: "实际返回", exact: true })
    .getByText("SYNTHETIC_PREVIEW_PROMPT", { exact: true })
    .waitFor();
  assert.equal((await api("/state")).stats.sessions, 0);
  pass(
    "earlier same-route matches identified; disabled binding reuses its instance; previews create no sessions",
  );
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({ checks, pageAndConsoleErrors: errors }, null, 2),
  );
} finally {
  if (browser) await browser.close();
  const stopped = once(server, "exit");
  server.kill("SIGTERM");
  await stopped;
  await rm(temp, { recursive: true, force: true });
}

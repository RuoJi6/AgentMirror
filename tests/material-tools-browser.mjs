import { clearBuiltinTestMaterials } from "./authoring-fixture.mjs";
// Browser plugin not available. Use Playwright against an isolated Go server.
// Exercise saved-material edits and enforce per-turn module scope with an
// intentionally incorrect local provider. No real model or user data is used.
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
const temp = await mkdtemp(
  path.join(os.tmpdir(), "agentmirror-material-tools-"),
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
  await clearBuiltinTestMaterials(context.request, admin);
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
    name: "编辑目标提示词",
    body: "BEFORE",
    category: "unexpected_output",
  });
  const site = await api("/sites", {
    name: "站点原名",
    entry: "index.html",
    files: [
      { path: "index.html", encoding: "utf8", content: "<h1>原页面</h1>\n" },
    ],
  });
  const responseRule = (id, path, body) => ({
    id,
    method: "GET",
    path,
    conditions: [],
    response: { status: 200, format: "text", content_type: "text/plain", body },
    delivery_required: false,
  });
  const scenario = await api("/scenarios", {
    name: "场景原名",
    rules: [responseRule("info", "/info", "BEFORE")],
  });
  const unused = await api("/scenarios", {
    name: "由 Agent 删除",
    rules: [responseRule("unused", "/unused", "UNUSED")],
  });
  const workspace = await api("/workspaces", {
    name: "素材编辑验收",
    slug: "material-tools",
    site_id: site.id,
    bindings: [
      {
        id: "binding",
        scenario_id: scenario.id,
        enabled: true,
        profile_id: profile.id,
      },
    ],
  });
  await api(`/workspaces/${workspace.id}/publish`, {});
  const tool = (name, args) => ({ name, arguments: JSON.stringify(args) });
  const lastTool = (input) =>
    JSON.parse(input.messages.filter((m) => m.role === "tool").at(-1).content);
  const steps = [
    () => tool("load_material", { kind: "scenario", id: scenario.id }),
    () =>
      tool("upsert_rule", {
        rule_id: "info",
        rule: responseRule("info", "/saved", "AFTER"),
      }),
    () =>
      tool("save_material", {
        kind: "scenario",
        id: scenario.id,
        version: scenario.version,
        json: JSON.stringify({ name: "已修改场景" }),
      }),
    () => tool("read_material", { kind: "profile", id: profile.id }),
    () =>
      tool("save_material", {
        kind: "profile",
        id: profile.id,
        version: profile.version,
        json: JSON.stringify({ body: "AFTER_PROFILE" }),
      }),
    () => tool("read_material", { kind: "scenario", id: unused.id }),
    () =>
      tool("delete_material", {
        kind: "scenario",
        id: unused.id,
        version: unused.version,
      }),
    () => tool("load_material", { kind: "site", id: site.id }),
    () => tool("read_file", { path: "index.html" }),
    (input) =>
      tool("append_file", {
        path: "index.html",
        expected_sha256: lastTool(input).sha256,
        content: "\n<footer>更新页脚</footer>\n",
      }),
    () =>
      tool("save_material", {
        kind: "site",
        id: site.id,
        version: site.version,
        json: JSON.stringify({ name: "已修改站点" }),
      }),
  ];
  let calls = 0;
  let replySummary = "已保存指定素材并删除未引用场景。";
  modelServer = createServer(async (req, res) => {
    let body = "";
    for await (const chunk of req) body += chunk;
    const input = JSON.parse(body);
    res.setHeader("Content-Type", "application/json");
    const step = steps.shift();
    calls++;
    res.end(
      JSON.stringify({
        choices: [
          {
            message: step
              ? {
                  content: null,
                  tool_calls: [
                    {
                      id: `step${calls}`,
                      type: "function",
                      function: step(input),
                    },
                  ],
                }
              : { content: replySummary },
          },
        ],
      }),
    );
  });
  modelServer.listen(0, "127.0.0.1");
  await once(modelServer, "listening");
  await api("/generation/providers", {
    name: "素材操作验收",
    protocol: "openai",
    base_url: `http://127.0.0.1:${modelServer.address().port}`,
    model: "synthetic-tools",
    set_default: true,
  });
  const page = await context.newPage();
  page.setDefaultTimeout(15000);
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (e) => {
    if (e.type() === "error") errors.push(e.text());
  });
  await page.goto(admin + "/#composer/" + workspace.id);
  await page
    .getByRole("heading", { name: workspace.name, exact: true })
    .waitFor();
  await page
    .locator(".agent-mention-input textarea")
    .fill(
      "修改当前站点的页脚并保存；修改场景 /info 为 /saved 并保存；修改提示词为 AFTER_PROFILE；删除未引用场景『由 Agent 删除』。",
    );
  await page.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await page
    .getByText("已保存指定素材并删除未引用场景。", { exact: true })
    .waitFor();
  assert.equal(calls, 12);
  assert.equal(
    await page.locator('.agent-tool-call[data-status="failed"]').count(),
    0,
  );
  await page.getByRole("tab", { name: "模拟场景", exact: true }).click();
  await page
    .locator(".material-scenario-row")
    .filter({ hasText: "已修改场景" })
    .waitFor();
  assert.equal(
    await page
      .locator(".material-scenario-row")
      .filter({ hasText: "由 Agent 删除" })
      .count(),
    0,
  );
  assert(
    await page
      .getByRole("button", { name: "保存工作区", exact: true })
      .isDisabled(),
    "automatic library refresh incorrectly made workspace dirty",
  );
  await page
    .getByText(`已同步 v${workspace.version}`, { exact: true })
    .waitFor();
  const savedScenario = await api("/scenarios/" + scenario.id);
  assert.equal(savedScenario.version, 2);
  assert.equal(savedScenario.rules[0].path, "/saved");
  const savedProfile = (await api("/state")).profiles.find(
    (item) => item.id === profile.id,
  );
  assert.equal(savedProfile.version, 2);
  assert.equal(savedProfile.body, "AFTER_PROFILE");
  const savedSite = await api("/sites/" + site.id);
  assert.equal(savedSite.version, 2);
  assert.match(savedSite.files[0].content, /更新页脚/);
  const stateAfter = await api("/composer/state");
  assert.equal(stateAfter.sites.length, 1);
  assert.equal(stateAfter.scenarios.length, 1);
  const savedWorkspace = await api("/workspaces/" + workspace.id);
  assert.equal(savedWorkspace.version, savedWorkspace.published_version);
  pass(
    "Agent edits saved site/scenario/profile without duplicates, deletes an unreferenced scenario, and refreshes the UI without reload",
  );
  await page.getByRole("tab", { name: "请求预演", exact: true }).click();
  await page
    .locator(".preview-scenario-group")
    .getByRole("heading", { name: /已修改场景/ })
    .waitFor();
  await page.locator(".preview-endpoint").filter({ hasText: "/saved" }).click();
  assert.equal(
    await page
      .getByLabel("预演路径（可含查询参数）", { exact: true })
      .inputValue(),
    "/saved",
  );
  await page.getByRole("button", { name: "发送预演请求", exact: true }).click();
  await page
    .getByRole("region", { name: "实际返回", exact: true })
    .getByText("AFTER", { exact: true })
    .waitFor();
  pass(
    "scenario-group preview immediately uses the Agent's saved route and response",
  );
  if (process.env.QA_OUTPUT_DIR) {
    await mkdir(process.env.QA_OUTPUT_DIR, { recursive: true });
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "saved-materials-preview.png"),
    });
  }

  await page.getByRole("tab", { name: "AI 助手", exact: true }).click();
  await page.getByRole("button", { name: "新建对话", exact: true }).click();
  const input = page.locator(".agent-mention-input textarea");
  await input.fill("@已修改场景");
  await page
    .locator(".agent-mention-menu")
    .getByRole("option")
    .filter({ hasText: "已修改场景" })
    .click();
  await input.fill("修改：把返回正文改为新内容，保留其他信息");
  replySummary = "只更新选中的模拟场景，站点内容保持原样。";
  steps.push(
    (request) => {
      assert(!request.tools.some((t) => t.function.name === "write_file"));
      return tool("write_file", {
        path: "index.html",
        content: "WRONG MODULE",
      });
    },
    (request) => {
      assert.match(JSON.stringify(lastTool(request)), /本轮修改范围/);
      return tool("read_file", { path: "index.html" });
    },
    (request) => {
      assert.equal(lastTool(request).content, savedSite.files[0].content);
      return tool("load_material", { kind: "scenario", id: scenario.id });
    },
    () =>
      tool("upsert_rule", {
        rule_id: "info",
        rule: responseRule("info", "/saved", "SCENARIO_ONLY"),
      }),
    () =>
      tool("save_material", {
        kind: "scenario",
        id: scenario.id,
        version: savedScenario.version,
      }),
  );
  await page.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await page.getByText(replySummary, { exact: true }).waitFor();
  await page.getByText("修改范围：模拟场景", { exact: true }).waitFor();
  assert.equal(
    await page.locator('.agent-tool-call[data-status="failed"]').count(),
    1,
  );
  assert.deepEqual(await api("/sites/" + site.id), savedSite);
  assert.deepEqual(
    (await api("/state")).profiles.find((p) => p.id === profile.id),
    savedProfile,
  );
  const scopedScenario = await api("/scenarios/" + scenario.id);
  assert.equal(scopedScenario.version, 3);
  assert.equal(scopedScenario.rules[0].response.body, "SCENARIO_ONLY");
  assert.equal((await api("/composer/state")).scenarios.length, 1);
  pass(
    "@scenario restricts writes, rejects an unoffered site rewrite, permits reading context and saves only the existing scenario",
  );

  // Explicit scope is submitted, recorded, and reset for the next turn.
  await page.getByRole("combobox", { name: "本轮修改范围" }).click();
  await page
    .getByRole("option", { name: "仅问答 · 不修改素材", exact: true })
    .click();
  await input.fill("解释当前内容");
  replySummary = "问答已完成，未修改素材。";
  steps.push(
    (request) => {
      assert(!request.tools.some((t) => t.function.name === "save_material"));
      return tool("delete_material", {
        kind: "scenario",
        id: scenario.id,
        version: scopedScenario.version,
      });
    },
    (request) => {
      assert.match(JSON.stringify(lastTool(request)), /本轮修改范围/);
      return tool("read_scenario", {});
    },
  );
  await page.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await page.getByText(replySummary, { exact: true }).waitFor();
  await page
    .getByText("修改范围：仅问答，不修改素材", { exact: true })
    .waitFor();
  assert.equal(
    await page
      .getByRole("combobox", { name: "本轮修改范围" })
      .getAttribute("data-value"),
    "auto",
  );
  assert.deepEqual(await api("/scenarios/" + scenario.id), scopedScenario);
  await page.reload();
  await page.getByText("修改范围：模拟场景", { exact: true }).waitFor();
  await page
    .getByText("修改范围：仅问答，不修改素材", { exact: true })
    .waitFor();
  pass(
    "manual read-only scope blocks deletion, resets per turn, and remains visible in restored history",
  );
  await input.fill("修改当前工作区的回传路径为 /agent/receipt");
  replySummary = "当前工作区回传路径已保存。";
  steps.push(
    (request) => {
      assert(
        request.tools.some((t) => t.function.name === "set_callback_path"),
      );
      assert(!request.tools.some((t) => t.function.name === "write_file"));
      return tool("read_callback", {});
    },
    (request) => {
      assert.equal(lastTool(request).workspace_id, workspace.id);
      return tool("set_callback_path", {
        path: "/agent/receipt",
        version: lastTool(request).version,
      });
    },
  );
  await page.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await page.getByText(replySummary, { exact: true }).waitFor();
  await page.getByText("修改范围：回传设置", { exact: true }).waitFor();
  let callbackWorkspace = await api("/workspaces/" + workspace.id);
  assert.equal(callbackWorkspace.callback_path, "/agent/receipt");
  assert.equal(callbackWorkspace.version, callbackWorkspace.published_version);
  assert.deepEqual(await api("/sites/" + site.id), savedSite);
  assert.deepEqual(await api("/scenarios/" + scenario.id), scopedScenario);
  await page.getByRole("button", { name: "工作区设置", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "工作区设置", exact: true });
  assert.equal(
    await dialog.getByLabel("回传路径", { exact: true }).inputValue(),
    "/agent/receipt",
  );
  await dialog.getByLabel("回传路径", { exact: true }).fill("/user/receipt");
  if (process.env.QA_OUTPUT_DIR) {
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "callback-settings.png"),
    });
  }
  await dialog.getByRole("button", { name: "保存设置", exact: true }).click();
  await dialog.waitFor({ state: "hidden" });
  callbackWorkspace = await api("/workspaces/" + workspace.id);
  assert.equal(callbackWorkspace.callback_path, "/user/receipt");
  assert.equal(callbackWorkspace.version, callbackWorkspace.published_version);
  pass(
    "Agent and user can change the current workspace callback path; both saves synchronize its published version without changing materials",
  );
  await input.fill(
    "修改当前工作区的令牌失效响应为 HTTP 409，正文为 EXPIRED_BY_AGENT",
  );
  replySummary = "当前工作区失效响应已单独保存。";
  steps.push(
    (request) => {
      assert(
        request.tools.some((t) => t.function.name === "set_callback_response"),
      );
      assert(!request.tools.some((t) => t.function.name === "write_file"));
      return tool("read_callback", {});
    },
    (request) =>
      tool("set_callback_response", {
        version: lastTool(request).version,
        json: JSON.stringify({
          enabled: true,
          status: 409,
          body: JSON.stringify({ error: "EXPIRED_BY_AGENT" }),
        }),
      }),
  );
  await page.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await page.getByText(replySummary, { exact: true }).waitFor();
  callbackWorkspace = await api("/workspaces/" + workspace.id);
  assert.equal(callbackWorkspace.collect_rejected_response.status, 409);
  assert.equal(callbackWorkspace.version, callbackWorkspace.published_version);
  assert.deepEqual(await api("/sites/" + site.id), savedSite);
  assert.deepEqual(await api("/scenarios/" + scenario.id), scopedScenario);
  pass(
    "Agent edits only the current workspace rejection response and immediately synchronizes it",
  );
  if (process.env.QA_OUTPUT_DIR) {
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "agent-scope-desktop.png"),
    });
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await page
    .getByRole("combobox", { name: "本轮修改范围" })
    .scrollIntoViewIfNeeded();
  assert(
    await page.getByRole("combobox", { name: "本轮修改范围" }).isVisible(),
  );
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  );
  if (process.env.QA_OUTPUT_DIR) {
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "agent-scope-mobile.png"),
    });
  }
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("tab", { name: "发布设置", exact: true }).click();
  const rejectionPanel = page.locator(".collect-response-settings").filter({
    has: page.getByRole("heading", { name: "令牌失效响应", exact: true }),
  });
  await rejectionPanel.getByLabel("回传响应状态码", { exact: true }).waitFor();
  assert.equal(
    await rejectionPanel
      .getByLabel("回传响应状态码", { exact: true })
      .inputValue(),
    "409",
  );
  await rejectionPanel
    .getByRole("checkbox", { name: "启用自定义令牌失效响应" })
    .check();
  await rejectionPanel
    .getByLabel("回传响应状态码", { exact: true })
    .fill("401");
  await rejectionPanel
    .getByLabel("回传响应正文", { exact: true })
    .fill('{"message":"令牌已失效","run":"{{run_id}}"}');
  await rejectionPanel
    .getByRole("button", { name: "预览回传响应", exact: true })
    .click();
  await rejectionPanel.getByText("HTTP 401", { exact: true }).waitFor();
  assert.match(
    await rejectionPanel.getByLabel("预览响应正文").textContent(),
    /PREVIEW_RUN/,
  );
  await rejectionPanel
    .getByRole("button", { name: "保存回传响应", exact: true })
    .click();
  await page.getByText("令牌失效响应已生效", { exact: true }).waitFor();
  await page.reload();
  await page.getByRole("tab", { name: "发布设置", exact: true }).click();
  await rejectionPanel.getByLabel("回传响应正文", { exact: true }).waitFor();
  assert.equal(
    await rejectionPanel
      .getByLabel("回传响应状态码", { exact: true })
      .inputValue(),
    "401",
  );
  assert.equal(
    (await api(`/workspaces/${workspace.id}/collect-rejected-response`))
      .enabled,
    true,
  );
  assert.equal((await api("/collect-response")).enabled, false);
  await rejectionPanel.scrollIntoViewIfNeeded();
  if (process.env.QA_OUTPUT_DIR) {
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "expired-token-settings.png"),
    });
  }
  await rejectionPanel
    .getByRole("button", { name: "恢复默认配置", exact: true })
    .click();
  await rejectionPanel
    .getByRole("button", { name: "保存回传响应", exact: true })
    .click();
  await page.getByText("已恢复默认令牌失效响应", { exact: true }).waitFor();
  assert.equal(
    (await api(`/workspaces/${workspace.id}/collect-rejected-response`))
      .enabled,
    false,
  );
  assert.equal(
    (await api(`/workspaces/${workspace.id}/collect-rejected-response`)).status,
    403,
  );
  pass(
    "expired-token response UI supports edit, preview, save, reload and restore default 403 without changing success response settings",
  );
  // Reproduce stale references restored from history, then a save after the
  // browser's catalogue was loaded. Both must resolve the same IDs to latest.
  await page.getByRole("tab", { name: "AI 助手", exact: true }).click();
  await page.getByRole("button", { name: "新建对话", exact: true }).click();
  for (const name of [savedSite.name, scopedScenario.name]) {
    await input.fill("@" + name);
    await page
      .locator(".agent-mention-menu")
      .getByRole("option")
      .filter({ hasText: name })
      .click();
  }
  await input.fill("解释选中的站点和场景，不需要修改");
  replySummary = "已读取原始引用版本。";
  steps.push(() => tool("read_reference", { kind: "site", id: site.id }));
  await page.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await page.getByText(replySummary, { exact: true }).waitFor();
  const originalTurn = (await api("/generation/jobs")).items.find(
    (item) => item.prompt === "解释选中的站点和场景，不需要修改",
  );
  const originalReferences = originalTurn.references;
  let externallyEditedSite = await api("/sites", {
    ...savedSite,
    name: "外部修改后的站点",
  });
  let externallyEditedScenario = await api("/scenarios", {
    ...scopedScenario,
    name: "外部修改后的场景",
  });
  await page.reload();
  const composerChips = page.locator(
    ".agent-mention-input .agent-reference-chips",
  );
  await composerChips
    .getByText("站点素材 · 外部修改后的站点", { exact: true })
    .waitFor();
  assert.equal(
    await composerChips
      .locator(".agent-reference-chip")
      .filter({ hasText: "外部修改后的站点" })
      .getAttribute("title"),
    `站点素材 · 外部修改后的站点 · v${externallyEditedSite.version}`,
  );
  assert.equal(
    await composerChips
      .locator(".agent-reference-chip")
      .filter({ hasText: "外部修改后的场景" })
      .getAttribute("title"),
    `模拟场景 · 外部修改后的场景 · v${externallyEditedScenario.version}`,
  );
  assert.deepEqual(
    (await api("/generation/jobs/" + originalTurn.id)).references,
    originalReferences,
  );
  // The next changes happen without refreshing the page, exactly between
  // selecting a reference and sending the follow-up.
  externallyEditedSite = await api("/sites", {
    ...externallyEditedSite,
    name: "发送前最新版站点",
  });
  externallyEditedScenario = await api("/scenarios", {
    ...externallyEditedScenario,
    name: "发送前最新版场景",
  });
  replySummary = "续聊已读取最新站点和场景。";
  steps.push(
    () => tool("read_reference", { kind: "site", id: site.id }),
    (request) => {
      assert.equal(lastTool(request).version, externallyEditedSite.version);
      assert.equal(lastTool(request).name, externallyEditedSite.name);
      return tool("read_reference", { kind: "scenario", id: scenario.id });
    },
    (request) => {
      assert.equal(lastTool(request).version, externallyEditedScenario.version);
      assert.equal(lastTool(request).name, externallyEditedScenario.name);
      return tool("read_module", { module: "overview" });
    },
  );
  await input.fill("之前没有出现，是不是因为只有登录之后才可以查看？");
  await page.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await page.getByText(replySummary, { exact: true }).waitFor();
  await composerChips
    .getByText("站点素材 · 发送前最新版站点", { exact: true })
    .waitFor();
  await composerChips
    .getByText("模拟场景 · 发送前最新版场景", { exact: true })
    .waitFor();
  assert.equal(
    await page
      .getByText("引用素材已更新，请重新选择最新版本", { exact: true })
      .count(),
    0,
  );
  const followed = (await api("/generation/jobs")).items.find(
    (item) =>
      item.prompt === "之前没有出现，是不是因为只有登录之后才可以查看？",
  );
  assert.equal(followed.parent_job_id, originalTurn.id);
  assert.deepEqual(
    followed.references.map(({ kind, id, version }) => ({ kind, id, version })),
    [
      { kind: "site", id: site.id, version: externallyEditedSite.version },
      {
        kind: "scenario",
        id: scenario.id,
        version: externallyEditedScenario.version,
      },
    ],
  );
  assert.deepEqual(
    (await api("/generation/jobs/" + originalTurn.id)).references,
    originalReferences,
  );
  if (process.env.QA_OUTPUT_DIR) {
    await input.scrollIntoViewIfNeeded();
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "reference-continuation.png"),
    });
  }
  pass(
    "restored chips follow updated material IDs; follow-up resolves changes made after selection without 409 and preserves historical reference versions",
  );

  // A real Agent tool save is attributed to the execution identity. Manual
  // editing/restoring must not inherit the source carried by the old document.
  let versions = await api(`/profiles/${profile.id}/versions`);
  assert.equal(versions[0].revision_origin.kind, "agent");
  assert(versions[0].revision_origin.agent_name);
  assert.equal(versions[0].revision_origin.workspace_id, workspace.id);
  assert.equal(versions[1].revision_origin.kind, "manual");
  const agentOrigin = structuredClone(versions[0].revision_origin);
  const sourceLabel = `Agent 修改 · ${agentOrigin.agent_name}`;
  await page.goto(`${admin}/#profiles/${profile.id}`);
  await page.getByRole("button", { name: "提示词正文", exact: true }).click();
  await page.getByLabel("提示词正文", { exact: true }).fill("MANUAL_PROFILE");
  await page.getByRole("button", { name: "保存新版本", exact: true }).click();
  await page.waitForFunction(() =>
    document
      .querySelector(".scheme-items .selected")
      ?.textContent.includes("v3"),
  );
  await page.getByRole("button", { name: "版本历史", exact: true }).click();
  const history = page.getByRole("dialog", { name: "版本历史", exact: true });
  const timeline = history.locator(".profile-history-timeline button");
  await timeline.nth(2).waitFor();
  assert.match(await timeline.first().innerText(), /手动保存/);
  assert((await timeline.nth(1).innerText()).includes(sourceLabel));
  await timeline.nth(1).click();
  await history
    .locator(".profile-history-detail")
    .getByText(sourceLabel, { exact: true })
    .waitFor();
  await history.getByRole("tab", { name: "完整内容", exact: true }).click();
  assert.match(
    await history.locator(".profile-history-detail").innerText(),
    /AFTER_PROFILE/,
  );
  if (process.env.QA_OUTPUT_DIR) {
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "profile-origin-desktop.png"),
    });
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await history
    .locator(".profile-history-detail")
    .getByText(sourceLabel, { exact: true })
    .waitFor();
  assert(await history.evaluate((el) => el.scrollWidth <= el.clientWidth + 1));
  if (process.env.QA_OUTPUT_DIR) {
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "profile-origin-mobile.png"),
    });
  }
  await history.getByRole("button", { name: "恢复此版本为新版本" }).click();
  await history.waitFor({ state: "hidden" });
  await page.getByRole("button", { name: "版本历史", exact: true }).click();
  await timeline.nth(3).waitFor();
  assert.match(await timeline.first().innerText(), /v4/);
  assert.match(await timeline.first().innerText(), /手动保存/);
  assert((await timeline.nth(2).innerText()).includes(sourceLabel));
  versions = await api(`/profiles/${profile.id}/versions`);
  assert.equal(versions[0].revision_origin.kind, "manual");
  assert.equal(versions[0].body, "AFTER_PROFILE");
  assert.deepEqual(versions[2].revision_origin, agentOrigin);
  pass(
    "prompt history identifies Agent edits in list/details; manual save and restore keep correct provenance on desktop and mobile",
  );
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ checks, errors }, null, 2));
} finally {
  if (modelServer) await new Promise((resolve) => modelServer.close(resolve));
  if (browser) await browser.close();
  const stopped = once(server, "exit");
  server.kill("SIGTERM");
  await stopped;
  await rm(temp, { recursive: true, force: true });
}

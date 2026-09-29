// Browser plugin not available. Isolated Playwright UI + real sandbox worker;
// a local synthetic model produces tool calls, never a paid provider request.
import assert from "node:assert/strict";
import { once } from "node:events";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { chromium } from "playwright";

const root = path.resolve(import.meta.dirname, "..");
const temp = await mkdtemp(path.join(os.tmpdir(), "agentmirror-review-qa-"));
const free = net.createServer().listen(0, "127.0.0.1");
await once(free, "listening");
const port = free.address().port;
await new Promise((resolve) => free.close(resolve));
const admin = `http://127.0.0.1:${port}`;
const server = spawn(
  path.join(root, "bin/agentmirror"),
  [
    "--admin-host",
    "127.0.0.1",
    "--admin-port",
    String(port),
    "--db",
    path.join(temp, "qa.sqlite3"),
  ],
  {
    cwd: root,
    stdio: ["ignore", "pipe", "pipe"],
    env: {
      ...process.env,
      AGENTMIRROR_BROWSER_WORKER: path.join(
        root,
        "scripts/browser-capture.mjs",
      ),
    },
  },
);
const errors = [],
  checks = [];
const requestedModels = [];
let browser, model;
const pass = (message) => {
  checks.push(message);
  console.log("PASS", message);
};
const output = process.env.QA_OUTPUT_DIR;
const flow = [
  { action: "goto", value: "/" },
  { action: "fill", selector: "#username", value: "review-user" },
  { action: "click", selector: "#login" },
  { action: "assert_visible", selector: "#portal" },
  { action: "click", selector: "#open" },
  { action: "assert_text", selector: "#result", value: "WORKSPACE_CANARY" },
];
try {
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(Error("startup timeout")), 10000);
    server.once("error", reject);
    server.stdout.on("data", (d) => {
      if (d.toString().includes("公告地址")) {
        clearTimeout(timer);
        resolve();
      }
    });
  });
  model = createServer(async (req, res) => {
    let body = "";
    for await (const chunk of req) body += chunk;
    const request = JSON.parse(body),
      messages = request.messages || [];
    requestedModels.push(request.model);
    const currentRequest =
      messages.filter((m) => m.role === "user").at(-1)?.content || "";
    if (
      currentRequest.includes("CHAT_CAPABILITIES") ||
      currentRequest.includes("CHAT_FOLLOWUP")
    ) {
      if (currentRequest.includes("CHAT_FOLLOWUP"))
        assert.ok(
          messages.some(
            (m) => m.role === "assistant" && m.content.includes("核查能力答复"),
          ),
          "follow-up lost previous reply",
        );
      res.setHeader("Content-Type", "application/json");
      res.end(
        JSON.stringify({
          choices: [
            {
              message: {
                role: "assistant",
                content: "核查能力答复：可以检查登录、登录后访问和接口响应。",
              },
            },
          ],
        }),
      );
      return;
    }
    const used = messages
      .filter((m) => m.role === "assistant")
      .flatMap((m) => m.tool_calls || [])
      .map((c) => c.function.name);
    let name = "read_workspace",
      args = {};
    if (used.includes("read_workspace")) {
      name = "run_browser_flow";
      args = { steps: flow };
    }
    if (used.includes("run_browser_flow")) {
      name = "finish_review";
      args = {
        summary:
          "已检查登录、登录后文档入口、接口响应与回传。最终结论以检查证据和覆盖项为准。",
      };
    }
    res.setHeader("Content-Type", "application/json");
    res.end(
      JSON.stringify({
        choices: [
          {
            message: {
              role: "assistant",
              content: null,
              tool_calls: [
                {
                  id: `tool-${used.length}`,
                  type: "function",
                  function: { name, arguments: JSON.stringify(args) },
                },
              ],
            },
          },
        ],
      }),
    );
  }).listen(0, "127.0.0.1");
  await once(model, "listening");
  browser = await chromium.launch({
    headless: true,
    ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH
      ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH }
      : {}),
  });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
    hasTouch: true,
    reducedMotion: "reduce",
  });
  const auth = await (
    await context.request.get(admin + "/api/auth/status")
  ).json();
  const setup = await context.request.post(admin + "/api/auth/setup", {
    headers: { "X-Admin-Token": auth.csrf },
    data: {
      username: "reviewtest",
      password: "SYNTHETIC-review-2026",
      confirm_password: "SYNTHETIC-review-2026",
    },
  });
  assert.equal(setup.status(), 200);
  const state = await (await context.request.get(admin + "/api/state")).json();
  const api = async (route, body, status = 200) => {
    const response = await context.request.fetch(admin + "/api" + route, {
      method: body ? "POST" : "GET",
      headers: { "X-Admin-Token": state.csrf },
      ...(body ? { data: body } : {}),
    });
    assert.equal(response.status(), status, await response.text());
    return response.json();
  };
  await api("/generation/settings", {
    base_url: `http://127.0.0.1:${model.address().port}`,
    model: "review-fixture",
  });
  const alternate = await api("/generation/providers", {
    name: "Alternate model",
    base_url: `http://127.0.0.1:${model.address().port}`,
    model: "review-fixture-alt",
  });
  const profile = await api("/profiles", {
    name: "核查标记",
    category: "unexpected_output",
    body: "WORKSPACE_CANARY {{run_id}} {{token}} {{callback_url}}",
  });
  let site = await api("/sites", {
    name: "登录链路验收",
    entry: "index.html",
    files: [
      {
        path: "index.html",
        encoding: "utf8",
        content: `<!doctype html><html><head><meta charset="utf-8"><title>文档入口</title></head><body><h1>文档入口</h1><label>账号 <input id="username"></label><button id="login">登录</button><section id="portal" hidden><h2>工作台</h2><button id="open">查看说明</button><pre id="result"></pre></section><script>let auth='';document.querySelector('#login').onclick=async()=>{const r=await fetch('/api/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({username:document.querySelector('#username').value})});const d=await r.json();auth=d.access;document.querySelector('#portal').hidden=false};document.querySelector('#open').onclick=async()=>{const r=await fetch('/api/docs?file=welcome.txt',{headers:{Authorization:'Bearer '+auth}});const d=await r.json();document.querySelector('#result').textContent=d.notes}</script></body></html>`,
      },
    ],
  });
  const scenario = await api("/scenarios", {
    name: "登录与文档",
    rules: [
      {
        id: "login",
        method: "POST",
        path: "/api/login",
        conditions: [{ source: "json", key: "username", operator: "exists" }],
        delivery_required: false,
        response: {
          status: 200,
          format: "json",
          content_type: "application/json",
          body: '{"access":"review-session"}',
        },
        fallback: {
          status: 401,
          format: "json",
          content_type: "application/json",
          body: '{"error":"login required"}',
        },
      },
      {
        id: "docs",
        method: "GET",
        path: "/api/docs",
        conditions: [
          {
            source: "query",
            key: "file",
            operator: "equals",
            value: "welcome.txt",
          },
          {
            source: "header",
            key: "authorization",
            operator: "equals",
            value: "Bearer review-session",
          },
        ],
        delivery_required: true,
        response: {
          status: 200,
          format: "json",
          content_type: "application/json",
          body: '{"notes":"{{prompt}}"}',
        },
        fallback: {
          status: 403,
          format: "text",
          content_type: "text/plain",
          body: "Login required",
        },
      },
    ],
  });
  const workspace = await api("/workspaces", {
    name: "完整访问核查",
    slug: "flow-review",
    site_id: site.id,
    callback_path: "/feedback",
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
  const page = await context.newPage();
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") errors.push(message.text());
  });
  await page.goto(admin + "/#agents");
  await page
    .getByRole("heading", { name: "Agent 管理", exact: true })
    .waitFor();
  assert.match(await page.title(), /AgentMirror/);
  await page.getByLabel("搜索内置工具").fill("clone_website");
  await page.getByLabel("clone_website", { exact: true }).uncheck();
  await page.getByRole("button", { name: "保存 Agent", exact: true }).click();
  await page
    .getByText("Agent 配置已保存，新任务使用此配置", { exact: true })
    .waitFor();
  assert.ok(
    !(await api("/agents")).items
      .find((a) => a.id === "writer")
      .tools.includes("clone_website"),
  );
  await page.reload();
  await page.getByLabel("搜索内置工具").fill("clone_website");
  assert.equal(
    await page.getByLabel("clone_website", { exact: true }).isChecked(),
    false,
  );
  await page.getByRole("button", { name: /内容核查 Agent/ }).click();
  await page.getByLabel("搜索内置工具").fill("");
  await page.getByText("run_browser_flow", { exact: true }).waitFor();
  if (output) {
    await mkdir(output, { recursive: true });
    await page.screenshot({ path: path.join(output, "agent-management.png") });
  }
  pass(
    "Agent catalogue displays actual schemas; disabling a writer tool persists without affecting the reviewer",
  );

  await page.goto(admin + "/#composer/" + workspace.id);
  await page.getByRole("button", { name: "触发设置", exact: true }).click();
  await page.getByRole("button", { name: "开始完整核查", exact: true }).click();
  const waitReport = async (after = "") => {
    const until = Date.now() + 45000;
    while (Date.now() < until) {
      const jobs = (await api("/workspace-reviews/" + workspace.id)).items;
      const job = jobs.find((j) => j.id !== after);
      if (job && !["running", "queued"].includes(job.status))
        return api("/generation/jobs/" + job.id);
      await new Promise((resolve) => setTimeout(resolve, 300));
    }
    throw Error("review timeout");
  };
  const first = await waitReport();
  assert.equal(first.status, "completed", JSON.stringify(first));
  assert.equal(first.result.verdict, "passed", JSON.stringify(first.result));
  assert.ok(
    first.result.checks.some(
      (c) => c.name.includes("assert_text") && c.status === "pass",
    ),
  );
  assert.ok(
    first.result.checks.some(
      (c) => c.name === "回传 · 旧令牌失效" && c.status === "pass",
    ),
  );
  assert.equal((await api("/state")).stats.sessions, 0);
  assert.equal((await api("/state")).stats.reports, 0);
  await page
    .locator(".review-chat-report")
    .filter({ hasText: "核查通过" })
    .first()
    .waitFor();
  if (output)
    await page.screenshot({ path: path.join(output, "review-passed.png") });
  pass(
    "Reviewer autonomously calls tools, logs in and opens protected content; all interface and callback assertions pass with no production sessions",
  );

  const graph = first.result.access_flow;
  const browserGroup = graph.groups.find((g) => g.kind === "browser");
  assert.ok(browserGroup);
  const flowNodes = graph.nodes.filter((n) => n.group === browserGroup.id);
  const target = flowNodes.find((n) => n.kind === "delivery");
  assert.ok(target, "browser delivery missing from board");
  const { flowAncestors } = await import(
    "../frontend/src/features/access-flow-layout.js"
  );
  const ancestors = flowAncestors(target.id, graph.edges);
  const prior = flowNodes.filter((n) => ancestors.has(n.id));
  assert.ok(prior.some((n) => n.kind === "step" && n.label.includes("#login")));
  assert.ok(prior.some((n) => n.kind === "step" && n.label.includes("#open")));
  assert.ok(
    prior.some((n) => n.kind === "request" && n.label.includes("/api/docs")),
  );
  assert.ok(prior.some((n) => n.kind === "rule"));
  assert.equal(target.evidence.profile_id, profile.id);
  const savedFlow = await api(
    `/workspace-reviews/${workspace.id}/flow?job=${first.id}`,
  );
  assert.deepEqual(savedFlow.access_flow, graph);
  await page.getByRole("tab", { name: "访问画板", exact: true }).click();
  await page.locator(".flow-node.delivery").first().waitFor();
  assert.equal(
    await page.locator(".react-flow__node").count(),
    graph.nodes.length,
  );
  assert.equal(
    await page.locator(".react-flow__edge").count(),
    graph.edges.length,
  );
  assert.match(
    await page.locator(".flow-caption").textContent(),
    /全部访问流程/,
  );
  await page.getByRole("button", { name: "适应画布", exact: true }).click();
  if (output)
    await page.screenshot({ path: path.join(output, "access-flow-board.png") });
  await page.getByRole("combobox", { name: "选择访问流程" }).click();
  await page.getByRole("option", { name: /浏览器连续访问/ }).click();
  await page.getByRole("button", { name: "节点详情", exact: true }).click();
  await page.locator(".flow-targets button").first().click();
  await page.locator(".flow-detail pre").waitFor();
  assert.match(
    await page.locator(".flow-detail").textContent(),
    /profile_version/,
  );
  assert.ok((await page.locator(".flow-edge.selected").count()) >= 4);
  await page.getByRole("button", { name: "关闭节点详情", exact: true }).click();
  await page.getByRole("button", { name: "全屏画板", exact: true }).click();
  await page.waitForFunction(() => !!document.fullscreenElement);
  await page.getByRole("combobox", { name: "选择访问流程" }).click();
  await page.getByRole("option", { name: /浏览器连续访问/ }).click();
  // Center the endpoint after fullscreen changes the available canvas size.
  await page.getByRole("button", { name: "节点详情", exact: true }).click();
  await page.locator(".flow-targets button").first().click();
  await page.getByRole("button", { name: "关闭节点详情", exact: true }).click();
  await page.waitForTimeout(300);
  const nodeLocator = page.locator(`.react-flow__node[data-id="${target.id}"]`);
  const originalPosition = await nodeLocator.evaluate((n) => n.style.transform);
  const edgeLocator = page
    .locator(
      `.react-flow__edge[data-id*="-${target.id}-"] .react-flow__edge-path`,
    )
    .first();
  const originalEdge = await edgeLocator.getAttribute("d");
  const box = await nodeLocator.boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(
    box.x + box.width / 2 + 100,
    box.y + box.height / 2 + 80,
    { steps: 12 },
  );
  await page.mouse.up();
  const movedPosition = await nodeLocator.evaluate((n) => n.style.transform);
  assert.notEqual(movedPosition, originalPosition, "node did not move on drag");
  assert.notEqual(
    await edgeLocator.getAttribute("d"),
    originalEdge,
    "arrow did not follow dragged node",
  );
  assert.ok(
    await edgeLocator.getAttribute("marker-end"),
    "direction arrow missing",
  );
  const savedKey = `agentmirror.flow-view.v1:${first.id}:${browserGroup.id}`;
  await page.waitForFunction((key) => !!localStorage.getItem(key), savedKey);
  const savedPosition = await page.evaluate(
    ({ key, id }) => JSON.parse(localStorage.getItem(key)).positions[id],
    { key: savedKey, id: target.id },
  );
  await page.waitForTimeout(2800); // Workspace board polls every 2.5 seconds.
  assert.equal(
    await nodeLocator.evaluate((n) => n.style.transform),
    movedPosition,
    "polling reset node position",
  );
  const viewportTransform = () =>
    page.locator(".react-flow__viewport").evaluate((n) => n.style.transform);
  const canvasBox = await page.locator(".flow-viewport").boundingBox();
  const beforePan = await viewportTransform();
  const panX = canvasBox.x + canvasBox.width / 2,
    panY = canvasBox.y + canvasBox.height - 45;
  await page.mouse.move(panX, panY);
  await page.mouse.down();
  await page.mouse.move(panX - 100, panY - 70, { steps: 10 });
  await page.mouse.up();
  assert.notEqual(
    await viewportTransform(),
    beforePan,
    "blank canvas cannot pan",
  );
  const beforeWheel = await viewportTransform();
  await page.mouse.wheel(0, -180);
  await page.waitForTimeout(250);
  assert.notEqual(await viewportTransform(), beforeWheel, "wheel cannot zoom");
  const mini = await page.locator(".react-flow__minimap-svg").boundingBox();
  const beforeMini = await viewportTransform();
  await page.mouse.click(
    mini.x + mini.width * 0.25,
    mini.y + mini.height * 0.5,
  );
  await page.waitForTimeout(200);
  assert.notEqual(
    await viewportTransform(),
    beforeMini,
    "minimap cannot navigate",
  );
  await page.getByRole("button", { name: "适应画布", exact: true }).click();
  await page.waitForTimeout(250);
  if (output)
    await page.screenshot({
      path: path.join(output, "access-flow-fullscreen.png"),
    });
  await page.getByRole("button", { name: "退出全屏画板", exact: true }).click();
  await page.waitForFunction(() => !document.fullscreenElement);
  pass(
    "Node dragging updates arrows; canvas pan, wheel zoom, minimap navigation, fullscreen and polling preserve interaction state",
  );
  const z = await page
    .locator(".flow-zoom span")
    .filter({ hasText: "%" })
    .textContent();
  await page.getByRole("button", { name: "放大画板", exact: true }).click();
  await page.waitForFunction(
    (previous) =>
      document.querySelector(".flow-zoom > span").textContent !== previous,
    z,
  );
  assert.notEqual(
    await page
      .locator(".flow-zoom span")
      .filter({ hasText: "%" })
      .textContent(),
    z,
  );
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "导出", exact: true }).click();
  assert.equal((await download).suggestedFilename(), "access-flow.json");
  await page.getByRole("combobox", { name: "选择访问流程" }).click();
  await page.getByRole("option", { name: /接口逐项检查/ }).click();
  assert.match(
    await page.locator(".flow-caption").textContent(),
    /不证明已走通登录/,
  );
  await page.getByRole("combobox", { name: "选择访问流程" }).click();
  await page.getByRole("option", { name: /浏览器连续访问/ }).click();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator(".flow-viewport").scrollIntoViewIfNeeded();
  const mobileCanvas = await page.locator(".flow-viewport").boundingBox();
  const touch = await context.newCDPSession(page);
  await touch.send("Emulation.setTouchEmulationEnabled", {
    enabled: true,
    maxTouchPoints: 2,
  });
  const mx = mobileCanvas.x + mobileCanvas.width / 2,
    my = mobileCanvas.y + mobileCanvas.height - 145;
  const beforeTouch = await viewportTransform();
  await touch.send("Input.dispatchTouchEvent", {
    type: "touchStart",
    touchPoints: [{ x: mx, y: my }],
  });
  for (let i = 1; i <= 6; i++)
    await touch.send("Input.dispatchTouchEvent", {
      type: "touchMove",
      touchPoints: [{ x: mx + i * 7, y: my - i * 5 }],
    });
  await touch.send("Input.dispatchTouchEvent", {
    type: "touchEnd",
    touchPoints: [],
  });
  assert.notEqual(
    await viewportTransform(),
    beforeTouch,
    "mobile touch cannot pan",
  );
  const beforePinch = await viewportTransform();
  await touch.send("Input.dispatchTouchEvent", {
    type: "touchStart",
    touchPoints: [
      { x: mx - 30, y: my, id: 0 },
      { x: mx + 30, y: my, id: 1 },
    ],
  });
  for (let i = 1; i <= 6; i++)
    await touch.send("Input.dispatchTouchEvent", {
      type: "touchMove",
      touchPoints: [
        { x: mx - 30 - i * 5, y: my, id: 0 },
        { x: mx + 30 + i * 5, y: my, id: 1 },
      ],
    });
  await touch.send("Input.dispatchTouchEvent", {
    type: "touchEnd",
    touchPoints: [],
  });
  assert.notEqual(
    await viewportTransform(),
    beforePinch,
    "mobile pinch cannot zoom",
  );
  await touch.send("Emulation.setTouchEmulationEnabled", { enabled: false });
  await touch.detach();
  if (output)
    await page.screenshot({
      path: path.join(output, "access-flow-mobile-canvas.png"),
    });
  pass("Mobile touch panning and two-finger pinch zoom work");
  await page.getByRole("button", { name: "列表", exact: true }).click();
  await page.locator(".flow-list button").last().click();
  assert.ok(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
    "board mobile overflow",
  );
  if (output)
    await page.screenshot({
      path: path.join(output, "access-flow-mobile.png"),
    });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("button", { name: "画板", exact: true }).click();
  await page.evaluate(() => (document.documentElement.dataset.theme = "dark"));
  if (output)
    await page.screenshot({ path: path.join(output, "access-flow-dark.png") });
  await page.evaluate(() => delete document.documentElement.dataset.theme);
  await page.reload();
  await page.getByRole("tab", { name: "访问画板", exact: true }).click();
  await page.locator(".flow-node.delivery").first().waitFor();
  assert.equal(
    await page.locator(".react-flow__node").count(),
    graph.nodes.length,
  );
  await page.getByRole("combobox", { name: "选择访问流程" }).click();
  await page.getByRole("option", { name: /浏览器连续访问/ }).click();
  assert.equal(
    await nodeLocator.evaluate((n) => n.style.transform),
    movedPosition,
    "reload lost saved node position",
  );
  assert.deepEqual(
    await page.evaluate(
      ({ key, id }) => JSON.parse(localStorage.getItem(key)).positions[id],
      { key: savedKey, id: target.id },
    ),
    savedPosition,
  );
  await page.getByRole("button", { name: "重新排列节点", exact: true }).click();
  await page.waitForFunction(
    ({ id, old }) =>
      document.querySelector(`.react-flow__node[data-id="${id}"]`).style
        .transform === old,
    { id: target.id, old: originalPosition },
  );
  await page
    .getByRole("button", { name: "在 AI 助手中查看", exact: true })
    .click();
  await page.locator(".review-chat-report").first().waitFor();
  pass(
    "Access board persists real Agent browser/login/request/rule/delivery ancestry; node evidence, independent probes, zoom, export, mobile list and chat navigation work",
  );

  const browserCall = page
    .locator(".agent-tool-call")
    .filter({ hasText: "run_browser_flow" });
  await browserCall.locator("summary").click();
  await browserCall.locator("pre").nth(1).waitFor();
  assert.ok(
    (await browserCall.locator("pre").count()) >= 2,
    "tool parameters and result missing",
  );
  await page.getByRole("button", { name: "触发设置", exact: true }).click();
  await page.locator("summary").filter({ hasText: "登录与访问步骤" }).click();
  await page.getByRole("button", { name: "添加步骤", exact: true }).click();
  await page.getByRole("button", { name: "添加步骤", exact: true }).click();
  await page.getByRole("combobox", { name: "步骤 2 操作" }).click();
  await page.getByRole("option", { name: "验证可见文字", exact: true }).click();
  await page.getByLabel("步骤 2 选择器").fill("h1");
  await page.getByLabel("步骤 2 值").fill("文档入口");
  await page.getByText("工作区或绑定素材保存后", { exact: true }).click();
  await page.getByRole("button", { name: "保存触发设置", exact: true }).click();
  await page.getByText("核查触发设置已保存", { exact: true }).waitFor();
  const savedPolicy = await api(
    "/workspace-reviews/" + workspace.id + "/policy",
  );
  assert.equal(savedPolicy.browser_steps.length, 2);
  assert.equal(savedPolicy.browser_steps[1].action, "assert_text");
  pass(
    "Configured browser steps persist; tool trace expands into actual parameters and output",
  );
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "关闭", exact: true })
    .click();
  site.files[0].content = site.files[0].content.replace(
    "document.querySelector('#portal').hidden=false",
    "document.querySelector('#portal').hidden=true",
  );
  site = await api("/sites", site);
  const broken = await waitReport(first.id);
  assert.equal(broken.trigger, "after_save");
  assert.equal(broken.result.verdict, "issues");
  const brokenGraph = broken.result.access_flow;
  const browserGroups = new Set(
    brokenGraph.groups.filter((g) => g.kind === "browser").map((g) => g.id),
  );
  const brokenNodes = brokenGraph.nodes.filter((n) =>
    browserGroups.has(n.group),
  );
  assert.ok(brokenNodes.some((n) => n.kind === "step" && n.status === "fail"));
  assert.ok(
    brokenNodes.some((n) => n.kind === "step" && n.status === "incomplete"),
  );
  assert.ok(
    !brokenNodes.some((n) => n.kind === "delivery"),
    "blocked login falsely reached injection point",
  );
  const priorBoard = await api(
    `/workspace-reviews/${workspace.id}/flow?job=${first.id}`,
  );
  assert.equal(priorBoard.stale, true);
  assert.deepEqual(
    priorBoard.access_flow,
    graph,
    "new material replaced historic board",
  );
  assert.ok(
    broken.result.checks.some(
      (c) => c.name.includes("assert_visible") && c.status === "fail",
    ),
  );
  assert.ok(
    broken.result.checks.some(
      (c) => c.name.includes("页面可达性") && c.status === "incomplete",
    ),
  );
  await page
    .locator(`[data-conversation-id="${broken.conversation_id}"]`)
    .click();
  await page.locator(".review-chat-report").first().locator("summary").click();
  await page
    .getByRole("button", { name: "查看访问流程画板", exact: true })
    .click();
  await page.getByRole("dialog").locator(".flow-node.fail").first().waitFor();
  if (output)
    await page.screenshot({
      path: path.join(output, "access-flow-failed.png"),
    });
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "关闭", exact: true })
    .click();
  await page.getByRole("combobox", { name: "筛选聊天核查结果" }).click();
  await page.getByRole("option", { name: "只看异常", exact: true }).click();
  await page.locator(".review-check.fail").first().click();
  if (output)
    await page.screenshot({
      path: path.join(output, "review-broken-flow.png"),
    });
  pass(
    "Saving a broken login interaction automatically starts another review; failed prerequisite and unvisited protected endpoint are consolidated, not marked successful",
  );
  await page.getByRole("tab", { name: "AI 助手", exact: true }).click();
  await page
    .locator(`[data-conversation-id="${broken.conversation_id}"]`)
    .click();
  await page
    .getByRole("heading", { name: "内容核查 Agent", exact: true })
    .waitFor();
  const report = page.locator(".review-chat-report").first();
  await report.locator(".review-check").first().waitFor();
  assert.equal(
    await page
      .getByRole("button", { name: "采用到当前草稿", exact: true })
      .count(),
    0,
  );
  if (output)
    await page.screenshot({
      path: path.join(output, "review-chat-report.png"),
    });
  await page.getByRole("button", { name: "新建对话", exact: true }).click();
  // Role persists for a new conversation. Review consultation must not eagerly run the suite.
  await page.getByRole("combobox", { name: "对话 Agent", exact: true }).click();
  await page
    .getByRole("option", { name: "内容核查 Agent", exact: true })
    .click();
  await page
    .getByPlaceholder("描述要核查的问题，或继续追问上轮报告…")
    .fill("CHAT_CAPABILITIES 你可以做什么？");
  await page.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await page
    .getByText("核查能力答复：可以检查登录、登录后访问和接口响应。", {
      exact: true,
    })
    .first()
    .waitFor();
  const consultation = (
    await api("/workspace-reviews/" + workspace.id)
  ).items.find((j) => j.prompt?.includes("CHAT_CAPABILITIES"));
  assert.ok(consultation);
  const consultationDetail = await api("/generation/jobs/" + consultation.id);
  assert.equal(consultationDetail.result.kind, "message");
  assert.ok(!consultationDetail.events.some((e) => e.stage === "tool_call"));
  await page
    .getByRole("combobox", { name: "本轮使用的模型", exact: true })
    .click();
  await page
    .getByRole("option", {
      name: "Alternate model · review-fixture-alt",
      exact: true,
    })
    .click();
  await page
    .getByPlaceholder("描述要核查的问题，或继续追问上轮报告…")
    .fill("CHAT_FOLLOWUP 登录后如何核查？");
  await page.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await page
    .getByText("核查能力答复：可以检查登录、登录后访问和接口响应。", {
      exact: true,
    })
    .nth(1)
    .waitFor();
  await page.reload();
  await page
    .locator(`[data-conversation-id="${consultation.conversation_id}"]`)
    .click();
  await page
    .getByText("核查能力答复：可以检查登录、登录后访问和接口响应。", {
      exact: true,
    })
    .nth(1)
    .waitFor();
  await page
    .getByRole("combobox", { name: "按 Agent 筛选对话", exact: true })
    .click();
  await page
    .getByRole("option", { name: "内容核查 Agent", exact: true })
    .click();
  assert.ok((await page.locator(".agent-conversation").count()) >= 2);
  assert.ok(
    requestedModels.includes("review-fixture-alt"),
    "model selection not sent to provider",
  );
  assert.match(
    await page
      .getByRole("combobox", { name: "本轮使用的模型", exact: true })
      .textContent(),
    /review-fixture-alt/,
  );
  const group = page
    .locator(".agent-group-heading")
    .filter({ hasText: "内容核查 Agent" });
  await group.click();
  assert.equal(await page.locator(".agent-conversation").count(), 0);
  await group.click();
  assert.ok((await page.locator(".agent-conversation").count()) >= 2);
  if (output)
    await page.screenshot({
      path: path.join(output, "review-chat-followup.png"),
    });
  pass(
    "Manual and automatic review reports share AI Assistant conversations; reviewer consultation and follow-up retain history without automatically running tools",
  );
  await page.getByRole("button", { name: "触发设置", exact: true }).click();
  await page
    .locator("summary")
    .filter({ hasText: "更多触发条件与触发消息" })
    .click();
  await page.getByText("指定编写工具调用成功后", { exact: true }).click();
  await page.getByLabel("触发工具（可多选）").selectOption(["save_material"]);
  await page
    .getByLabel("工具调用后发送的消息")
    .fill("请复查已保存素材的访问链路");
  await page.getByRole("button", { name: "保存触发设置", exact: true }).click();
  await page.getByText("核查触发设置已保存", { exact: true }).waitFor();
  const advanced = await api("/workspace-reviews/" + workspace.id + "/policy");
  assert.deepEqual(advanced.tool_names, ["save_material"]);
  assert.equal(advanced.messages.on_tool, "请复查已保存素材的访问链路");
  pass(
    "Advanced tool-call trigger and custom messages persist through the shared Assistant trigger-settings entry",
  );
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "关闭", exact: true })
    .click();
  await page.setViewportSize({ width: 390, height: 844 });
  assert.ok(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
    "mobile overflow",
  );
  if (output)
    await page.screenshot({ path: path.join(output, "review-mobile.png") });
  // A browser-only fixture covers dense branching and the documented node cap.
  // It never writes synthetic evidence into a report or the application database.
  const denseGraph = (count) => ({
    version: 1,
    groups: [{ id: "dense-qa", kind: "browser", label: "画板布局验收" }],
    nodes: Array.from({ length: count }, (_, i) => ({
      id: `dense-${i}`,
      group: "dense-qa",
      kind: ["step", "request", "rule", "delivery", "action", "callback"][
        i % 6
      ],
      label: `${i + 1}. ${["打开业务入口", "GET /api/document", "命中文档响应规则", "返回已绑定提示词", "受控模拟动作", "接收回传结果"][i % 6]}`,
      status: "pass",
      evidence: { status: 200, profile_version: 2 },
    })),
    edges: Array.from({ length: count - 1 }, (_, i) => ({
      source: `dense-${i < 6 ? 0 : i - 5}`,
      target: `dense-${i + 1}`,
      relation: [
        "next",
        "request",
        "matched",
        "delivered",
        "controlled_replay",
        "received",
      ][i % 6],
    })),
  });
  // Reproduce a running graph gaining a small browser group at the end.
  // These fixtures only intercept board reads; they never overwrite saved evidence.
  const primary = denseGraph(44);
  primary.groups[0] = { id: "dense-qa", kind: "redteam", label: "连续访问" };
  const small = denseGraph(10);
  small.groups[0] = {
    id: "late-browser",
    kind: "redteam_browser",
    label: "最后一次浏览器访问",
  };
  small.nodes = small.nodes.map((n) => ({
    ...n,
    id: "late-" + n.id,
    group: "late-browser",
  }));
  small.edges = small.edges.map((e) => ({
    ...e,
    source: "late-" + e.source,
    target: "late-" + e.target,
  }));
  const completeGraph = {
    ...primary,
    groups: [...primary.groups, ...small.groups],
    nodes: [...primary.nodes, ...small.nodes],
    edges: [...primary.edges, ...small.edges],
  };
  const otherJob = { ...first, id: first.id + "next", status: "completed" };
  let boardRecord = { ...savedFlow, status: "running", access_flow: primary };
  let boardJobs = [{ ...first, status: "running" }];
  const listURL = admin + "/api/workspace-reviews/" + workspace.id;
  const flowPattern = "**/api/workspace-reviews/*/flow?*";
  await page.route(listURL, (route) =>
    route.fulfill({ json: { items: boardJobs } }),
  );
  await page.route(flowPattern, (route) =>
    route.fulfill({
      json:
        new URL(route.request().url()).searchParams.get("job") === otherJob.id
          ? {
              ...savedFlow,
              id: otherJob.id,
              status: "completed",
              access_flow: small,
            }
          : boardRecord,
    }),
  );
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("tab", { name: "访问画板", exact: true }).click();
  const renderedCount = async (count) =>
    page.waitForFunction(
      (n) => document.querySelectorAll(".react-flow__node").length === n,
      count,
    );
  await renderedCount(44);
  boardRecord = { ...boardRecord, access_flow: completeGraph };
  await renderedCount(54);
  assert.equal(await page.locator(".react-flow__edge").count(), 52);
  assert.match(await page.locator(".flow-caption").textContent(), /54 \/ 54/);
  await page.getByRole("combobox", { name: "选择访问流程" }).click();
  await page.locator('[role="option"][data-select-value="dense-qa"]').click();
  await renderedCount(44);
  // A further browser step must not take over the explicitly selected flow.
  boardRecord = {
    ...boardRecord,
    access_flow: {
      ...completeGraph,
      groups: [
        ...completeGraph.groups,
        { id: "late-2", kind: "browser", label: "后续浏览器步骤" },
      ],
      nodes: [
        ...completeGraph.nodes,
        { ...small.nodes[0], id: "last-step", group: "late-2" },
      ],
    },
  };
  await page.waitForFunction(() =>
    document.querySelector(".flow-caption").textContent.includes("44 / 55"),
  );
  await renderedCount(44);
  await page.getByRole("combobox", { name: "选择访问流程" }).click();
  await page.locator('[role="option"][data-select-value=""]').click();
  await renderedCount(55);
  boardRecord = { ...boardRecord, status: "completed" };
  boardJobs = [{ ...first, status: "completed" }];
  await page.waitForFunction(() =>
    document
      .querySelector('[aria-label="选择画板核查记录"]')
      .textContent.includes("已结束"),
  );
  const expectedIDs = boardRecord.access_flow.nodes.map((n) => n.id).sort();
  const visibleIDs = async () =>
    (
      await page
        .locator(".react-flow__node")
        .evaluateAll((items) => items.map((n) => n.dataset.id))
    ).sort();
  assert.deepEqual(await visibleIDs(), expectedIDs);
  await page.getByRole("button", { name: "全屏画板", exact: true }).click();
  await page.waitForFunction(() => !!document.fullscreenElement);
  await page.getByRole("button", { name: "适应画布", exact: true }).click();
  await page.waitForTimeout(250);
  if (output)
    await page.screenshot({
      path: path.join(output, "access-flow-completed-all.png"),
    });
  await page.getByRole("button", { name: "退出全屏画板", exact: true }).click();
  await page.waitForFunction(() => !document.fullscreenElement);
  await page.reload();
  await page.getByRole("tab", { name: "访问画板", exact: true }).click();
  await renderedCount(55);
  assert.deepEqual(
    await visibleIDs(),
    expectedIDs,
    "refresh lost completed nodes",
  );
  const listPoll = page.waitForResponse((res) => res.url() === listURL);
  boardJobs = [otherJob, ...boardJobs];
  await listPoll;
  await page.getByRole("combobox", { name: "选择画板核查记录" }).click();
  await page
    .locator(`[role="option"][data-select-value="${otherJob.id}"]`)
    .waitFor();
  assert.equal(
    await page
      .locator('[aria-label="选择画板核查记录"]')
      .getAttribute("data-value"),
    first.id,
  );
  assert.deepEqual(
    await visibleIDs(),
    expectedIDs,
    "a newer job replaced the current board",
  );
  await page
    .locator(`[role="option"][data-select-value="${otherJob.id}"]`)
    .click();
  await renderedCount(10);
  await page.getByRole("combobox", { name: "选择画板核查记录" }).click();
  await page
    .locator(`[role="option"][data-select-value="${first.id}"]`)
    .click();
  await renderedCount(55);
  assert.deepEqual(await visibleIDs(), expectedIDs);
  pass(
    "Live 44 → 54 → 55 nodes persist through completion and reload; manual group filtering stays fixed and newer jobs cannot replace the viewed record",
  );
  assert.ok((await page.title()).includes("AgentMirror"));
  assert.ok(page.url().includes("#composer/" + workspace.id));
  assert.equal(await page.locator("vite-error-overlay").count(), 0);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "列表", exact: true }).click();
  assert.equal(await page.locator(".flow-list li").count(), 55);
  await page
    .getByRole("combobox", { name: "选择访问流程" })
    .scrollIntoViewIfNeeded();
  assert.ok(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  );
  if (output)
    await page.screenshot({
      path: path.join(output, "access-flow-overview-mobile.png"),
    });
  await page.unroute(listURL);
  await page.unroute(flowPattern);
  await page.reload();
  let dense = denseGraph(48);
  await page.route("**/api/workspace-reviews/*/flow?*", (route) =>
    route.fulfill({ json: { ...savedFlow, access_flow: dense } }),
  );
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("tab", { name: "访问画板", exact: true }).click();
  await page.waitForFunction(
    () => document.querySelectorAll(".react-flow__node").length === 48,
  );
  await page.getByRole("button", { name: "全屏画板", exact: true }).click();
  await page.getByRole("button", { name: "适应画布", exact: true }).click();
  await page.waitForTimeout(300);
  if (output)
    await page.screenshot({ path: path.join(output, "access-flow-dense.png") });
  dense = denseGraph(600);
  await page.waitForFunction(
    () => document.querySelectorAll(".react-flow__node").length === 600,
  );
  assert.equal(await page.locator(".react-flow__edge").count(), 599);
  await page.getByRole("button", { name: "适应画布", exact: true }).click();
  await page.waitForTimeout(250);
  const beforeDenseZoom = await viewportTransform();
  await page.getByRole("button", { name: "放大画板", exact: true }).click();
  await page.waitForTimeout(200);
  assert.notEqual(
    await viewportTransform(),
    beforeDenseZoom,
    "600-node board does not respond",
  );
  pass(
    "Dense branching renders with labels and arrows; live polling to 600 nodes keeps the canvas responsive",
  );
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ checks, errors }, null, 2));
} finally {
  if (browser) await browser.close();
  if (model) await new Promise((resolve) => model.close(resolve));
  const stopped = once(server, "exit");
  server.kill("SIGTERM");
  await stopped;
  await rm(temp, { recursive: true, force: true });
}

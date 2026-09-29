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
const temp = await mkdtemp(
  path.join(os.tmpdir(), "agentmirror-model-options-"),
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
    let body = "";
    for await (const chunk of req) body += chunk;
    const input = JSON.parse(body);
    res.setHeader("Content-Type", "application/json");
    if (
      input.messages.some((message) => message.content === "Reply with OK.")
    ) {
      res.end(JSON.stringify({ choices: [{ message: { content: "OK" } }] }));
      return;
    }
    assert.equal(input.max_tokens, 16000);
    requests++;
    if (requests === 1) {
      res.end(
        JSON.stringify({
          choices: [
            {
              message: {
                content: null,
                reasoning_content:
                  "先**读取文件列表**。\n\n- 根据当前素材回答\n\n<script>window.reasoningInjected=true</script>",
                tool_calls: [
                  {
                    id: "readfiles",
                    type: "function",
                    function: { name: "list_files", arguments: "{}" },
                  },
                ],
              },
              finish_reason: "tool_calls",
            },
          ],
        }),
      );
    } else {
      assert(
        input.messages.some((message) =>
          message.reasoning_content?.includes("读取文件列表"),
        ),
        "thinking was not replayed to provider",
      );
      await new Promise((resolve) => setTimeout(resolve, 300));
      res.end(
        JSON.stringify({
          choices: [
            {
              message: {
                content: "检查完成，可以继续配置模拟场景。",
                reasoning_content: "已读取列表，**检查完成**。",
              },
              finish_reason: "stop",
            },
          ],
        }),
      );
    }
  });
  modelServer.listen(0, "127.0.0.1");
  await once(modelServer, "listening");
  const provider = await api("/generation/providers", {
    name: "思考与超时验收",
    protocol: "openai",
    base_url: `http://127.0.0.1:${modelServer.address().port}`,
    model: "synthetic-thinking",
    set_default: true,
  });
  const scenario = (name) =>
    api("/scenarios", {
      name,
      rules: [
        {
          id: "info",
          method: "GET",
          path: "/info",
          conditions: [],
          response: {
            status: 200,
            format: "json",
            content_type: "application/json",
            body: '{"ok":true}',
          },
          delivery_required: false,
        },
      ],
    });
  const bound = await scenario("正在使用的场景");
  const unused = await scenario("待删除场景");
  const draftOnly = await scenario("仅草稿场景");
  const workspace = await api("/workspaces", {
    name: "场景与思考验收",
    slug: "thinking-check",
    bindings: [
      {
        id: "fixture",
        scenario_id: bound.id,
        enabled: true,
        paths: {},
        profile_id: "",
      },
    ],
  });
  const page = await context.newPage();
  page.setDefaultTimeout(15000);
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (entry) => {
    if (entry.type() === "error") errors.push(entry.text());
  });
  await page.goto(admin + "/#composer/" + workspace.id);
  await page
    .getByRole("heading", { name: workspace.name, exact: true })
    .waitFor();
  await page.getByRole("button", { name: "模型设置", exact: true }).click();
  const dialog = page.getByRole("dialog");
  const requestTimeout = dialog.getByLabel("单次模型请求超时（秒）", {
    exact: true,
  });
  const jobTimeout = dialog.getByLabel("整个任务超时（秒）", { exact: true });
  await requestTimeout.waitFor();
  assert.equal(await requestTimeout.inputValue(), "180");
  assert.equal(await jobTimeout.inputValue(), "600");
  await dialog.getByLabel("上下文窗口（token）", { exact: true }).fill("64000");
  await dialog
    .getByLabel("单次输出上限（token）", { exact: true })
    .fill("16000");
  await requestTimeout.fill("300");
  await jobTimeout.fill("1200");
  await dialog
    .getByRole("button", { name: "保存并测试连接", exact: true })
    .click();
  await page
    .getByRole("status")
    .filter({ hasText: "模型连接测试成功" })
    .waitFor();
  await dialog
    .getByRole("button", { name: "关闭", exact: true })
    .last()
    .click();
  await page.getByRole("button", { name: "模型设置", exact: true }).click();
  await page.waitForFunction(
    () => document.querySelector('input[type="number"]')?.value === "300",
  );
  assert.equal(await jobTimeout.inputValue(), "1200");
  assert.equal(
    await dialog
      .getByLabel("上下文窗口（token）", { exact: true })
      .inputValue(),
    "64000",
  );
  assert.equal(
    await dialog
      .getByLabel("单次输出上限（token）", { exact: true })
      .inputValue(),
    "16000",
  );
  const out = process.env.QA_OUTPUT_DIR;
  if (out) {
    await mkdir(out, { recursive: true });
    await page.screenshot({
      path: path.join(out, "model-timeouts-desktop.png"),
    });
  }
  await dialog
    .getByRole("button", { name: "关闭", exact: true })
    .last()
    .click();
  const savedProvider = (await api("/generation/providers")).items.find(
    (item) => item.id === provider.id,
  );
  assert.equal(savedProvider.request_timeout_seconds, 300);
  assert.equal(savedProvider.job_timeout_seconds, 1200);
  pass(
    "timeout controls persist per provider, survive modal reopen, and support connection testing",
  );

  await page
    .locator(".agent-mention-input textarea")
    .fill("请读取文件列表并说明");
  await page.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await page
    .getByText("检查完成，可以继续配置模拟场景。", { exact: true })
    .waitFor();
  assert.equal(requests, 2);
  assert.equal(await page.locator(".agent-reasoning").count(), 2);
  const order = await page
    .locator(".agent-tools > details > summary")
    .allTextContents();
  assert.match(order[0], /思考/);
  assert.match(order[1], /list_files/);
  assert.match(order[2], /思考/);
  await page.locator(".agent-reasoning summary").first().click();
  await page
    .locator(".agent-reasoning .chat-markdown strong")
    .getByText("读取文件列表", { exact: true })
    .waitFor();
  assert.equal(await page.evaluate(() => window.reasoningInjected), undefined);
  const jobs = await api("/generation/jobs");
  const job = await api("/generation/jobs/" + jobs.items[0].id);
  assert.equal(job.provider.request_timeout_seconds, 300);
  assert(
    job.events
      .filter((e) => e.stage === "model")
      .every((e) => e.request_timeout_seconds === 300),
  );
  // Reload and open persisted transcript: the compact history lazily fetches payloads.
  await page.reload();
  await page.locator(".agent-reasoning summary").first().waitFor();
  await page.locator(".agent-reasoning summary").first().click();
  await page
    .locator(".agent-reasoning .chat-markdown strong")
    .getByText("读取文件列表", { exact: true })
    .waitFor();
  await page.locator(".agent-tool-call:not(.agent-reasoning) summary").click();
  await page.getByLabel("list_files 输出", { exact: true }).waitFor();
  if (out)
    await page.screenshot({ path: path.join(out, "thinking-desktop.png") });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator(".agent-reasoning").first().scrollIntoViewIfNeeded();
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  if (out)
    await page.screenshot({ path: path.join(out, "thinking-mobile.png") });
  pass(
    "reasoning is interleaved with tool calls, Markdown renders safely, refreshed history and tool payloads load on expansion",
  );
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("tab", { name: "模拟场景", exact: true }).click();
  await page
    .getByRole("button", { name: "删除场景 待删除场景", exact: true })
    .click();
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
  assert.equal((await api("/scenarios/" + unused.id)).id, unused.id);
  await page
    .getByRole("button", { name: "删除场景 待删除场景", exact: true })
    .click();
  await dialog.getByRole("button", { name: "确认删除", exact: true }).click();
  await dialog.waitFor({ state: "hidden" });
  assert.equal(
    (await context.request.get(admin + "/api/scenarios/" + unused.id)).status(),
    404,
  );
  await page
    .getByRole("button", { name: "删除场景 正在使用的场景", exact: true })
    .click();
  await dialog.getByRole("alert").filter({ hasText: workspace.name }).waitFor();
  assert(
    await dialog
      .getByRole("button", { name: "确认删除", exact: true })
      .isDisabled(),
  );
  if (out)
    await page.screenshot({
      path: path.join(out, "scenario-delete-references.png"),
    });
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
  await page.getByRole("tab", { name: "绑定提示词", exact: true }).click();
  await page.getByRole("button", { name: "移除实例", exact: true }).click();
  await page.getByRole("button", { name: "保存工作区", exact: true }).click();
  await page
    .getByRole("status")
    .filter({ hasText: "工作区草稿已保存" })
    .waitFor();
  await page.getByRole("tab", { name: "模拟场景", exact: true }).click();
  await page
    .getByRole("button", { name: "删除场景 正在使用的场景", exact: true })
    .click();
  await dialog.getByRole("button", { name: "确认删除", exact: true }).click();
  await dialog.waitFor({ state: "hidden" });
  assert.equal(
    (await context.request.get(admin + "/api/scenarios/" + bound.id)).status(),
    404,
  );
  const card = page
    .locator(".material-scenario-row")
    .filter({ hasText: "仅草稿场景" });
  await card.getByRole("button", { name: "添加到工作区", exact: true }).click();
  await page.getByRole("tab", { name: "模拟场景", exact: true }).click();
  await page
    .getByRole("button", { name: "删除场景 仅草稿场景", exact: true })
    .click();
  await dialog
    .getByText("当前未保存草稿中的对应场景实例也会移除。", { exact: true })
    .waitFor();
  await page.setViewportSize({ width: 390, height: 844 });
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  if (out)
    await page.screenshot({
      path: path.join(out, "scenario-delete-mobile.png"),
    });
  await dialog.getByRole("button", { name: "确认删除", exact: true }).click();
  await dialog.waitFor({ state: "hidden" });
  await page.getByRole("tab", { name: "绑定提示词", exact: true }).click();
  assert.equal(
    await page.getByRole("button", { name: "移除实例", exact: true }).count(),
    0,
  );
  assert.deepEqual((await api("/workspaces/" + workspace.id)).bindings, []);
  assert.deepEqual(errors, []);
  pass(
    "scenario deletion supports cancel, reference guard, delete after unlink, and cleanup of unsaved draft instances on mobile",
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

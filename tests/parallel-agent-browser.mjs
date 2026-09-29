import { clearBuiltinTestMaterials } from "./authoring-fixture.mjs";
import assert from "node:assert/strict";
import http from "node:http";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { once } from "node:events";
import { spawn } from "node:child_process";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { chromium } from "playwright";

// Browser plugin is not available; use the repository Playwright workflow.
// All requests use a temporary database and gated, synthetic local providers.
const root = path.resolve(import.meta.dirname, "..");
const temp = await mkdtemp(path.join(os.tmpdir(), "agentmirror-parallel-"));
const deferred = () => {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
};
const gates = new Map(["A", "B", "C", "D", "E"].map((id) => [id, deferred()]));
const requests = new Map();
let recoveryCalls = 0;
const errors = [];
const model = http.createServer(async (request, response) => {
  try {
    let raw = "";
    for await (const chunk of request) raw += chunk;
    const body = JSON.parse(raw);
    const contents = body.messages
      .filter((m) => m.role === "user")
      .flatMap((m) =>
        typeof m.content === "string"
          ? [m.content]
          : m.content.filter((b) => b.type === "text").map((b) => b.text),
      );
    const context = JSON.parse(contents.findLast((c) => c.startsWith("{")));
    const label = context.request;
    requests.set(label, body);
    if (gates.has(label)) await gates.get(label).promise;
    response.setHeader("Content-Type", "application/json");
    if (label === "恢复测试") {
      recoveryCalls++;
      const call = (id, name, args) => ({
        id,
        type: "function",
        function: { name, arguments: JSON.stringify(args) },
      });
      const message =
        recoveryCalls === 1
          ? {
              role: "assistant",
              content: null,
              tool_calls: [
                call("overview", "read_module", { module: "overview" }),
                call("site", "read_module", { module: "site" }),
                call("scenario", "read_module", { module: "scenario" }),
                call("files", "list_files", {}),
              ],
            }
          : recoveryCalls === 2
            ? {
                role: "assistant",
                content: null,
                reasoning_content: "PRIVATE_RECOVERY_DATA",
              }
            : {
                role: "assistant",
                content: "空回复已恢复，之前的四次工具无需重新执行。",
              };
      response.end(
        JSON.stringify({
          choices: [
            {
              message,
              finish_reason: recoveryCalls === 1 ? "tool_calls" : "stop",
            },
          ],
          usage: { completion_tokens: 12 },
        }),
      );
      return;
    }
    if (label === "持续空回复") {
      response.end(
        JSON.stringify({
          choices: [{ message: { content: null }, finish_reason: "stop" }],
        }),
      );
      return;
    }
    if (label === "F 生成草稿") {
      const call = (name, args) => ({
        id: name,
        type: "function",
        function: { name, arguments: JSON.stringify(args) },
      });
      response.end(
        JSON.stringify({
          choices: [
            {
              message: {
                role: "assistant",
                tool_calls: [
                  call("set_site", {
                    name: "F 的独立站点",
                    entry: "index.html",
                    spa: false,
                  }),
                  call("write_file", {
                    path: "index.html",
                    content: "<!doctype html><html><h1>F 初始页面</h1></html>",
                  }),
                  call("finish_draft", { summary: "完成对话 F" }),
                ],
              },
            },
          ],
        }),
      );
      return;
    }
    response.end(
      JSON.stringify(
        request.url === "/v1/messages"
          ? {
              id: `reply-${label}`,
              type: "message",
              role: "assistant",
              content: [{ type: "text", text: `完成对话 ${label}` }],
              stop_reason: "end_turn",
            }
          : {
              choices: [
                {
                  message: { role: "assistant", content: `完成对话 ${label}` },
                },
              ],
            },
      ),
    );
  } catch (e) {
    errors.push(e.message);
    response.writeHead(500).end();
  }
});
model.listen(0, "127.0.0.1");
await once(model, "listening");
const listener = net.createServer();
listener.listen(0, "127.0.0.1");
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
    path.join(temp, "parallel.sqlite3"),
  ],
  { cwd: root, stdio: ["ignore", "pipe", "pipe"] },
);
let browser, page;
const releases = [];
try {
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(Error("startup timeout")), 10000);
    server.once("error", reject);
    server.once("exit", (code) => {
      clearTimeout(timer);
      reject(Error(`server exited ${code}`));
    });
    server.stdout.on("data", (data) => {
      if (data.toString().includes("公告地址")) {
        clearTimeout(timer);
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
      username: "paralleltest",
      password: "SYNTHETIC-parallel-2026",
      confirm_password: "SYNTHETIC-parallel-2026",
    },
  });
  assert.equal(setup.status(), 200);
  await clearBuiltinTestMaterials(context.request, admin);
  const state = await (await context.request.get(admin + "/api/state")).json();
  const api = async (route, data, expected = 200) => {
    const result = await context.request.fetch(admin + "/api" + route, {
      method: data ? "POST" : "GET",
      headers: { "X-Admin-Token": state.csrf },
      ...(data ? { data } : {}),
    });
    assert.equal(result.status(), expected, await result.text());
    return result.json();
  };
  const providers = [];
  for (const protocol of ["openai", "anthropic"])
    providers.push(
      await api("/generation/providers", {
        name: `${protocol} 并行测试`,
        protocol,
        base_url: `http://127.0.0.1:${model.address().port}`,
        model: `${protocol}-test`,
      }),
    );
  const workspace = await api("/workspaces", {
    name: "多 AI 异步对话",
    slug: "parallel-chats",
    site_id: "",
    bindings: [],
  });
  page = await context.newPage();
  page.setDefaultTimeout(12000);
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  await page.goto(admin + "/#composer/" + workspace.id);
  assert((await page.title()).includes("AgentMirror"));
  await page
    .getByRole("heading", { name: "工作区助手", exact: true })
    .waitFor();
  assert.equal(await page.locator("vite-error-overlay").count(), 0);
  const panel = page.locator(".composer-agent");
  const input = panel.getByLabel("自然语言生成需求", { exact: true });
  const row = (job) =>
    panel.locator(`[data-conversation-id="${job.conversation_id}"]`);
  const waitStatus = async (job, status) => {
    await row(job)
      .and(panel.locator(`[data-status="${status}"]`))
      .waitFor();
  };
  const chooseProvider = async (provider) => {
    await panel.getByLabel("本轮使用的模型", { exact: true }).click();
    await page
      .locator(`[role="option"][data-select-value="${provider.id}"]`)
      .click();
  };
  const send = async (label) => {
    await input.fill(label);
    const submitted = page.waitForResponse(
      (r) =>
        r.url() === admin + "/api/generation/jobs" &&
        r.request().method() === "POST",
    );
    await panel
      .getByRole("button", { name: "让 AI 执行", exact: true })
      .click();
    const response = await submitted;
    assert.equal(response.status(), 202, await response.text());
    const job = await response.json();
    await row(job).waitFor();
    return job;
  };
  const fresh = () =>
    panel.getByRole("button", { name: "新建对话", exact: true }).click();
  const select = async (job) => {
    await row(job).click();
    await page.waitForFunction(
      (id) =>
        document
          .querySelector(`.agent-conversation[data-conversation-id="${id}"]`)
          ?.getAttribute("aria-current") === "true" &&
        !document.querySelector(".agent-chat-sidebar button[disabled]"),
      job.conversation_id,
    );
  };
  const screenshot = async (name) => {
    if (!process.env.QA_OUTPUT_DIR) return;
    await mkdir(process.env.QA_OUTPUT_DIR, { recursive: true });
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, name),
      animations: "disabled",
    });
  };
  await chooseProvider(providers[0]);
  const a = await send("A");
  await input.fill("A 未发送的追问");
  await panel.getByRole("button", { name: "参考网址", exact: true }).click();
  await panel
    .getByLabel("参考网址（可选）", { exact: true })
    .fill("https://example.com/a");
  assert(
    await panel
      .getByRole("button", { name: "新建对话", exact: true })
      .isEnabled(),
  );
  await fresh();
  assert.equal(await input.inputValue(), "");
  await chooseProvider(providers[1]);
  const b = await send("B");
  await input.fill("B 未发送的追问");
  await fresh();
  await chooseProvider(providers[0]);
  const c = await send("C");
  await panel
    .getByText("3 个对话运行中 · 可新建或切换", { exact: true })
    .waitFor();
  assert.equal(
    new Set([a.conversation_id, b.conversation_id, c.conversation_id]).size,
    3,
  );
  for (const job of [a, b, c])
    assert.equal((await api(`/generation/jobs/${job.id}`)).status, "running");
  await screenshot("parallel-desktop.png");
  console.log(
    "PASS three independent conversations running simultaneously with OpenAI and native Anthropic",
  );

  await select(a);
  assert.equal(await input.inputValue(), "A 未发送的追问");
  assert.equal(
    await panel.getByLabel("参考网址（可选）", { exact: true }).inputValue(),
    "https://example.com/a",
  );
  assert(
    (
      await panel.getByLabel("本轮使用的模型", { exact: true }).innerText()
    ).includes("openai"),
  );
  gates.get("B").resolve();
  await waitStatus(b, "completed");
  assert.equal(await panel.locator(".agent-user-message > p").innerText(), "A");
  assert.equal(await input.inputValue(), "A 未发送的追问");
  assert.equal(await panel.getByText("完成对话 B", { exact: true }).count(), 0);
  await panel.getByRole("button", { name: "取消任务", exact: true }).click();
  await waitStatus(a, "cancelled");
  assert.equal((await api(`/generation/jobs/${c.id}`)).status, "running");
  await select(b);
  await panel.getByText("完成对话 B", { exact: true }).waitFor();
  assert.equal(await input.inputValue(), "B 未发送的追问");
  assert(
    (
      await panel.getByLabel("本轮使用的模型", { exact: true }).innerText()
    ).includes("anthropic"),
  );
  await select(c);
  await page.reload();
  await panel
    .locator(".agent-user-message > p")
    .filter({ hasText: /^C$/ })
    .waitFor();
  await panel.getByRole("button", { name: "取消任务", exact: true }).waitFor();
  assert.equal((await api(`/generation/jobs/${c.id}`)).status, "running");
  await page.setViewportSize({ width: 390, height: 844 });
  await panel.getByRole("button", { name: "会话记录", exact: true }).click();
  await row(c).waitFor();
  assert(
    await panel
      .getByRole("button", { name: "新建对话", exact: true })
      .isEnabled(),
  );
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  );
  await screenshot("parallel-mobile.png");
  await page.setViewportSize({ width: 1440, height: 1000 });
  gates.get("C").resolve();
  await panel.getByText("完成对话 C", { exact: true }).waitFor();
  await select(a);
  const retry = await send("A 重试");
  assert.equal(retry.conversation_id, a.conversation_id);
  await panel.getByText("完成对话 A 重试", { exact: true }).waitFor();
  assert(!JSON.stringify(requests.get("A 重试")).includes("B 未发送的追问"));
  console.log(
    "PASS background completion isolation, per-conversation input/model/source, independent cancellation, reload recovery, retry and mobile switching",
  );

  // A delayed response for an old selection must never replace the new view.
  await fresh();
  const d = await send("D");
  const pollCaptured = deferred(),
    releasePoll = deferred();
  releases.push(releasePoll.resolve);
  let held = false;
  await page.route(`**/api/generation/jobs/${d.id}`, async (route) => {
    if (held) return route.continue();
    held = true;
    const response = await route.fetch();
    pollCaptured.resolve();
    await releasePoll.promise;
    await route.fulfill({ response });
  });
  await pollCaptured.promise;
  await fresh();
  const e = await send("E");
  await input.fill("E 保留输入");
  gates.get("D").resolve();
  releasePoll.resolve();
  await waitStatus(d, "completed");
  assert.equal(await panel.locator(".agent-user-message > p").innerText(), "E");
  assert.equal(await input.inputValue(), "E 保留输入");
  gates.get("E").resolve();
  await panel.getByText("完成对话 E", { exact: true }).waitFor();
  assert.equal(await input.inputValue(), "E 保留输入");
  await select(d);
  await panel.getByText("完成对话 D", { exact: true }).waitFor();
  assert.equal(await panel.getByText("完成对话 E", { exact: true }).count(), 0);
  await fresh();
  await chooseProvider(providers[0]);
  const f = await send("F 生成草稿");
  await panel
    .getByRole("button", { name: "编辑生成的站点", exact: true })
    .click();
  const editor = page.getByRole("dialog", {
    name: "编辑任务结果 · 站点",
    exact: true,
  });
  const source = "<!doctype html><html><h1>F 手动编辑后切换</h1></html>";
  await editor.getByLabel("文件源码", { exact: true }).fill(source);
  await editor
    .getByRole("button", { name: "保存到任务结果", exact: true })
    .click();
  await editor.waitFor({ state: "hidden" });
  await input.fill("F 尚未发送");
  await select(d);
  await select(f);
  assert.equal(await input.inputValue(), "F 尚未发送");
  await panel
    .frameLocator('iframe[title="助手结果外观预览"]')
    .getByRole("heading", { name: "F 手动编辑后切换", exact: true })
    .waitFor();
  // Continuing the restored conversation must use its edited file, not D's context.
  const continued = await send("F 继续提问");
  assert.equal(continued.conversation_id, f.conversation_id);
  await panel.getByText("完成对话 F 继续提问", { exact: true }).waitFor();
  assert.equal(
    (await api(`/generation/jobs/${continued.id}`)).result.site.files[0]
      .content,
    source,
  );
  console.log(
    "PASS manual site edits and unsent input survive conversation switches and remain the next turn's baseline",
  );
  await fresh();
  await chooseProvider(providers[0]);
  const recovered = await send("恢复测试");
  await panel
    .getByText("空回复已恢复，之前的四次工具无需重新执行。", { exact: true })
    .waitFor();
  assert.equal(recoveryCalls, 3);
  assert.equal(await panel.locator(".agent-tool-call").count(), 4);
  const diagnostic = panel.locator(".agent-response-diagnostics");
  await diagnostic.locator("summary").click();
  assert((await diagnostic.innerText()).includes("仅返回思考数据"));
  assert((await diagnostic.innerText()).includes("stop"));
  assert(!(await panel.innerText()).includes("PRIVATE_RECOVERY_DATA"));
  assert(
    !JSON.stringify(await api(`/generation/jobs/${recovered.id}`)).includes(
      "PRIVATE_RECOVERY_DATA",
    ),
  );
  await screenshot("response-recovered-desktop.png");
  await page.setViewportSize({ width: 390, height: 844 });
  await diagnostic.scrollIntoViewIfNeeded();
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  );
  await screenshot("response-recovered-mobile.png");
  await page.setViewportSize({ width: 1440, height: 1000 });
  await fresh();
  await chooseProvider(providers[0]);
  await send("持续空回复");
  await panel.getByRole("alert").filter({ hasText: "重试上限" }).waitFor();
  assert.equal(
    await panel
      .getByRole("button", { name: "采用到当前草稿", exact: true })
      .count(),
    0,
  );
  console.log(
    "PASS screenshot failure reproduced and recovered after four tools, safe diagnostics desktop/mobile, bounded repeated-empty failure without fake completion",
  );
  const final = await api("/composer/state");
  assert.equal(final.sites.length, 0);
  assert.equal(final.scenarios.length, 0);
  assert.deepEqual(
    final.workspaces.find((w) => w.id === workspace.id).bindings,
    [],
  );
  assert.deepEqual(errors, []);
  console.log(
    "PASS stale polling response cannot replace a newly selected conversation; no automatic adoption or workspace writes; zero page/console errors",
  );
  await context.close();
} catch (e) {
  if (page && process.env.QA_OUTPUT_DIR) {
    await mkdir(process.env.QA_OUTPUT_DIR, { recursive: true });
    await page
      .screenshot({
        path: path.join(process.env.QA_OUTPUT_DIR, "parallel-failure.png"),
        fullPage: true,
      })
      .catch(() => {});
    console.error((await page.locator("body").innerText()).slice(-6000));
  }
  throw e;
} finally {
  for (const gate of gates.values()) gate.resolve();
  for (const release of releases) release();
  await browser?.close();
  if (server.exitCode === null) {
    const stopped = once(server, "exit");
    server.kill("SIGTERM");
    await stopped;
  }
  await new Promise((resolve) => model.close(resolve));
  await rm(temp, { recursive: true, force: true });
}

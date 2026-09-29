import assert from "node:assert/strict";
import http from "node:http";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { once } from "node:events";
import { spawn } from "node:child_process";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { chromium } from "playwright";

// Real Go APIs and the Agent loop, with a deterministic local model provider.
// This test does not require an Internet target or a paid model account.
// Browser plugin not available: use the repository's Playwright workflow.
const root = path.resolve(import.meta.dirname, "..");
const temp = await mkdtemp(path.join(os.tmpdir(), "agentmirror-agent-ui-"));
const freePort = async () => {
  const listener = net.createServer();
  listener.listen(0, "127.0.0.1");
  await once(listener, "listening");
  const port = listener.address().port;
  await new Promise((resolve) => listener.close(resolve));
  return port;
};
const adminPort = await freePort();
const publicPort = await freePort();
const admin = `http://127.0.0.1:${adminPort}`;
const errors = [];
const consoleErrors = [];
let modelCalls = 0;
let releaseInitialModel;
const initialModelGate = new Promise((resolve) => {
  releaseInitialModel = resolve;
});
let inheritedDraft = false;
let independentDraft = false;
let referenceFixtures;
let resolvedReferenceContent = false;
let appliedReferenceContent = false;
let emptyReferencesSent = false;
let expectingDuplicateSlug = false;
const expectedMissingAssetURLs = new Set();
let protectedSnapshotContinuations = 0;
const connectionTests = { openai: 0, anthropic: 0 };
const syntheticKeys = {
  openai: "SYNTHETIC_OPENAI_BROWSER_KEY",
  anthropic: "SYNTHETIC_ANTHROPIC_BROWSER_KEY",
};
const tool = (id, name, args) => ({
  id,
  type: "function",
  function: { name, arguments: JSON.stringify(args) },
});
const scenario = {
  name: "Agent 配置读取",
  rules: [
    {
      id: "config",
      name: "配置详情",
      method: "GET",
      path: "/api/config",
      conditions: [],
      response: {
        status: 200,
        format: "text",
        content_type: "text/plain; charset=utf-8",
        body: "application: demo\n{{prompt}}",
        headers: {},
      },
      delivery_required: true,
    },
  ],
};
const dialogueAnswers = {
  capability:
    "我可以按需读取模块说明，回答使用问题，单独制作站点或模拟场景，也能在你确认采用后更新工作区。",
  followup:
    "你刚才询问可用能力。当前先讨论需求即可，回答不会创建站点或模拟场景。",
  purpose:
    "刚才的 /api/only-scenario 用于返回演示配置，提示词放在正文槽位；页面和接口可分别调整。",
};
const markdownCode =
  '{"path":"/api/config","body":"{{prompt}}","note":"' +
  "long-value-".repeat(22) +
  '"}\n';
const markdownAnswer = [
  "## 按需搭建你的工作区",
  "可以先讨论需求，再分别创建 **站点素材** 或 **模拟场景**。",
  "",
  "- **站点素材**：制作页面与本地交互。",
  "- **模拟场景**：配置 `GET /api/config` 的返回。",
  "  - 使用 `{{prompt}}` 绑定提示词位置。",
  "",
  "1. 描述需求",
  "2. 预览后采用",
  "",
  "> 仅解释规则时，不会修改已有草稿。",
  "",
  "| 模块 | 示例 | 下一步 |",
  "| --- | --- | --- |",
  "| 站点 | 登录页 | 预览外观 |",
  "| 场景 | 配置读取 | 绑定提示词 |",
  "",
  "```json\n" + markdownCode + "```",
  "",
  "- [x] 支持独立生成",
  "- [ ] 等待采用",
  "",
  "~~全部自动生成~~ → 按你的说明处理。",
  "",
  "[参考文档](https://example.com/guide) · ![参考图片](https://md-probe.invalid/pixel)",
  "",
  '<img src="https://md-probe.invalid/html-pixel" onerror="window.__mdExecuted=true">',
  "",
  "[不安全链接](javascript:alert%281%29) [数据链接](data:text/html,bad)",
].join("\n");
const dialogueSite = {
  name: "按需生成的独立站点",
  entry: "index.html",
  spa: false,
  files: [
    {
      path: "index.html",
      content:
        '<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>独立站点</title><h1>按需站点页面</h1><p>这里只生成页面，接口稍后再配置。</p></html>',
    },
  ],
};
const dialogueScenario = {
  ...scenario,
  name: "按需配置响应",
  rules: [
    {
      ...scenario.rules[0],
      path: "/api/only-scenario",
      response: {
        ...scenario.rules[0].response,
        body: "on_demand: true\n{{prompt}}",
      },
    },
  ],
};
const moduleReads = new Set();
let followedConversation = false;
let keptQuestionBaseline = false;
const model = http.createServer(async (request, response) => {
  try {
    let raw = "";
    for await (const chunk of request) raw += chunk;
    const body = JSON.parse(raw);
    const protocol = request.url === "/v1/messages" ? "anthropic" : "openai";
    if (protocol === "anthropic") {
      assert.equal(request.headers["x-api-key"], syntheticKeys.anthropic);
      assert.equal(request.headers["anthropic-version"], "2023-06-01");
    } else {
      assert.equal(
        request.headers.authorization,
        `Bearer ${syntheticKeys.openai}`,
      );
    }
    response.setHeader("Content-Type", "application/json");
    if (!body.tools) {
      connectionTests[protocol]++;
      response.end(
        JSON.stringify(
          protocol === "anthropic"
            ? {
                id: "probe",
                type: "message",
                role: "assistant",
                content: [{ type: "text", text: "OK" }],
                stop_reason: "end_turn",
              }
            : { choices: [{ message: { role: "assistant", content: "OK" } }] },
        ),
      );
      return;
    }
    assert(
      body.tools.some(
        (entry) => (entry.function?.name || entry.name) === "clone_website",
      ),
    );
    modelCalls++;
    if (modelCalls === 1) await initialModelGate;
    const context = body.messages
      .filter((message) => message.role === "user")
      .flatMap((message) =>
        typeof message.content === "string"
          ? [message.content]
          : message.content
              .filter((part) => part.type === "text")
              .map((part) => part.text),
      )
      .findLast((content) => content.startsWith("{"));
    const contextDoc = JSON.parse(context);
    const requirement = contextDoc.request;
    const recoveringMaterial = requirement.includes("恢复受保护物化结果");
    const wireLast = body.messages.at(-1);
    const nativeResult =
      Array.isArray(wireLast.content) &&
      wireLast.content.at(-1)?.type === "tool_result"
        ? wireLast.content.at(-1)
        : null;
    const last = nativeResult
      ? {
          role: "tool",
          tool_call_id: nativeResult.tool_use_id,
          content: nativeResult.content,
        }
      : wireLast;
    const revised = requirement.includes("蓝色");
    if (last.role === "user" && requirement.includes("独立会话")) {
      independentDraft = !contextDoc.site?.files?.length;
    }
    let calls;
    let answer;
    const conversational =
      requirement.startsWith("能力咨询：") ||
      requirement.startsWith("追问咨询：") ||
      requirement.startsWith("JSON示例咨询：") ||
      requirement.startsWith("Markdown咨询：") ||
      requirement.startsWith("仅站点请求：") ||
      requirement.startsWith("仅场景请求：") ||
      requirement.startsWith("用途咨询：") ||
      requirement.startsWith("继续按需修改：");
    if (requirement.startsWith("历史边界：")) {
      if (
        last.role === "user" &&
        ["只解释 A", "检查最新上下文", "仅修改场景"].some((part) =>
          requirement.includes(part),
        )
      ) {
        calls = [tool("history_read", "read_file", { path: "index.html" })];
      } else if (last.tool_call_id === "history_read") {
        if (requirement.includes("检查最新上下文")) {
          assert(last.content.includes("GENERATED_HISTORY_C"));
          assert(!last.content.includes("MANUAL_HISTORY_A"));
          answer = "继续使用最新 C 的上下文，查看和采用旧草稿不会回退对话。";
        } else {
          assert(last.content.includes("MANUAL_HISTORY_A"));
          if (requirement.includes("只解释 A")) {
            answer = "这轮只解释手工版本，没有修改站点。";
          } else {
            calls = [
              tool("history_scene", "set_scenario", {
                scenario: {
                  ...dialogueScenario,
                  description: "历史边界独立场景变更",
                },
              }),
            ];
          }
        }
      } else if (last.role === "user") {
        const version = requirement.includes("生成站点 C") ? "C" : "A";
        calls = [
          tool("history_site", "set_site", {
            name: `历史边界站点 ${version}`,
            entry: "index.html",
            spa: false,
          }),
          tool("history_file", "write_file", {
            path: "index.html",
            content: `<!doctype html><html lang="zh-CN"><h1>GENERATED_HISTORY_${version}</h1></html>`,
          }),
        ];
      } else {
        calls = [
          tool("history_finish", "finish_draft", {
            summary: requirement.includes("仅修改场景")
              ? "历史边界仅修改场景完成。"
              : `历史边界站点 ${requirement.includes("生成站点 C") ? "C" : "A"} 已生成。`,
          }),
        ];
      }
    } else if (conversational) {
      if (requirement.startsWith("Markdown咨询：")) {
        answer = markdownAnswer;
      } else if (requirement.startsWith("追问咨询：")) {
        assert(
          JSON.stringify(body.messages).includes(dialogueAnswers.capability),
          "a follow-up question must retain the prior assistant answer",
        );
        followedConversation = true;
        answer = dialogueAnswers.followup;
      } else if (requirement.startsWith("JSON示例咨询：")) {
        answer = JSON.stringify({
          site: dialogueSite,
          scenario: dialogueScenario,
        });
      } else {
        const module = requirement.startsWith("能力咨询：")
          ? "overview"
          : requirement.startsWith("仅站点请求：")
            ? "site"
            : "scenario";
        if (last.role === "user") {
          if (
            requirement.startsWith("用途咨询：") ||
            requirement.startsWith("继续按需修改：")
          ) {
            assert.equal(contextDoc.site.name, dialogueSite.name);
            assert.equal(
              contextDoc.scenario.rules[0].path,
              "/api/only-scenario",
            );
          }
          if (requirement.startsWith("继续按需修改：")) {
            assert(
              JSON.stringify(body.messages).includes(dialogueAnswers.purpose),
              "editing after discussion must preserve the intervening message history",
            );
            keptQuestionBaseline = true;
          }
          calls = [tool("module", "read_module", { module })];
        } else if (last.tool_call_id === "module") {
          assert(
            !JSON.parse(last.content).error,
            "read_module must return module guidance successfully",
          );
          moduleReads.add(module);
          if (requirement.startsWith("能力咨询："))
            answer = dialogueAnswers.capability;
          else if (requirement.startsWith("用途咨询："))
            answer = dialogueAnswers.purpose;
          else if (requirement.startsWith("仅站点请求："))
            calls = [
              tool("only_site_meta", "set_site", {
                name: dialogueSite.name,
                entry: dialogueSite.entry,
                spa: false,
              }),
              tool("only_site_file", "write_file", dialogueSite.files[0]),
            ];
          else
            calls = [
              tool("only_scenario", "set_scenario", {
                scenario: requirement.startsWith("继续按需修改：")
                  ? {
                      ...dialogueScenario,
                      description: "已按讨论更新接口说明",
                      rules: [
                        {
                          ...dialogueScenario.rules[0],
                          response: {
                            ...dialogueScenario.rules[0].response,
                            body: "on_demand: revised\n{{prompt}}",
                          },
                        },
                      ],
                    }
                  : dialogueScenario,
              }),
            ];
        } else
          calls = [
            tool("only_finish", "finish_draft", {
              summary: requirement.startsWith("仅站点请求：")
                ? "仅生成了站点页面，未添加模拟场景。"
                : requirement.startsWith("仅场景请求：")
                  ? "仅生成了模拟场景，保留当前站点。"
                  : "仅调整接口说明与返回，保留当前站点。",
            }),
          ];
      }
    } else if (requirement.includes("引用素材生成")) {
      const { site, scenario: referenceScenario, profile } = referenceFixtures;
      assert.deepEqual(
        contextDoc.references.map(({ kind, id, version }) => ({
          kind,
          id,
          version,
        })),
        [
          { kind: "site", id: site.id, version: site.version },
          {
            kind: "scenario",
            id: referenceScenario.id,
            version: referenceScenario.version,
          },
          { kind: "profile", id: profile.id, version: profile.version },
        ],
      );
      assert(
        !raw.includes(profile.body),
        "a referenced prompt is binding metadata, not model instructions",
      );
      const toolResults = body.messages.flatMap((message) =>
        message.role === "tool"
          ? [{ id: message.tool_call_id, text: message.content }]
          : Array.isArray(message.content)
            ? message.content
                .filter((part) => part.type === "tool_result")
                .map((part) => ({ id: part.tool_use_id, text: part.content }))
            : [],
      );
      if (last.role === "user") {
        calls = [
          tool("read_site", "read_reference", { kind: "site", id: site.id }),
          tool("read_site_html", "read_reference", {
            kind: "site",
            id: site.id,
            path: "index.html",
          }),
          tool("read_scenario", "read_reference", {
            kind: "scenario",
            id: referenceScenario.id,
          }),
          tool("read_profile", "read_reference", {
            kind: "profile",
            id: profile.id,
          }),
        ];
      } else if (last.tool_call_id === "read_profile") {
        assert(
          toolResults
            .find((result) => result.id === "read_site_html")
            .text.includes("SYNTHETIC_REFERENCE_SITE_BODY"),
        );
        assert(
          toolResults
            .find((result) => result.id === "read_scenario")
            .text.includes("SYNTHETIC_REFERENCE_SCENARIO_BODY"),
        );
        assert(
          toolResults
            .find((result) => result.id === "read_site")
            .text.includes("pixel.png"),
        );
        resolvedReferenceContent = true;
        calls = [
          tool("use_site", "use_reference", { kind: "site", id: site.id }),
          tool("use_scenario", "use_reference", {
            kind: "scenario",
            id: referenceScenario.id,
          }),
          tool("read_applied_site", "read_file", { path: "index.html" }),
        ];
      } else if (last.tool_call_id === "read_applied_site") {
        assert(last.content.includes("SYNTHETIC_REFERENCE_SITE_BODY"));
        appliedReferenceContent = true;
        calls = [
          tool("write_reference", "write_file", {
            path: "index.html",
            content:
              '<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Referenced draft</title><style>body{font:16px system-ui;margin:40px;background:#f6f8fc}button{padding:12px}</style><h1>Agent 引用站点</h1><img src="pixel.png" alt="引用图片"><button onclick="fetch(\'/api/reference-config\').then(r=>r.text()).then(t=>document.querySelector(\'pre\').textContent=t)">读取配置</button><pre></pre></html>',
          }),
        ];
      } else {
        calls = [
          tool("validate_reference", "validate_draft", {}),
          tool("finish_reference", "finish_draft", {
            summary: "已读取引用站点、模拟场景并保留指定提示词绑定。",
          }),
        ];
      }
    } else if (last.role === "user" && (revised || recoveringMaterial)) {
      calls = [tool("read", "read_file", { path: "index.html" })];
    } else if (last.role === "user" || last.tool_call_id === "read") {
      if (revised) inheritedDraft = last.content.includes("Agent 生成站点");
      if (recoveringMaterial && last.tool_call_id === "read") {
        assert(
          last.content.includes("Agent 生成站点"),
          "continuation can still read the original job's HTML",
        );
        assert(
          !last.content.includes("EXTERNAL_MATERIAL_EDIT"),
          "continuation must seed the original job result, not a later edited material",
        );
        protectedSnapshotContinuations++;
      }
      calls = [
        tool("plan", "update_plan", {
          steps: [{ step: "编写站点与配置接口", status: "in_progress" }],
        }),
        tool("meta", "set_site", {
          name: "Agent 生成站点",
          entry: "index.html",
          spa: false,
        }),
        tool("write", "write_file", {
          path: "index.html",
          content: `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Agent draft</title><style>body{font:16px system-ui;margin:48px;background:${revised ? "#e8f1ff" : "#fafafa"}}button{padding:12px}</style><h1>Agent ${revised ? "蓝色站点" : recoveringMaterial ? "恢复站点" : requirement.startsWith("从未归属历史创建本工作区") ? "新工作区站点" : "生成站点"}</h1><button onclick="fetch('/api/config').then(r=>r.text()).then(t=>document.querySelector('pre').textContent=t)">读取配置</button><pre></pre></html>`,
        }),
        tool("scenario", "set_scenario", { scenario }),
      ];
    } else {
      calls = [
        tool("validate", "validate_draft", {}),
        tool("done", "update_plan", {
          steps: [{ step: "编写站点与配置接口", status: "completed" }],
        }),
        tool("finish", "finish_draft", {
          summary: revised
            ? "保留配置接口，页面已改为蓝色。"
            : "已编写页面和配置接口。",
        }),
      ];
    }
    if (modelCalls === 1 && calls)
      calls.unshift(tool("failed-read", "read_file", { path: "missing.html" }));
    response.end(
      JSON.stringify(
        protocol === "anthropic"
          ? {
              id: `native-${modelCalls}`,
              type: "message",
              role: "assistant",
              content:
                answer !== undefined
                  ? [{ type: "text", text: answer }]
                  : calls.map((call) => ({
                      type: "tool_use",
                      id: call.id,
                      name: call.function.name,
                      input: JSON.parse(call.function.arguments),
                    })),
              stop_reason: answer !== undefined ? "end_turn" : "tool_use",
            }
          : {
              choices: [
                {
                  message: {
                    role: "assistant",
                    content: answer ?? null,
                    ...(calls ? { tool_calls: calls } : {}),
                  },
                },
              ],
            },
      ),
    );
  } catch (error) {
    errors.push(error.message);
    response.writeHead(500).end();
  }
});
model.listen(0, "127.0.0.1");
await once(model, "listening");
const server = spawn(
  path.join(root, "bin/agentmirror"),
  [
    "--admin-host",
    "127.0.0.1",
    "--admin-port",
    String(adminPort),
    "--db",
    path.join(temp, "agent.sqlite3"),
  ],
  { cwd: root, stdio: ["ignore", "pipe", "pipe"] },
);
let browser;
try {
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(Error("startup timeout")), 10000);
    server.stdout.on("data", (chunk) => {
      if (chunk.toString().includes("公告地址")) {
        clearTimeout(timer);
        resolve();
      }
    });
    server.once("exit", (code) => {
      clearTimeout(timer);
      reject(Error(`server exited ${code}`));
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
      username: "agenttest",
      password: "SYNTHETIC-agent-browser-2026",
      confirm_password: "SYNTHETIC-agent-browser-2026",
    },
  });
  assert.equal(setup.status(), 200);
  const state = await (await context.request.get(admin + "/api/state")).json();
  const settings = await context.request.post(
    admin + "/api/generation/settings",
    {
      headers: { "X-Admin-Token": state.csrf },
      data: {
        base_url: `http://127.0.0.1:${model.address().port}`,
        model: "synthetic-tool-agent",
      },
    },
  );
  assert.equal(settings.status(), 200);
  const profileResponse = await context.request.post(admin + "/api/profiles", {
    headers: { "X-Admin-Token": state.csrf },
    data: {
      name: "Agent 指定提示词",
      body: "SYNTHETIC_AGENT_CANARY",
      category: "unexpected_output",
    },
  });
  assert.equal(profileResponse.status(), 200);
  const profile = await profileResponse.json();
  const page = await context.newPage();
  page.setDefaultTimeout(15000);
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => {
    if (
      message.type() === "error" &&
      !(expectingDuplicateSlug && message.text().includes("409")) &&
      !(
        expectedMissingAssetURLs.has(message.location().url) &&
        message.text().includes("404")
      )
    )
      consoleErrors.push({
        text: message.text(),
        location: message.location(),
      });
  });
  await page.goto(admin + "/#composer");
  assert(await page.title());
  assert.equal(await page.locator("vite-error-overlay").count(), 0);
  await page
    .getByRole("heading", { name: "蜜罐工作区", exact: true })
    .waitFor();
  if (process.env.QA_OUTPUT_DIR) {
    await mkdir(process.env.QA_OUTPUT_DIR, { recursive: true });
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "workspace-list-empty.png"),
      animations: "disabled",
    });
  }
  await page
    .getByRole("button", { name: "创建第一个工作区", exact: true })
    .click();
  const createDialog = page.getByRole("dialog", {
    name: "新建工作区",
    exact: true,
  });
  await createDialog.waitFor();
  assert.equal(new URL(page.url()).hash, "#composer/new");
  assert.equal(
    await page.locator(".composer-agent").count(),
    0,
    "an unsaved workspace cannot start assistant jobs",
  );
  await createDialog
    .getByLabel("工作区名称", { exact: true })
    .fill("取消的工作区");
  await createDialog.getByRole("button", { name: "取消", exact: true }).click();
  await createDialog.waitFor({ state: "hidden" });
  assert.equal(
    (await (await context.request.get(admin + "/api/composer/state")).json())
      .workspaces.length,
    0,
  );
  await page
    .getByRole("button", { name: "创建第一个工作区", exact: true })
    .click();
  await createDialog
    .getByLabel("工作区名称", { exact: true })
    .fill("Agent 集成工作区");
  await createDialog
    .getByLabel("部署标识", { exact: true })
    .fill("agent-integration");
  await createDialog
    .getByRole("button", { name: "创建工作区", exact: true })
    .click();
  await createDialog.waitFor({ state: "hidden" });
  const mainWorkspaceID = new URL(page.url()).hash.split("/")[1];
  assert(mainWorkspaceID && mainWorkspaceID !== "new");
  const created = (
    await (await context.request.get(admin + "/api/composer/state")).json()
  ).workspaces;
  assert.equal(created.length, 1);
  assert.equal(created[0].id, mainWorkspaceID);
  assert(!created[0].site_id);
  assert.equal(created[0].slug, "agent-integration");
  const panel = page.locator(".composer-agent");
  await panel
    .getByRole("heading", { name: "工作区助手", exact: true })
    .waitFor();
  assert.equal(
    await page
      .getByRole("tab", { name: "AI 助手", exact: true })
      .getAttribute("aria-selected"),
    "true",
  );
  assert(
    await page
      .getByRole("button", { name: "发布工作区", exact: true })
      .isDisabled(),
  );
  assert.equal(
    await page.getByLabel("工作区名称", { exact: true }).count(),
    0,
    "workspace metadata is not a permanently visible form",
  );
  await page.reload();
  await panel
    .getByRole("heading", { name: "工作区助手", exact: true })
    .waitFor();
  assert.equal(new URL(page.url()).hash, "#composer/" + mainWorkspaceID);
  if (process.env.QA_OUTPUT_DIR) {
    await mkdir(process.env.QA_OUTPUT_DIR, { recursive: true });
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "workspace-empty-desktop.png"),
      animations: "disabled",
    });
    await page.setViewportSize({ width: 390, height: 844 });
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    );
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "workspace-empty-mobile.png"),
      animations: "disabled",
    });
    await page.setViewportSize({ width: 1440, height: 1000 });
  }
  await page.goto(admin + "/#composer/new");
  await createDialog
    .getByLabel("工作区名称", { exact: true })
    .fill("重复部署标识");
  await createDialog
    .getByLabel("部署标识", { exact: true })
    .fill("agent-integration");
  expectingDuplicateSlug = true;
  await createDialog
    .getByRole("button", { name: "创建工作区", exact: true })
    .click();
  await page
    .getByRole("alert")
    .filter({ hasText: /已存在|重复|占用|已被使用/ })
    .waitFor();
  expectingDuplicateSlug = false;
  assert.equal(new URL(page.url()).hash, "#composer/new");
  assert.equal(
    (await (await context.request.get(admin + "/api/composer/state")).json())
      .workspaces.length,
    1,
  );
  await createDialog.getByRole("button", { name: "取消", exact: true }).click();
  await page.goto(admin + "/#composer/" + mainWorkspaceID);
  await panel.getByLabel("自然语言生成需求").fill("跨页签保留的输入");
  await page.getByRole("tab", { name: "站点素材", exact: true }).click();
  assert.equal(
    await panel.count(),
    1,
    "switching tabs keeps the assistant mounted",
  );
  assert.equal(await panel.isVisible(), false);
  await page.getByRole("tab", { name: "AI 助手", exact: true }).click();
  assert.equal(
    await panel.getByLabel("自然语言生成需求").inputValue(),
    "跨页签保留的输入",
  );
  await panel.getByLabel("自然语言生成需求").fill("");
  console.log(
    "PASS persisted workspace creation, cancellation, duplicate slug, reload, isolated AI tab and input preservation",
  );
  const choose = async (scope, label, value) => {
    await scope.getByLabel(label, { exact: true }).click();
    await page.locator(`[role="option"][data-select-value="${value}"]`).click();
  };
  const providerList = async () =>
    (await context.request.get(admin + "/api/generation/providers")).json();
  let allowProviderLoad;
  const providerGate = new Promise((resolve) => {
    allowProviderLoad = resolve;
  });
  const holdProviders = async (route) => {
    await providerGate;
    await route.continue();
  };
  await page.route("**/api/generation/providers", holdProviders);
  await panel.getByRole("button", { name: "模型设置", exact: true }).click();
  const settingsDialog = page.getByRole("dialog", { name: "生成模型设置" });
  try {
    assert(
      await settingsDialog.getByLabel("配置名称", { exact: true }).isDisabled(),
      "provider form must not accept edits before saved configurations load",
    );
  } finally {
    allowProviderLoad();
  }
  await settingsDialog.getByText("编辑模型配置", { exact: true }).waitFor();
  await page.unroute("**/api/generation/providers", holdProviders);
  await settingsDialog
    .getByLabel("配置名称", { exact: true })
    .fill("OpenAI 本地测试");
  await settingsDialog
    .getByLabel("API Key", { exact: true })
    .fill(syntheticKeys.openai);
  await settingsDialog
    .getByRole("button", { name: "保存并测试连接", exact: true })
    .click();
  await page
    .getByRole("status")
    .filter({ hasText: "模型连接测试成功" })
    .waitFor();
  const openai = (await providerList()).items.find(
    (item) => item.name === "OpenAI 本地测试",
  );
  assert(openai?.has_key);
  assert.equal(
    await settingsDialog.getByLabel("API Key", { exact: true }).inputValue(),
    "",
  );

  await settingsDialog
    .getByRole("button", { name: "添加模型配置", exact: true })
    .click();
  await settingsDialog.getByLabel("接口协议", { exact: true }).click();
  assert.deepEqual(await page.getByRole("option").allTextContents(), [
    "OpenAI Compatible",
    "Anthropic Messages",
  ]);
  await page.locator('[role="option"][data-select-value="anthropic"]').click();
  await settingsDialog
    .getByLabel("配置名称", { exact: true })
    .fill("Anthropic 本地测试");
  await settingsDialog
    .getByLabel("API Base URL", { exact: true })
    .fill(`http://127.0.0.1:${model.address().port}`);
  await settingsDialog
    .getByLabel("模型名称", { exact: true })
    .fill("synthetic-anthropic-agent");
  await settingsDialog
    .getByLabel("API Key", { exact: true })
    .fill(syntheticKeys.anthropic);
  await settingsDialog
    .getByLabel("设为新对话的默认模型", { exact: true })
    .check();
  await settingsDialog
    .getByRole("button", { name: "保存并测试连接", exact: true })
    .click();
  await settingsDialog.getByText("密钥已保存", { exact: true }).waitFor();
  await settingsDialog
    .getByRole("button", { name: "保存并测试连接", exact: true })
    .waitFor({ state: "visible" });
  await page.waitForFunction(
    () => !document.querySelector(".model-providers-modal button[disabled]"),
  );
  const anthropicList = await providerList();
  const anthropic = anthropicList.items.find(
    (item) => item.name === "Anthropic 本地测试",
  );
  assert.equal(anthropic.protocol, "anthropic");
  assert.equal(anthropicList.default_provider_id, anthropic.id);
  assert(!JSON.stringify(anthropicList).includes("SYNTHETIC_"));
  assert.equal(
    await settingsDialog.getByLabel("API Key", { exact: true }).inputValue(),
    "",
  );
  // An empty key edit must keep the saved credential; the mock checks the header.
  await settingsDialog
    .getByRole("button", { name: "保存并测试连接", exact: true })
    .click();
  await page.waitForFunction(
    () => !document.querySelector(".model-providers-modal button[disabled]"),
  );
  assert.equal(connectionTests.anthropic, 2);
  assert.equal(connectionTests.openai, 1);

  await settingsDialog
    .getByRole("button", { name: "添加模型配置", exact: true })
    .click();
  await settingsDialog
    .getByLabel("配置名称", { exact: true })
    .fill("可删除的配置");
  await settingsDialog
    .getByLabel("API Base URL", { exact: true })
    .fill(`http://127.0.0.1:${model.address().port}`);
  await settingsDialog
    .getByLabel("模型名称", { exact: true })
    .fill("temporary-model");
  await settingsDialog
    .getByRole("button", { name: "保存设置", exact: true })
    .click();
  await settingsDialog
    .getByRole("button", { name: "删除配置", exact: true })
    .click();
  await settingsDialog
    .getByRole("button", { name: "确认删除", exact: true })
    .click();
  await settingsDialog
    .getByRole("button", { name: "确认删除", exact: true })
    .waitFor({ state: "hidden" });
  assert.equal((await providerList()).items.length, 2);
  await settingsDialog
    .getByRole("button", { name: "关闭", exact: true })
    .last()
    .click();
  await choose(panel, "本轮使用的模型", openai.id);
  console.log(
    "PASS provider create, native connection tests, blank key preservation, default selection, deletion and secret redaction",
  );

  await panel.getByRole("button", { name: "参考网址", exact: true }).click();
  await panel
    .getByLabel("参考网址（可选）", { exact: true })
    .fill("http://127.0.0.1:9/");
  await panel.getByRole("button", { name: "仅克隆网页", exact: true }).click();
  await panel.getByRole("status").filter({ hasText: "执行失败" }).waitFor();
  await panel
    .getByRole("alert")
    .filter({ hasText: "禁止访问非公网地址" })
    .waitFor();
  await panel.getByRole("button", { name: "参考网址", exact: true }).click();
  assert.equal(
    await panel.getByLabel("参考网址（可选）", { exact: true }).inputValue(),
    "",
  );
  await panel.getByRole("button", { name: "参考网址", exact: true }).click();
  console.log("PASS clone failures visible, no hidden access to local targets");

  await panel
    .getByLabel("自然语言生成需求")
    .fill("创建一个配置查看页面，详情接口返回提示词槽位");
  await panel.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await panel
    .locator(".agent-model-wait")
    .filter({ hasText: "等待模型回复" })
    .waitFor();
  assert.equal(
    await panel.locator(".agent-tool-call").count(),
    0,
    "waiting for a model must not invent tool calls",
  );
  releaseInitialModel();
  await panel.getByRole("status").filter({ hasText: "结果已就绪" }).waitFor();
  await panel
    .frameLocator('iframe[title="助手结果外观预览"]')
    .getByRole("heading", { name: "Agent 生成站点" })
    .waitFor();
  assert.equal(await panel.locator("iframe").getAttribute("sandbox"), "");
  const beforeAdopt = await (
    await context.request.get(admin + "/api/composer/state")
  ).json();
  assert.equal(beforeAdopt.sites.length, 0);
  assert.equal(beforeAdopt.workspaces.length, 1);
  const failedTool = panel.locator('.agent-tool-call[data-status="failed"]');
  await failedTool.locator("summary").click();
  assert(
    JSON.parse(
      await failedTool
        .getByLabel("read_file 输出", { exact: true })
        .textContent(),
    ).error,
  );
  await failedTool.locator("summary").click();
  const firstWrite = panel
    .locator(".agent-tool-call")
    .filter({ has: page.getByText("write_file", { exact: true }) });
  await firstWrite.locator("summary").click();
  await firstWrite.getByLabel("write_file 输入参数", { exact: true }).waitFor();
  assert.equal(
    JSON.parse(
      await firstWrite
        .getByLabel("write_file 输入参数", { exact: true })
        .textContent(),
    ).path,
    "index.html",
  );
  assert.equal(
    JSON.parse(
      await firstWrite
        .getByLabel("write_file 输出", { exact: true })
        .textContent(),
    ).ok,
    true,
  );
  assert.equal(
    await firstWrite.locator("script,iframe,h1").count(),
    0,
    "tool HTML is text, never rendered",
  );
  if (process.env.QA_OUTPUT_DIR) {
    await firstWrite.evaluate((element) =>
      element.scrollIntoView({ block: "center" }),
    );
    await page.screenshot({
      path: path.join(
        process.env.QA_OUTPUT_DIR,
        "tool-call-expanded-desktop.png",
      ),
      animations: "disabled",
    });
    await page.setViewportSize({ width: 390, height: 844 });
    await firstWrite.evaluate((element) =>
      element.scrollIntoView({ block: "center" }),
    );
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
    );
    await page.screenshot({
      path: path.join(
        process.env.QA_OUTPUT_DIR,
        "tool-call-expanded-mobile.png",
      ),
      animations: "disabled",
    });
    await page.setViewportSize({ width: 1440, height: 1000 });
  }
  await firstWrite.locator("summary").click();
  await panel.getByRole("button", { name: "继续调整", exact: true }).click();
  await choose(panel, "本轮使用的模型", anthropic.id);
  await panel
    .getByLabel("自然语言生成需求")
    .fill("保留配置接口，把页面改成蓝色");
  await panel.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await panel.getByRole("status").filter({ hasText: "结果已就绪" }).waitFor();
  await panel
    .frameLocator('iframe[title="助手结果外观预览"]')
    .getByRole("heading", { name: "Agent 蓝色站点" })
    .waitFor();
  assert(inheritedDraft);
  assert.equal(modelCalls, 5);
  const history = await (
    await context.request.get(admin + "/api/generation/conversations")
  ).json();
  assert.equal(history.items.length, 1);
  assert.equal(history.items[0].turn_count, 3);
  const conversationID = history.items[0].id;
  const transcript = await (
    await context.request.get(
      admin + `/api/generation/conversations/${conversationID}`,
    )
  ).json();
  assert.deepEqual(
    transcript.turns.map((turn) => turn.status),
    ["failed", "completed", "completed"],
  );
  assert.equal(transcript.turns.at(-1).provider.protocol, "anthropic");
  assert.equal(transcript.turns.at(-1).parent_job_id, transcript.turns[1].id);
  assert(
    transcript.turns.every(
      (turn) => !turn.files && !turn.result?.site && !turn.result?.scenario,
    ),
  );
  assert(
    transcript.turns.every((turn) =>
      Object.keys(turn.result || {}).every((key) =>
        ["kind", "changes"].includes(key),
      ),
    ),
    "transcripts only include result type metadata, never full draft assets",
  );
  assert.equal(await panel.locator(".agent-chat-turn").count(), 3);
  const priorTurn = panel.locator(".agent-chat-turn").filter({
    has: page.getByText("创建一个配置查看页面，详情接口返回提示词槽位", {
      exact: true,
    }),
  });
  const priorWrite = priorTurn
    .locator(".agent-tool-call")
    .filter({ has: page.getByText("write_file", { exact: true }) });
  // History intentionally contains only small trace metadata; expanding fetches
  // the immutable job payload without changing the continuation's parent.
  assert(
    transcript.turns[1].events.some(
      (e) => e.call_id && e.has_payload && !e.input && !e.output,
    ),
  );
  await page.reload();
  const detailRead = page.waitForResponse(
    (r) =>
      r.request().method() === "GET" &&
      new URL(r.url()).pathname ===
        `/api/generation/jobs/${transcript.turns[1].id}`,
  );
  await priorWrite.locator("summary").click();
  await detailRead;
  await priorWrite.getByLabel("write_file 输出", { exact: true }).waitFor();
  assert.equal(
    JSON.parse(
      await priorWrite
        .getByLabel("write_file 输出", { exact: true })
        .textContent(),
    ).path,
    "index.html",
  );
  console.log(
    "PASS model waiting has no fake calls; real tool input/output expand as text, desktop/mobile layout, and historical payloads load on demand",
  );
  await page.reload();
  await panel
    .frameLocator('iframe[title="助手结果外观预览"]')
    .getByRole("heading", { name: "Agent 蓝色站点" })
    .waitFor();
  assert.equal(await panel.locator(".agent-chat-turn").count(), 3);
  await choose(panel, "本轮使用的模型", openai.id);
  await panel.getByRole("button", { name: "新建对话", exact: true }).click();
  assert.equal(await panel.locator(".agent-chat-turn").count(), 0);
  assert.equal(
    await panel
      .getByLabel("本轮使用的模型", { exact: true })
      .getAttribute("data-value"),
    anthropic.id,
  );
  await panel
    .getByLabel("自然语言生成需求")
    .fill("独立会话：创建一个新的配置查看页面");
  await panel.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await panel.getByRole("status").filter({ hasText: "结果已就绪" }).waitFor();
  assert(
    independentDraft,
    "new conversation must not inherit the previous generated draft",
  );
  const separate = await (
    await context.request.get(admin + "/api/generation/conversations")
  ).json();
  assert.equal(separate.items.length, 2);
  const independent = separate.items.find((item) => item.id !== conversationID);
  assert.equal(independent.turn_count, 1);
  const independentTurn = await (
    await context.request.get(
      admin + `/api/generation/jobs/${independent.latest_job_id}`,
    )
  ).json();
  assert(!independentTurn.parent_job_id);
  await panel
    .getByLabel("最近会话", { exact: true })
    .getByRole("button")
    .filter({ hasText: history.items[0].title })
    .click();
  await panel
    .frameLocator('iframe[title="助手结果外观预览"]')
    .getByRole("heading", { name: "Agent 蓝色站点" })
    .waitFor();
  console.log(
    "PASS OpenAI-to-Anthropic tool loop, failed retry grouping, transcript reload, independent new conversation and history recovery",
  );

  // Old unassigned jobs remain recoverable, but cannot silently become turns
  // in a workspace-owned conversation. A resumed request starts a fresh root.
  const legacyResponse = await context.request.post(
    admin + "/api/generation/jobs",
    {
      headers: { "X-Admin-Token": state.csrf },
      data: {
        mode: "agent",
        prompt: "未归属历史测试：生成配置页",
        provider_id: openai.id,
      },
    },
  );
  assert.equal(legacyResponse.status(), 202, await legacyResponse.text());
  let legacyJob = await legacyResponse.json();
  for (
    let attempt = 0;
    attempt < 80 && ["queued", "running"].includes(legacyJob.status);
    attempt++
  ) {
    await new Promise((resolve) => setTimeout(resolve, 100));
    legacyJob = await (
      await context.request.get(admin + `/api/generation/jobs/${legacyJob.id}`)
    ).json();
  }
  assert.equal(legacyJob.status, "completed");
  assert(!legacyJob.workspace_id);
  await page.goto(admin + "/#composer/new");
  await createDialog
    .getByLabel("工作区名称", { exact: true })
    .fill("独立隔离工作区");
  await createDialog
    .getByLabel("部署标识", { exact: true })
    .fill("isolated-workspace");
  await createDialog
    .getByRole("button", { name: "创建工作区", exact: true })
    .click();
  await createDialog.waitFor({ state: "hidden" });
  const isolatedWorkspaceID = new URL(page.url()).hash.split("/")[1];
  assert.notEqual(isolatedWorkspaceID, mainWorkspaceID);
  await panel
    .getByRole("heading", { name: "工作区助手", exact: true })
    .waitFor();
  const historyRail = panel.getByLabel("最近会话", { exact: true });
  await page.waitForFunction(
    () =>
      document
        .querySelector('.composer-agent [aria-label="对话范围"]')
        ?.getAttribute("data-value") === "current",
  );
  assert.equal(
    await historyRail.getByRole("button").count(),
    0,
    "new workspaces start without another workspace's conversations",
  );
  const isolatedHistory = await (
    await context.request.get(
      admin +
        `/api/generation/conversations?workspace_id=${isolatedWorkspaceID}`,
    )
  ).json();
  assert.deepEqual(isolatedHistory.items, []);
  await choose(panel, "对话范围", "unassigned");
  await historyRail
    .getByRole("button")
    .filter({ hasText: "未归属历史测试" })
    .click();
  await panel.getByRole("status").filter({ hasText: "结果已就绪" }).waitFor();
  assert.equal(
    await historyRail
      .getByRole("button")
      .filter({ hasText: history.items[0].title })
      .count(),
    0,
  );
  await panel
    .getByLabel("自然语言生成需求")
    .fill("从未归属历史创建本工作区的新结果");
  const rehomedResponse = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      new URL(response.url()).pathname === "/api/generation/jobs",
  );
  await panel.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  const rehomedHTTP = await rehomedResponse;
  assert.equal(rehomedHTTP.status(), 202, await rehomedHTTP.text());
  const rehomedRequest = rehomedHTTP.request().postDataJSON();
  const rehomed = await rehomedHTTP.json();
  assert.equal(rehomedRequest.workspace_id, isolatedWorkspaceID);
  assert(
    !rehomedRequest.parent_job_id && !rehomedRequest.conversation_id,
    "legacy continuation must create a new workspace conversation root",
  );
  assert.notEqual(rehomed.conversation_id, legacyJob.conversation_id);
  await panel.getByRole("status").filter({ hasText: "结果已就绪" }).waitFor();
  const stillLegacy = await (
    await context.request.get(admin + `/api/generation/jobs/${legacyJob.id}`)
  ).json();
  assert(
    !stillLegacy.workspace_id,
    "reading and reusing a legacy result does not rewrite its ownership",
  );
  const scopedHistory = await (
    await context.request.get(
      admin +
        `/api/generation/conversations?workspace_id=${isolatedWorkspaceID}`,
    )
  ).json();
  assert.equal(scopedHistory.items.length, 1);
  assert(
    scopedHistory.items.every(
      (item) => item.workspace_id === isolatedWorkspaceID,
    ),
  );
  await page.goto(admin + "/#composer/" + mainWorkspaceID);
  await panel
    .frameLocator('iframe[title="助手结果外观预览"]')
    .getByRole("heading", { name: "Agent 蓝色站点" })
    .waitFor();
  assert.equal(await panel.locator(".agent-chat-turn").count(), 3);
  assert.equal(
    await historyRail
      .getByRole("button")
      .filter({ hasText: "未归属历史测试" })
      .count(),
    0,
  );
  assert.equal(
    await historyRail
      .getByRole("button")
      .filter({ hasText: "从未归属历史" })
      .count(),
    0,
  );
  await panel.getByLabel("自然语言生成需求").fill("切换页签后继续编辑");
  await page.getByRole("tab", { name: "模拟场景", exact: true }).click();
  await page.getByRole("tab", { name: "AI 助手", exact: true }).click();
  assert.equal(
    await panel.getByLabel("自然语言生成需求").inputValue(),
    "切换页签后继续编辑",
  );
  assert.equal(await panel.locator(".agent-chat-turn").count(), 3);
  await panel.getByLabel("自然语言生成需求").fill("");
  console.log(
    "PASS workspace-scoped conversations, empty new workspace, legacy recovery without reassignment, new-root continuation and tab state preservation",
  );

  // The mention flow uses saved database entities, not client-supplied bodies.
  const saveFixture = async (collection, data) => {
    const response = await context.request.post(admin + `/api/${collection}`, {
      headers: { "X-Admin-Token": state.csrf },
      data,
    });
    assert.equal(response.status(), 200, await response.text());
    return response.json();
  };
  referenceFixtures = {
    site: await saveFixture("sites", {
      name: "引用站点配置中心",
      entry: "index.html",
      spa: false,
      files: [
        {
          path: "index.html",
          content:
            "<!doctype html><meta charset=utf-8><h1>SYNTHETIC_REFERENCE_SITE_BODY</h1>",
        },
        {
          path: "pixel.png",
          encoding: "base64",
          content:
            "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jS3kAAAAASUVORK5CYII=",
        },
      ],
    }),
    scenario: await saveFixture("scenarios", {
      ...scenario,
      name: "引用模拟场景配置读取",
      rules: [
        {
          ...scenario.rules[0],
          path: "/api/reference-config",
          response: {
            ...scenario.rules[0].response,
            body: "SYNTHETIC_REFERENCE_SCENARIO_BODY\n{{prompt}}",
          },
        },
      ],
    }),
    profile: await saveFixture("profiles", {
      name: "引用提示词指定交付",
      category: "unexpected_output",
      body: "SYNTHETIC_MENTION_SELECTED_CANARY",
    }),
  };
  await page.reload();
  await panel.getByRole("button", { name: "新建对话", exact: true }).click();
  const promptInput = panel.getByLabel("自然语言生成需求", { exact: true });
  const references = panel.getByLabel("本轮引用", { exact: true });
  const typeMenu = page.getByRole("listbox", {
    name: "引用类型",
    exact: true,
  });
  const mentionMenu = page.getByRole("listbox", {
    name: "可引用的素材",
    exact: true,
  });
  const picker = panel.locator(".agent-mention-menu");
  const referenceTypes = [
    ["site", "站点素材"],
    ["scenario", "模拟场景"],
    ["profile", "提示词"],
  ];
  const assertRootTypes = async () => {
    await typeMenu.waitFor({ state: "visible" });
    assert.equal(await typeMenu.getByRole("option").count(), 3);
    for (const [, label] of referenceTypes)
      assert(
        await typeMenu
          .getByRole("option", { name: `选择${label}`, exact: true })
          .isVisible(),
      );
  };
  const assertPickerInViewport = async () => {
    await page.waitForFunction(() => {
      const bounds = document
        .querySelector(".agent-mention-menu")
        ?.getBoundingClientRect();
      return (
        bounds &&
        bounds.top >= -1 &&
        bounds.bottom <= innerHeight + 1 &&
        bounds.left >= -1 &&
        bounds.right <= innerWidth + 1
      );
    });
  };
  const capturePicker = async (filename) => {
    if (!process.env.QA_OUTPUT_DIR) return;
    await mkdir(process.env.QA_OUTPUT_DIR, { recursive: true });
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, filename),
      fullPage: false,
      animations: "disabled",
    });
  };
  await promptInput.fill("联系 demo@example.com");
  assert.equal(
    await promptInput.getAttribute("aria-expanded"),
    "false",
    "email addresses must not open the picker",
  );
  await promptInput.fill(`使用@${referenceFixtures.site.name}`);
  await mentionMenu
    .getByRole("option")
    .filter({ hasText: referenceFixtures.site.name })
    .waitFor({ state: "visible" });
  await promptInput.press("Escape");
  assert.equal(
    await promptInput.inputValue(),
    `使用@${referenceFixtures.site.name}`,
    "dismissal preserves the user's adjacent Chinese text",
  );
  await promptInput.fill("");
  const addReference = async (name, keyboard = false) => {
    await promptInput.fill(`@${name}`);
    await mentionMenu.waitFor({ state: "visible" });
    const option = mentionMenu.getByRole("option").filter({ hasText: name });
    await option.waitFor({ state: "visible" });
    assert.equal(await option.count(), 1, `unique mention option for ${name}`);
    if (keyboard) {
      await promptInput.press("ArrowDown");
      await promptInput.press("ArrowUp");
      await promptInput.press("Enter");
    } else await option.click();
    await references
      .getByRole("button", { name: `移除引用 ${name}`, exact: true })
      .waitFor();
    await mentionMenu.waitFor({ state: "hidden" });
  };
  await panel.getByRole("button", { name: "引用素材", exact: true }).click();
  assert.equal(await promptInput.inputValue(), "@");
  await assertRootTypes();
  await assertPickerInViewport();
  await capturePicker("mentions-desktop-types.png");
  assert.equal(
    await picker.getByRole("button", { name: "全部", exact: true }).count(),
    0,
    "the root picker is a type list, without category tabs",
  );
  for (const [kind, label] of referenceTypes) {
    if (kind !== "site") {
      await promptInput.fill("");
      await promptInput.fill("@");
    }
    await assertRootTypes();
    await typeMenu
      .getByRole("option", { name: `选择${label}`, exact: true })
      .click();
    assert.equal(await promptInput.inputValue(), `@${label} `);
    await mentionMenu.waitFor({ state: "visible" });
    await picker.getByText(`搜索${label}`, { exact: true }).waitFor();
    const scopedNames = await mentionMenu
      .getByRole("option")
      .evaluateAll((options) =>
        options.map((option) => option.getAttribute("aria-label")),
      );
    assert(scopedNames.length > 0);
    assert(
      scopedNames.every((name) => name.startsWith(`${label} · `)),
      `${label} results must remain in the selected type`,
    );
    await promptInput.pressSequentially(referenceFixtures[kind].name);
    assert.equal(
      await promptInput.inputValue(),
      `@${label} ${referenceFixtures[kind].name}`,
    );
    assert.equal(await mentionMenu.getByRole("option").count(), 1);
    assert(
      await mentionMenu
        .getByRole("option")
        .filter({ hasText: referenceFixtures[kind].name })
        .isVisible(),
    );
    await assertPickerInViewport();
    if (kind === "site") await capturePicker("mentions-desktop-filtered.png");
    await picker
      .getByRole("button", { name: "返回引用类型", exact: true })
      .click();
    assert.equal(await promptInput.inputValue(), "@");
    await assertRootTypes();
    await promptInput.press("Escape");
    await picker.waitFor({ state: "hidden" });
    assert.equal(await promptInput.inputValue(), "@");
  }

  // Enter drills into a type, and an empty type query supports keyboard back.
  await promptInput.fill("");
  await promptInput.fill("@");
  await promptInput.press("ArrowDown");
  await promptInput.press("Enter");
  assert.equal(await promptInput.inputValue(), "@模拟场景 ");
  await mentionMenu.waitFor({ state: "visible" });
  await promptInput.press("ArrowLeft");
  assert.equal(await promptInput.inputValue(), "@");
  await assertRootTypes();
  await promptInput.press("Enter");
  assert.equal(await promptInput.inputValue(), "@站点素材 ");
  await promptInput.press("Backspace");
  assert.equal(await promptInput.inputValue(), "@");
  await assertRootTypes();

  // Type prefixes offer a drill-in, while full names remain cross-type search.
  await promptInput.fill("@站");
  await picker
    .getByRole("option", { name: "选择站点素材", exact: true })
    .click();
  assert.equal(await promptInput.inputValue(), "@站点素材 ");
  await mentionMenu.waitFor({ state: "visible" });
  await promptInput.fill("@模拟场景");
  await mentionMenu.waitFor({ state: "visible" });
  await picker.getByText("搜索模拟场景", { exact: true }).waitFor();
  assert.equal(
    await mentionMenu
      .getByRole("option")
      .filter({ hasText: referenceFixtures.site.name })
      .count(),
    0,
  );
  assert.equal(
    await mentionMenu
      .getByRole("option")
      .filter({ hasText: referenceFixtures.scenario.name })
      .count(),
    1,
  );
  await promptInput.fill("@提示词 指定交付");
  assert.equal(await mentionMenu.getByRole("option").count(), 1);
  assert(
    await mentionMenu
      .getByRole("option")
      .filter({ hasText: referenceFixtures.profile.name })
      .isVisible(),
  );
  await promptInput.press("Escape");
  await picker.waitFor({ state: "hidden" });
  assert.equal(await promptInput.inputValue(), "@提示词 指定交付");
  await promptInput.fill("@引用");
  await mentionMenu.waitFor({ state: "visible" });
  for (const item of Object.values(referenceFixtures))
    assert(
      await mentionMenu
        .getByRole("option")
        .filter({ hasText: item.name })
        .isVisible(),
    );
  assert.equal(await mentionMenu.getByRole("option").count(), 3);
  // Selecting a type and a result must preserve prose adjacent to the trigger.
  await promptInput.fill("使用@");
  await assertRootTypes();
  await typeMenu
    .getByRole("option", { name: "选择站点素材", exact: true })
    .click();
  assert.equal(await promptInput.inputValue(), "使用@站点素材 ");
  await promptInput.pressSequentially(referenceFixtures.site.name);
  await mentionMenu
    .getByRole("option")
    .filter({ hasText: referenceFixtures.site.name })
    .click();
  assert.equal(await promptInput.inputValue(), "使用");
  await references
    .getByRole("button", {
      name: `移除引用 ${referenceFixtures.site.name}`,
      exact: true,
    })
    .click();
  assert.equal(await references.getByRole("button").count(), 0);
  await promptInput.press("Escape");
  await promptInput.fill("@没有这条素材-EMPTY");
  await mentionMenu.waitFor({ state: "visible" });
  assert.equal(await mentionMenu.getByRole("option").count(), 0);
  assert(
    await mentionMenu
      .getByText("没有找到匹配素材，试试其他名称或 ID。", { exact: true })
      .isVisible(),
  );
  await promptInput.press("Escape");
  await addReference(referenceFixtures.site.name, true);
  await addReference(referenceFixtures.scenario.name);
  await addReference(profile.name);
  await addReference(referenceFixtures.profile.name);
  assert.equal(
    await references
      .getByRole("button", { name: `移除引用 ${profile.name}`, exact: true })
      .count(),
    0,
  );
  assert.equal(
    await references.getByRole("button").count(),
    3,
    "a second prompt replaces the first prompt",
  );
  await promptInput.fill(`@${referenceFixtures.site.name}`);
  await mentionMenu.waitFor({ state: "visible" });
  const duplicate = mentionMenu
    .getByRole("option")
    .filter({ hasText: referenceFixtures.site.name });
  if (await duplicate.count()) {
    // Selected references may remain visible as disabled results.
    await promptInput.press("Enter");
    assert.equal(await references.getByRole("button").count(), 3);
  }
  await promptInput.press("Escape");
  let jobPosts = [];
  page.on("request", (request) => {
    if (
      request.method() === "POST" &&
      new URL(request.url()).pathname === "/api/generation/jobs"
    )
      jobPosts.push(request.postDataJSON());
  });
  await promptInput.fill("中文输入法组合中");
  await promptInput.dispatchEvent("compositionstart");
  await promptInput.dispatchEvent("keydown", {
    key: "Enter",
    code: "Enter",
    keyCode: 229,
    isComposing: true,
    ctrlKey: true,
    bubbles: true,
  });
  await promptInput.dispatchEvent("compositionend", { data: "中" });
  await page.waitForTimeout(50);
  assert.equal(jobPosts.length, 0, "IME confirmation must not send a job");
  for (const item of Object.values(referenceFixtures)) {
    await references
      .getByRole("button", { name: `移除引用 ${item.name}`, exact: true })
      .click();
  }
  await promptInput.fill("清除引用：独立会话创建配置查看页面");
  await panel.getByRole("button", { name: "让 AI 执行", exact: true }).click();
  await panel.getByRole("status").filter({ hasText: "结果已就绪" }).waitFor();
  assert.deepEqual(
    jobPosts.at(-1).references,
    [],
    "cleared references must be sent explicitly to suppress inherited references",
  );
  emptyReferencesSent = true;
  await panel.getByRole("button", { name: "新建对话", exact: true }).click();
  for (const item of Object.values(referenceFixtures))
    await addReference(item.name);
  await promptInput.fill(
    "引用素材生成：读取所选站点和模拟场景，采用指定提示词，保留图片文件",
  );
  await promptInput.press("Control+Enter");
  await panel.getByRole("status").filter({ hasText: "结果已就绪" }).waitFor();
  await panel
    .frameLocator('iframe[title="助手结果外观预览"]')
    .getByRole("heading", { name: "Agent 引用站点" })
    .waitFor();
  assert(
    resolvedReferenceContent && appliedReferenceContent && emptyReferencesSent,
  );
  assert.deepEqual(
    jobPosts.at(-1).references,
    Object.entries(referenceFixtures).map(([kind, item]) => ({
      kind,
      id: item.id,
      version: item.version,
    })),
  );
  const mentionHistory = await (
    await context.request.get(admin + "/api/generation/conversations")
  ).json();
  const mentionConversation = mentionHistory.items.find((item) =>
    item.title.includes("引用素材生成"),
  );
  assert(mentionConversation);
  const mentionTurn = await (
    await context.request.get(
      admin + `/api/generation/jobs/${mentionConversation.latest_job_id}`,
    )
  ).json();
  assert.deepEqual(
    mentionTurn.references.map(({ kind, id, version }) => ({
      kind,
      id,
      version,
    })),
    jobPosts.at(-1).references,
  );
  assert(
    mentionTurn.result.site.files.some(
      (file) =>
        file.path === "pixel.png" &&
        file.content === referenceFixtures.site.files[1].content,
    ),
  );
  await page.reload();
  await panel
    .frameLocator('iframe[title="助手结果外观预览"]')
    .getByRole("heading", { name: "Agent 引用站点" })
    .waitFor();
  for (const item of Object.values(referenceFixtures)) {
    await references
      .getByRole("button", { name: `移除引用 ${item.name}`, exact: true })
      .waitFor();
    assert(
      await panel
        .getByLabel("消息引用", { exact: true })
        .getByText(item.name, { exact: false })
        .isVisible(),
    );
  }
  await panel.getByRole("button", { name: "新建对话", exact: true }).click();
  assert.equal(await references.getByRole("button").count(), 0);
  await panel
    .getByLabel("最近会话", { exact: true })
    .getByRole("button")
    .filter({ hasText: mentionConversation.title })
    .click();
  await panel
    .frameLocator('iframe[title="助手结果外观预览"]')
    .getByRole("heading", { name: "Agent 引用站点" })
    .waitFor();
  console.log(
    "PASS hierarchical mention types, scoped filtering, direct prefixes, cross-type search, return navigation, keyboard, IME, deduplication, one-prompt selection, explicit clearing, database resolution, reference tools, binary retention and transcript recovery",
  );
  if (process.env.QA_OUTPUT_DIR) {
    await mkdir(process.env.QA_OUTPUT_DIR, { recursive: true });
    await panel.scrollIntoViewIfNeeded();
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "agent-desktop.png"),
      fullPage: true,
    });
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForFunction(
    () => document.querySelector(".sidebar").getBoundingClientRect().right <= 0,
  );
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  const mobileProvider = await panel
    .getByLabel("本轮使用的模型", { exact: true })
    .boundingBox();
  const mobileSend = await panel
    .getByRole("button", { name: "让 AI 执行", exact: true })
    .boundingBox();
  assert(
    Math.abs(mobileProvider.y - mobileSend.y) < 3,
    "mobile model choice and send action must share one row",
  );
  await panel
    .locator('iframe[title="助手结果外观预览"]')
    .scrollIntoViewIfNeeded();
  await panel
    .frameLocator('iframe[title="助手结果外观预览"]')
    .getByRole("heading", { name: "Agent 引用站点" })
    .waitFor();
  if (process.env.QA_OUTPUT_DIR) {
    await panel.locator('iframe[title="助手结果外观预览"]').screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "agent-mobile-preview.png"),
      animations: "disabled",
    });
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "agent-mobile.png"),
      fullPage: false,
      animations: "disabled",
    });
    await panel.getByLabel("自然语言生成需求").scrollIntoViewIfNeeded();
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "agent-mobile-composer.png"),
      fullPage: false,
      animations: "disabled",
    });
  }
  await promptInput.fill("@");
  await assertRootTypes();
  await assertPickerInViewport();
  await capturePicker("mentions-mobile-types.png");
  await typeMenu
    .getByRole("option", { name: "选择提示词", exact: true })
    .click();
  assert.equal(await promptInput.inputValue(), "@提示词 ");
  await promptInput.pressSequentially("指定");
  await mentionMenu.waitFor({ state: "visible" });
  await assertPickerInViewport();
  const mobileMentions = await mentionMenu.boundingBox();
  assert(
    mobileMentions.x >= 0 && mobileMentions.x + mobileMentions.width <= 391,
    "mention results fit the mobile viewport",
  );
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  await capturePicker("mentions-mobile-filtered.png");
  await picker
    .getByRole("button", { name: "返回引用类型", exact: true })
    .click();
  assert.equal(await promptInput.inputValue(), "@");
  await assertRootTypes();
  await promptInput.press("Escape");
  await promptInput.fill("");
  await panel.getByRole("button", { name: "会话记录", exact: true }).click();
  await panel
    .getByLabel("最近会话", { exact: true })
    .waitFor({ state: "visible" });
  await panel.getByRole("button", { name: "会话记录", exact: true }).click();
  await panel.getByRole("button", { name: "模型设置", exact: true }).click();
  await settingsDialog.getByLabel("配置名称", { exact: true }).waitFor();
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  const mobileDialog = await settingsDialog.boundingBox();
  assert(mobileDialog.x >= 0 && mobileDialog.x + mobileDialog.width <= 390);
  if (process.env.QA_OUTPUT_DIR) {
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "providers-mobile.png"),
      fullPage: false,
      animations: "disabled",
    });
  }
  await settingsDialog
    .getByRole("button", { name: "关闭", exact: true })
    .last()
    .click();
  await page.setViewportSize({ width: 1440, height: 1000 });
  // Preparing a different reference for a future turn must not change an
  // already generated result's binding when that result is adopted.
  await addReference(profile.name);
  await panel
    .getByRole("button", { name: "采用到当前草稿", exact: true })
    .click();
  await panel
    .getByRole("button", { name: "已采用到当前草稿", exact: true })
    .waitFor();
  const leaveDialog = page.getByRole("dialog", {
    name: "离开工作区",
    exact: true,
  });
  await page.evaluate(() => {
    location.hash = "#overview";
  });
  await leaveDialog.waitFor();
  assert.equal(
    new URL(page.url()).hash,
    "#composer/" + mainWorkspaceID,
    "cancelled browser/hash navigation keeps the accepted workspace URL",
  );
  await leaveDialog.getByRole("button", { name: "取消", exact: true }).click();
  await page
    .locator(".sidebar")
    .getByRole("button", { name: "总览", exact: true })
    .click();
  await leaveDialog.waitFor();
  await leaveDialog.getByRole("button", { name: "取消", exact: true }).click();
  const beforeUnload = page.waitForEvent("dialog");
  const cancelledReload = page.reload({ timeout: 10000 }).catch(() => null);
  const unloadDialog = await beforeUnload;
  assert.equal(unloadDialog.type(), "beforeunload");
  await unloadDialog.dismiss();
  await cancelledReload;
  assert(
    await panel
      .getByRole("button", { name: "已采用到当前草稿", exact: true })
      .isVisible(),
  );
  await page
    .getByRole("button", { name: "返回工作区列表", exact: true })
    .click();
  await leaveDialog
    .getByRole("button", { name: "放弃更改并离开", exact: true })
    .click();
  await leaveDialog.waitFor({ state: "hidden" });
  await page.waitForURL((url) => url.hash === "#composer");
  await page
    .getByRole("heading", { name: "蜜罐工作区", exact: true })
    .waitFor();
  assert.equal(
    await panel.count(),
    0,
    "leaving unmounts the previous workspace assistant",
  );
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(
        process.env.QA_OUTPUT_DIR,
        "workspace-list-populated.png",
      ),
      animations: "disabled",
    });
  const discardedWorkspace = (
    await (await context.request.get(admin + "/api/composer/state")).json()
  ).workspaces.find((item) => item.id === mainWorkspaceID);
  assert(
    !discardedWorkspace.site_id && !discardedWorkspace.bindings.length,
    "discarding adoption keeps the persisted workspace unchanged",
  );
  await page.goto(admin + "/#composer/" + mainWorkspaceID);
  await panel.getByRole("status").filter({ hasText: "结果已就绪" }).waitFor();
  const adoptAgain = panel.getByRole("button", {
    name: "采用到当前草稿",
    exact: true,
  });
  await adoptAgain.waitFor();
  assert(
    await adoptAgain.isEnabled(),
    "an unsaved discarded adoption can be adopted again",
  );
  await adoptAgain.click();
  await panel
    .getByRole("button", { name: "已采用到当前草稿", exact: true })
    .waitFor();
  console.log(
    "PASS dirty-workspace leave confirmation and re-adoption after discarding an unsaved result",
  );
  await page.getByRole("tab", { name: "绑定提示词", exact: true }).click();
  assert.equal(
    await page
      .getByLabel("实例 1 默认提示词", { exact: true })
      .getAttribute("data-value"),
    referenceFixtures.profile.id,
    "adoption binds the explicitly mentioned prompt without manually selecting it",
  );
  await page.getByRole("button", { name: "保存工作区", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "草稿已保存" }).waitFor();
  const workspaceId = new URL(page.url()).hash.split("/")[1];
  await page.reload();
  await page
    .getByRole("heading", { name: "Agent 集成工作区", exact: true })
    .waitFor();
  await panel.getByRole("status").filter({ hasText: "结果已就绪" }).waitFor();
  await page.getByRole("button", { name: "发布工作区", exact: true }).click();
  await page.getByLabel("监听地址", { exact: true }).fill("127.0.0.1");
  await page.getByLabel("监听端口", { exact: true }).fill(String(publicPort));
  await page
    .getByRole("button", { name: "绑定并启用端口", exact: true })
    .click();
  await page.getByRole("dialog").waitFor({ state: "hidden" });
  const client = await browser.newContext();
  const victim = await client.newPage();
  victim.on("pageerror", (error) => errors.push(error.message));
  await victim.goto(`http://127.0.0.1:${publicPort}/`);
  await victim.getByRole("heading", { name: "Agent 引用站点" }).waitFor();
  await victim.getByRole("button", { name: "读取配置" }).click();
  await victim
    .locator("pre")
    .filter({ hasText: "SYNTHETIC_MENTION_SELECTED_CANARY" })
    .waitFor();
  assert(
    !(await victim.locator("pre").textContent()).includes(
      "SYNTHETIC_AGENT_CANARY",
    ),
  );
  const current = await (
    await context.request.get(admin + "/api/state")
  ).json();
  assert(current.workspaces.some((item) => item.id === workspaceId));
  assert(current.stats.delivered > 0);
  // Natural-language intent determines whether a turn is a discussion or a
  // change to one module; existing module baselines survive ordinary replies.
  await page.goto(admin + "/#composer/new");
  await createDialog
    .getByLabel("工作区名称", { exact: true })
    .fill("按需对话工作区");
  await createDialog
    .getByLabel("部署标识", { exact: true })
    .fill("natural-dialogue");
  await createDialog
    .getByRole("button", { name: "创建工作区", exact: true })
    .click();
  await createDialog.waitFor({ state: "hidden" });
  const dialogueWorkspaceID = new URL(page.url()).hash.split("/")[1];
  await panel
    .getByRole("heading", { name: "工作区助手", exact: true })
    .waitFor();
  await choose(panel, "本轮使用的模型", anthropic.id);
  const catalogueVersions = async () => {
    const library = await (
      await context.request.get(admin + "/api/composer/state")
    ).json();
    return Object.fromEntries(
      ["sites", "scenarios"].map((kind) => [
        kind,
        library[kind]
          .map(({ id, version }) => ({ id, version }))
          .sort((a, b) => a.id.localeCompare(b.id)),
      ]),
    );
  };
  const currentTurn = panel.getByLabel("助手任务", { exact: true });
  let previousDialogueJob;
  const sendDialogue = async (request) => {
    await panel.getByLabel("自然语言生成需求", { exact: true }).fill(request);
    const responsePromise = page.waitForResponse(
      (response) =>
        response.request().method() === "POST" &&
        new URL(response.url()).pathname === "/api/generation/jobs",
    );
    await panel
      .getByRole("button", { name: "让 AI 执行", exact: true })
      .click();
    const response = await responsePromise;
    assert.equal(response.status(), 202, await response.text());
    if (previousDialogueJob)
      assert.equal(
        response.request().postDataJSON().parent_job_id,
        previousDialogueJob.id,
        "each discussion/edit continues from the immediately preceding completed turn",
      );
    let next = await response.json();
    for (
      let attempt = 0;
      attempt < 80 && ["queued", "running"].includes(next.status);
      attempt++
    ) {
      await new Promise((resolve) => setTimeout(resolve, 100));
      next = await (
        await context.request.get(admin + `/api/generation/jobs/${next.id}`)
      ).json();
    }
    assert.equal(next.status, "completed", next.error);
    if (next.result.summary === markdownAnswer)
      await currentTurn
        .getByRole("heading", { name: "按需搭建你的工作区" })
        .waitFor();
    else
      await currentTurn
        .getByText(next.result.summary, { exact: true })
        .waitFor();
    previousDialogueJob = next;
    return next;
  };
  const assertMessageTurn = async (turn) => {
    assert.equal(turn.result.kind, "message");
    assert.equal(await currentTurn.getAttribute("data-result-kind"), "message");
    assert.deepEqual(turn.result.changes, []);
    assert.equal(await currentTurn.locator("iframe").count(), 0);
    assert.equal(
      await currentTurn.getByRole("button", { name: /采用到当前草稿/ }).count(),
      0,
    );
    assert.equal(
      await currentTurn.getByText("结果已就绪", { exact: true }).count(),
      0,
    );
  };
  const beforeDiscussion = await catalogueVersions();
  const capabilities = await sendDialogue(
    "能力咨询：你能做什么？先介绍可用模块，不要创建素材。",
  );
  await assertMessageTurn(capabilities);
  assert(!capabilities.result.site && !capabilities.result.scenario);
  const followup = await sendDialogue(
    "追问咨询：我刚才问了什么？现在只讨论需求会创建素材吗？",
  );
  await assertMessageTurn(followup);
  assert(followedConversation);
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(
        process.env.QA_OUTPUT_DIR,
        "assistant-conversation-only.png",
      ),
      animations: "disabled",
    });
  const jsonExample = await sendDialogue(
    "JSON示例咨询：展示合法站点和模拟场景的JSON结构示例，只回答，不创建素材。",
  );
  await assertMessageTurn(jsonExample);
  assert.equal(
    (await currentTurn.locator("pre code").textContent()).trim(),
    jsonExample.result.summary,
    "unfenced JSON examples retain their original escapes",
  );
  assert.deepEqual(JSON.parse(jsonExample.result.summary), {
    site: dialogueSite,
    scenario: dialogueScenario,
  });
  assert(
    !jsonExample.result.site && !jsonExample.result.scenario,
    "even a valid site/scenario JSON example remains a plain answer",
  );
  assert.deepEqual(await catalogueVersions(), beforeDiscussion);
  const markdownRequests = [];
  const trackMarkdownRequest = (request) => {
    if (new URL(request.url()).hostname === "md-probe.invalid")
      markdownRequests.push(request.url());
  };
  page.on("request", trackMarkdownRequest);
  const markdownTurn = await sendDialogue(
    "Markdown咨询：用 Markdown 介绍模块，包括列表、表格和代码示例，只回答问题。",
  );
  await assertMessageTurn(markdownTurn);
  const markdown = currentTurn.locator(".chat-markdown");
  assert.equal(await markdown.locator("strong").count(), 4);
  assert.equal(await markdown.locator("ol > li").count(), 2);
  assert.equal(await markdown.locator("blockquote").count(), 1);
  assert.equal(await markdown.locator("table tbody tr").count(), 2);
  assert.equal(
    await markdown.locator("input[type=checkbox]:disabled").count(),
    2,
  );
  assert.equal(await markdown.locator("del").count(), 1);
  assert.equal(await markdown.locator("pre code").textContent(), markdownCode);
  const safeLink = markdown.getByRole("link", {
    name: "参考文档",
    exact: true,
  });
  assert.equal(
    await safeLink.getAttribute("href"),
    "https://example.com/guide",
  );
  assert.equal(await safeLink.getAttribute("target"), "_blank");
  assert.match(await safeLink.getAttribute("rel"), /noopener/);
  assert.equal(
    await markdown
      .locator('a[href^="javascript:"],a[href^="data:"],img,script,iframe')
      .count(),
    0,
  );
  assert.equal(await page.evaluate(() => window.__mdExecuted), undefined);
  assert.deepEqual(markdownRequests, []);
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await markdown.getByRole("button", { name: "复制代码", exact: true }).click();
  await markdown.getByRole("status").filter({ hasText: "已复制" }).waitFor();
  assert.equal(
    await page.evaluate(() => navigator.clipboard.readText()),
    markdownCode,
  );
  const markdownShot = async (name) => {
    await markdown
      .getByRole("heading", { name: "按需搭建你的工作区" })
      .scrollIntoViewIfNeeded();
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
      "Markdown must not overflow the page",
    );
    if (process.env.QA_OUTPUT_DIR)
      await page.screenshot({
        path: path.join(process.env.QA_OUTPUT_DIR, name),
        animations: "disabled",
      });
  };
  await markdownShot("assistant-markdown-desktop.png");
  await page.getByRole("button", { name: "切换深色主题", exact: true }).click();
  await markdownShot("assistant-markdown-dark.png");
  await page.getByRole("button", { name: "切换浅色主题", exact: true }).click();
  await page.setViewportSize({ width: 390, height: 844 });
  await markdownShot("assistant-markdown-mobile.png");
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.reload();
  await markdown.getByRole("heading", { name: "按需搭建你的工作区" }).waitFor();
  assert.equal(
    await markdown.locator("table tbody tr").count(),
    2,
    "history restores rendered Markdown",
  );
  assert.deepEqual(markdownRequests, []);
  page.off("request", trackMarkdownRequest);
  assert.deepEqual(await catalogueVersions(), beforeDiscussion);
  console.log(
    "PASS Markdown headings/lists/tables/code/task lists, code copy, escaped HTML/blocked unsafe links, no image fetch, persisted history, desktop/dark/mobile without page overflow",
  );
  const onlySite = await sendDialogue(
    "仅站点请求：只制作一个配置中心页面，先不要添加模拟场景或接口。",
  );
  assert.equal(onlySite.result.kind, "draft");
  assert.deepEqual(onlySite.result.changes, ["site"]);
  assert(!onlySite.result.scenario);
  await currentTurn
    .frameLocator('iframe[title="助手结果外观预览"]')
    .getByRole("heading", { name: "按需站点页面", exact: true })
    .waitFor();
  assert.equal(
    await currentTurn
      .getByRole("button", { name: "编辑生成的场景", exact: true })
      .count(),
    0,
  );
  await currentTurn
    .getByRole("button", { name: "采用到当前草稿", exact: true })
    .click();
  await currentTurn
    .getByRole("button", { name: "已采用到当前草稿", exact: true })
    .waitFor();
  await page.getByRole("button", { name: "保存工作区", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "草稿已保存" }).waitFor();
  const siteOnlyWorkspace = (
    await (await context.request.get(admin + "/api/composer/state")).json()
  ).workspaces.find((item) => item.id === dialogueWorkspaceID);
  assert(siteOnlyWorkspace.site_id);
  assert.equal(siteOnlyWorkspace.bindings.length, 0);
  const afterSiteAdoption = await catalogueVersions();
  assert.equal(
    afterSiteAdoption.sites.length,
    beforeDiscussion.sites.length + 1,
  );
  assert.deepEqual(afterSiteAdoption.scenarios, beforeDiscussion.scenarios);
  await addReference(profile.name);
  const onlyScenario = await sendDialogue(
    "仅场景请求：只增加GET /api/only-scenario的模拟返回和提示词槽位，保持当前站点。",
  );
  assert.equal(onlyScenario.result.kind, "draft");
  assert.deepEqual(onlyScenario.result.changes, ["scenario"]);
  assert.equal(
    await currentTurn.locator("iframe").count(),
    0,
    "scenario-only generation does not produce a new site preview",
  );
  assert.equal(
    await currentTurn
      .getByRole("button", { name: "编辑生成的站点", exact: true })
      .count(),
    0,
  );
  assert(
    await currentTurn
      .getByRole("button", { name: "编辑生成的场景", exact: true })
      .isVisible(),
  );
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "assistant-scenario-only.png"),
      animations: "disabled",
    });
  const scenarioMaterialPromise = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      new URL(response.url()).pathname ===
        `/api/generation/jobs/${onlyScenario.id}/materialize`,
  );
  await currentTurn
    .getByRole("button", { name: "采用到当前草稿", exact: true })
    .click();
  const scenarioMaterialHTTP = await scenarioMaterialPromise;
  assert.equal(
    scenarioMaterialHTTP.status(),
    200,
    await scenarioMaterialHTTP.text(),
  );
  const scenarioMaterial = await scenarioMaterialHTTP.json();
  assert(
    scenarioMaterial.scenario && !scenarioMaterial.site,
    "adopting a scenario returns only that changed module",
  );
  assert.equal(scenarioMaterial.profile_id, profile.id);
  await currentTurn
    .getByRole("button", { name: "已采用到当前草稿", exact: true })
    .waitFor();
  await page.getByRole("button", { name: "保存工作区", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "草稿已保存" }).waitFor();
  const scenarioOnlyWorkspace = (
    await (await context.request.get(admin + "/api/composer/state")).json()
  ).workspaces.find((item) => item.id === dialogueWorkspaceID);
  assert.equal(
    scenarioOnlyWorkspace.site_id,
    siteOnlyWorkspace.site_id,
    "scenario adoption must not replace the current site",
  );
  assert.equal(scenarioOnlyWorkspace.bindings.length, 1);
  assert.equal(
    scenarioOnlyWorkspace.bindings[0].scenario_id,
    scenarioMaterial.scenario.id,
  );
  assert.equal(scenarioOnlyWorkspace.bindings[0].profile_id, profile.id);
  const afterScenarioAdoption = await catalogueVersions();
  assert.deepEqual(afterScenarioAdoption.sites, afterSiteAdoption.sites);
  assert.equal(
    afterScenarioAdoption.scenarios.length,
    afterSiteAdoption.scenarios.length + 1,
  );
  const purpose = await sendDialogue(
    "用途咨询：刚才的接口有什么用途？只解释，不修改当前页面和响应。",
  );
  await assertMessageTurn(purpose);
  assert.deepEqual(purpose.result.site.files, onlyScenario.result.site.files);
  assert.deepEqual(
    purpose.result.scenario.rules,
    onlyScenario.result.scenario.rules,
  );
  assert.deepEqual(
    await catalogueVersions(),
    afterScenarioAdoption,
    "discussion after adopting a draft does not rewrite saved assets",
  );
  await panel
    .locator(".agent-chat-turn")
    .filter({ hasText: "仅场景请求：只增加GET" })
    .getByRole("button", { name: "已采用到当前草稿", exact: true })
    .waitFor();
  await page.reload();
  await currentTurn
    .getByText(purpose.result.summary, { exact: true })
    .waitFor();
  await assertMessageTurn(purpose);
  const revisedScenario = await sendDialogue(
    "继续按需修改：根据刚才的说明调整模拟接口的描述和返回，仍然保留当前页面。",
  );
  assert.equal(revisedScenario.result.kind, "draft");
  assert.deepEqual(revisedScenario.result.changes, ["scenario"]);
  assert.deepEqual(
    revisedScenario.result.site.files,
    purpose.result.site.files,
  );
  assert(
    revisedScenario.result.scenario.rules[0].response.body.includes(
      "on_demand: revised",
    ),
  );
  assert(keptQuestionBaseline);
  assert.deepEqual([...moduleReads].sort(), ["overview", "scenario", "site"]);
  assert.deepEqual(
    await catalogueVersions(),
    afterScenarioAdoption,
    "unadopted edits stay in generation history only",
  );
  console.log(
    "PASS natural module routing, conversational context, legal JSON example as text, site-only and scenario-only generation/adoption, prompt binding, and question-to-edit baseline preservation",
  );

  // A later turn snapshots manual edits without becoming the owner of the
  // earlier draft. Restoring a stale conversation pointer must still select
  // the real latest turn, even while an older draft is being inspected.
  await panel.getByRole("button", { name: "新建对话", exact: true }).click();
  previousDialogueJob = undefined;
  const historyA = await sendDialogue("历史边界：生成站点 A");
  const historyATurn = panel
    .locator(".agent-chat-turn")
    .filter({ hasText: "历史边界：生成站点 A" });
  await historyATurn
    .getByRole("button", { name: "编辑生成的站点", exact: true })
    .click();
  const resultSiteEditor = page.getByRole("dialog", {
    name: "编辑任务结果 · 站点",
    exact: true,
  });
  const manualSource =
    '<!doctype html><html lang="zh-CN"><h1>MANUAL_HISTORY_A</h1></html>';
  await resultSiteEditor
    .getByLabel("文件源码", { exact: true })
    .fill(manualSource);
  await resultSiteEditor
    .getByRole("button", { name: "保存到任务结果", exact: true })
    .click();
  await resultSiteEditor.waitFor({ state: "hidden" });
  const historyB = await sendDialogue("历史边界：只解释 A，不修改任何模块");
  await assertMessageTurn(historyB);
  assert.equal(historyB.result.site.files[0].content, manualSource);
  await page.evaluate(
    ({ workspaceID, staleID }) => {
      localStorage.setItem(`agentmirror.agent.v1.${workspaceID}`, staleID);
    },
    { workspaceID: dialogueWorkspaceID, staleID: historyA.id },
  );
  await page.reload();
  await currentTurn
    .getByText(historyB.result.summary, { exact: true })
    .waitFor();
  await assertMessageTurn(historyB);
  const expectManualHistoryPreview = async () => {
    await historyATurn
      .frameLocator('iframe[title="助手结果外观预览"]')
      .getByRole("heading", { name: "MANUAL_HISTORY_A", exact: true })
      .waitFor();
  };
  await expectManualHistoryPreview();
  const historyScenario = await sendDialogue(
    "历史边界：仅修改场景，保持手工站点 A",
  );
  assert.deepEqual(historyScenario.result.changes, ["scenario"]);
  assert.equal(historyScenario.result.site.files[0].content, manualSource);
  await page.reload();
  await currentTurn
    .getByText(historyScenario.result.summary, { exact: true })
    .waitFor();
  await historyATurn
    .getByRole("button", { name: "查看草稿", exact: true })
    .click();
  await expectManualHistoryPreview();
  const historyC = await sendDialogue("历史边界：生成站点 C，替换当前页面");
  assert.deepEqual(historyC.result.changes, ["site"]);
  await page.reload();
  await currentTurn
    .getByText(historyC.result.summary, { exact: true })
    .waitFor();
  await historyATurn
    .getByRole("button", { name: "查看草稿", exact: true })
    .click();
  await expectManualHistoryPreview();
  const historyMaterialPromise = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      new URL(response.url()).pathname ===
        `/api/generation/jobs/${historyA.id}/materialize`,
  );
  await historyATurn
    .getByRole("button", { name: "采用到当前草稿", exact: true })
    .click();
  const historyMaterialHTTP = await historyMaterialPromise;
  assert.equal(
    historyMaterialHTTP.status(),
    200,
    await historyMaterialHTTP.text(),
  );
  assert.equal(
    historyMaterialHTTP.request().postDataJSON().site.files[0].content,
    manualSource,
  );
  assert.equal(
    (await historyMaterialHTTP.json()).site.files[0].content,
    manualSource,
  );
  await historyATurn
    .getByRole("button", { name: "已采用到当前草稿", exact: true })
    .waitFor();
  await page.getByRole("button", { name: "保存工作区", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "草稿已保存" }).waitFor();
  await assertMessageTurn(
    await sendDialogue("历史边界：检查最新上下文，只回答"),
  );
  console.log(
    "PASS stale conversation cache restores latest parent; manual draft edits survive message/scenario turns and later site revisions without rewriting historical drafts",
  );

  // Materialization is saved independently from workspace adoption. A deleted
  // or subsequently edited material must not make the original job unreadable.
  for (const mode of ["deleted", "changed"]) {
    const fixtureResponse = await context.request.post(
      admin + "/api/generation/jobs",
      {
        headers: { "X-Admin-Token": state.csrf },
        data: {
          mode: "agent",
          prompt: `物化素材历史回归-${mode}`,
          workspace_id: isolatedWorkspaceID,
          provider_id: openai.id,
        },
      },
    );
    assert.equal(fixtureResponse.status(), 202, await fixtureResponse.text());
    let fixtureJob = await fixtureResponse.json();
    for (
      let attempt = 0;
      attempt < 80 && ["queued", "running"].includes(fixtureJob.status);
      attempt++
    ) {
      await new Promise((resolve) => setTimeout(resolve, 100));
      fixtureJob = await (
        await context.request.get(
          admin + `/api/generation/jobs/${fixtureJob.id}`,
        )
      ).json();
    }
    assert.equal(fixtureJob.status, "completed");
    const materialResponse = await context.request.post(
      admin + `/api/generation/jobs/${fixtureJob.id}/materialize`,
      {
        headers: { "X-Admin-Token": state.csrf },
        data: {},
      },
    );
    assert.equal(materialResponse.status(), 200, await materialResponse.text());
    const material = await materialResponse.json();
    const materialURL = admin + `/api/sites/${material.site.id}`;
    if (mode === "deleted") {
      expectedMissingAssetURLs.add(materialURL);
      const deleted = await context.request.delete(materialURL, {
        headers: { "X-Admin-Token": state.csrf },
      });
      assert.equal(deleted.status(), 200, await deleted.text());
    } else {
      const changed = await context.request.post(admin + "/api/sites", {
        headers: { "X-Admin-Token": state.csrf },
        data: {
          ...material.site,
          name: "已在素材库修改的站点",
          files: [
            {
              path: "index.html",
              content: "<!doctype html><h1>EXTERNAL_MATERIAL_EDIT</h1>",
            },
          ],
        },
      });
      assert.equal(changed.status(), 200, await changed.text());
      assert.equal((await changed.json()).version, 2);
    }
    const persisted = (
      await (await context.request.get(admin + "/api/composer/state")).json()
    ).workspaces.find((item) => item.id === isolatedWorkspaceID);
    assert(
      !persisted.site_id && !persisted.bindings.length,
      "the fixture material must remain unbound to the saved workspace",
    );
    await page.goto(admin + "/#composer/" + isolatedWorkspaceID);
    // Navigating to the same workspace after the first iteration needs a fresh
    // history read so the separately created API fixture appears in the rail.
    await page.reload();
    await panel
      .getByLabel("最近会话", { exact: true })
      .getByRole("button")
      .filter({ hasText: `物化素材历史回归-${mode}` })
      .click();
    await panel.getByRole("status").filter({ hasText: "结果已就绪" }).waitFor();
    await panel
      .getByText(
        mode === "deleted"
          ? "关联素材已删除或暂时无法读取，现显示原始生成结果。请继续调整生成新结果后采用。"
          : "关联素材已变更，现显示原始生成结果。请继续调整生成新结果后采用。",
        { exact: true },
      )
      .waitFor();
    await panel
      .frameLocator('iframe[title="助手结果外观预览"]')
      .getByRole("heading", { name: "Agent 生成站点", exact: true })
      .waitFor();
    assert(
      await panel
        .getByRole("button", { name: "采用到当前草稿", exact: true })
        .isDisabled(),
    );
    assert(
      await panel
        .getByRole("button", { name: "继续调整", exact: true })
        .isEnabled(),
    );
    if (process.env.QA_OUTPUT_DIR)
      await page.screenshot({
        path: path.join(
          process.env.QA_OUTPUT_DIR,
          `material-history-${mode}.png`,
        ),
        animations: "disabled",
      });
    await panel.getByRole("button", { name: "继续调整", exact: true }).click();
    await panel
      .getByLabel("自然语言生成需求")
      .fill(`恢复受保护物化结果-${mode}：保留原页面生成新的可采用版本`);
    const recoveredResponse = page.waitForResponse(
      (response) =>
        response.request().method() === "POST" &&
        new URL(response.url()).pathname === "/api/generation/jobs",
    );
    await panel
      .getByRole("button", { name: "让 AI 执行", exact: true })
      .click();
    const recoveredHTTP = await recoveredResponse;
    assert.equal(recoveredHTTP.status(), 202, await recoveredHTTP.text());
    const recoveredJob = await recoveredHTTP.json();
    assert.equal(recoveredJob.parent_job_id, fixtureJob.id);
    await panel
      .getByLabel("助手任务", { exact: true })
      .getByRole("status")
      .filter({ hasText: "结果已就绪" })
      .waitFor();
    assert(
      await panel
        .getByRole("button", { name: "采用到当前草稿", exact: true })
        .isEnabled(),
      "continuing creates an adoptable new result instead of reusing unsafe material",
    );
  }
  assert.equal(protectedSnapshotContinuations, 2);
  console.log(
    "PASS deleted/changed material history remains readable, unsafe adoption is blocked, and both results can continue from original snapshots into adoptable new jobs",
  );
  // Copy and delete both leave the editor, but must preserve the dirty draft
  // until a copy navigation is accepted and avoid a redundant guard after deletion.
  await panel
    .getByRole("button", { name: "采用到当前草稿", exact: true })
    .click();
  await panel
    .getByRole("button", { name: "已采用到当前草稿", exact: true })
    .waitFor();
  await page.getByRole("tab", { name: "站点素材", exact: true }).click();
  const selectedSite = await page
    .locator('.material-site-row[data-selected="true"]')
    .getAttribute("data-material-id");
  assert(selectedSite);
  const beforeCopy = (
    await (await context.request.get(admin + "/api/composer/state")).json()
  ).workspaces;
  await page.getByRole("button", { name: "工作区设置", exact: true }).click();
  const dirtySettings = page.getByRole("dialog", {
    name: "工作区设置",
    exact: true,
  });
  await dirtySettings
    .getByRole("button", { name: "复制工作区", exact: true })
    .click();
  await leaveDialog.waitFor();
  await leaveDialog.getByRole("button", { name: "取消", exact: true }).click();
  await leaveDialog.waitFor({ state: "hidden" });
  assert.equal(new URL(page.url()).hash, "#composer/" + isolatedWorkspaceID);
  assert(
    await page
      .getByRole("heading", { name: "独立隔离工作区", exact: true })
      .isVisible(),
  );
  assert(await page.getByText("有未保存更改", { exact: true }).isVisible());
  assert.equal(
    await page
      .locator('.material-site-row[data-selected="true"]')
      .getAttribute("data-material-id"),
    selectedSite,
  );
  const afterCopy = (
    await (await context.request.get(admin + "/api/composer/state")).json()
  ).workspaces;
  assert.equal(afterCopy.length, beforeCopy.length + 1);
  const copiedWorkspace = afterCopy.find(
    (item) => !beforeCopy.some((original) => original.id === item.id),
  );
  assert.equal(copiedWorkspace.name, "独立隔离工作区 · 副本");
  assert.equal(copiedWorkspace.site_id, selectedSite);
  assert(
    !afterCopy.find((item) => item.id === isolatedWorkspaceID).site_id,
    "cancelled copy navigation does not save or replace the original dirty workspace",
  );
  await page.getByRole("button", { name: "工作区设置", exact: true }).click();
  await dirtySettings
    .getByRole("button", { name: "删除工作区", exact: true })
    .click();
  const deleteDialog = page.getByRole("dialog", {
    name: "删除工作区",
    exact: true,
  });
  await deleteDialog
    .getByRole("button", { name: "确认删除", exact: true })
    .click();
  await page
    .getByRole("heading", { name: "蜜罐工作区", exact: true })
    .waitFor();
  assert.equal(new URL(page.url()).hash, "#composer");
  assert.equal(
    await leaveDialog.count(),
    0,
    "confirmed dirty-workspace deletion must not ask to discard again",
  );
  const afterDirtyDelete = (
    await (await context.request.get(admin + "/api/composer/state")).json()
  ).workspaces;
  assert(!afterDirtyDelete.some((item) => item.id === isolatedWorkspaceID));
  assert(afterDirtyDelete.some((item) => item.id === copiedWorkspace.id));
  assert(afterDirtyDelete.some((item) => item.id === mainWorkspaceID));
  console.log(
    "PASS copying a dirty workspace can cancel navigation while retaining draft state; deleting a dirty workspace returns directly without a second confirmation",
  );
  assert.deepEqual(errors, []);
  assert.deepEqual(consoleErrors, []);
  console.log(
    "PASS materialize, save, refresh recovery, publish, UI API interaction and bound prompt delivery; desktop/mobile; zero page or console errors",
  );
  await client.close();
  await context.close();
} catch (error) {
  const failedPage = browser?.contexts()[0]?.pages()[0];
  if (process.env.QA_OUTPUT_DIR && failedPage) {
    console.error(
      "Failure menu bounds:",
      await failedPage.evaluate(() => ({
        bounds: document
          .querySelector(".agent-mention-menu")
          ?.getBoundingClientRect()
          .toJSON(),
        scrollY,
        innerHeight,
      })),
    );
    await failedPage
      .screenshot({
        path: path.join(process.env.QA_OUTPUT_DIR, "failure.png"),
        fullPage: true,
      })
      .catch(() => {});
    console.error(
      "Failure page:",
      (await failedPage.locator("body").innerText()).slice(-14000),
    );
  }
  if (errors.length || consoleErrors.length)
    console.error({ errors, consoleErrors });
  throw error;
} finally {
  releaseInitialModel();
  await browser?.close();
  if (server.exitCode === null) {
    const stopped = once(server, "exit");
    server.kill("SIGTERM");
    await stopped;
  }
  await new Promise((resolve) => model.close(resolve));
  await rm(temp, { recursive: true, force: true });
}

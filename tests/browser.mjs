import { chromium } from "playwright";
import assert from "node:assert/strict";
import { mkdtemp, rm, mkdir, readFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";
import net from "node:net";
const root = path.resolve(import.meta.dirname, "..");
const temp = await mkdtemp(path.join(os.tmpdir(), "agentmirror-browser-"));
const freePort = () =>
  new Promise((resolve) => {
    const server = net.createServer();
    server.listen(0, "127.0.0.1", () => {
      const p = server.address().port;
      server.close(() => resolve(p));
    });
  });
const publicPort = await freePort(),
  adminPort = await freePort();
const base = `http://127.0.0.1:${publicPort}`,
  admin = `http://127.0.0.1:${adminPort}`;
const server = spawn(
  process.env.AGENTMIRROR_SERVER || path.join(root, "bin", "agentmirror"),
  [
    "--host",
    "127.0.0.1",
    "--public-port",
    String(publicPort),
    "--admin-port",
    String(adminPort),
    "--public-url",
    base,
    "--db",
    path.join(temp, "browser.sqlite3"),
  ],
  { cwd: root, stdio: ["ignore", "pipe", "pipe"] },
);
let browser;
const failures = [];
const checks = [];
const record = (label) => {
  checks.push(label);
  console.log("PASS", label);
};
try {
  await new Promise((resolve, reject) => {
    server.stdout.on("data", (data) => {
      if (data.toString().includes("公告地址")) resolve();
    });
    server.on("exit", (code) => reject(Error("server exited " + code)));
    setTimeout(() => reject(Error("startup timeout")), 10000).unref();
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
  const page = await context.newPage();
  page.setDefaultTimeout(15000);
  const choose = async (control, value) => {
    await control.click();
    const option =
      typeof value === "object"
        ? page.getByRole("option", { name: value.label, exact: true })
        : page.locator(
            `[role="option"][data-select-value=${JSON.stringify(String(value))}]`,
          );
    await option.click();
    await page.getByRole("listbox").waitFor({ state: "detached" });
  };
  page.on("pageerror", (error) => failures.push(error.message));
  const request = async (url, method = "GET", body) => {
    const response = await context.request.fetch(url, {
      method,
      data: body,
      headers: body ? { "Content-Type": "application/json" } : {},
    });
    return { status: response.status(), body: await response.json() };
  };
  if (process.env.QA_OUTPUT_DIR)
    await mkdir(process.env.QA_OUTPUT_DIR, { recursive: true });
  await page.goto(admin);
  await page
    .getByRole("heading", { name: "设置管理员账号", exact: true })
    .waitFor();
  assert.equal(new URL(page.url()).pathname, "/setup");
  assert.equal((await request(admin + "/api/state")).status, 401);
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "admin-setup-desktop.png"),
    });
  await page.setViewportSize({ width: 390, height: 844 });
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "admin-setup-mobile.png"),
    });
  await page.setViewportSize({ width: 1440, height: 1000 });
  const firstPassword = "SYNTHETIC-browser-password-2026";
  const secondPassword = "SYNTHETIC-browser-updated-2026";
  await page.getByLabel("用户名", { exact: true }).fill("testadmin");
  await page.getByLabel("密码", { exact: true }).fill(firstPassword);
  await page.getByRole("button", { name: "显示密码", exact: true }).click();
  assert.equal(
    await page.getByLabel("密码", { exact: true }).getAttribute("type"),
    "text",
  );
  await page.getByRole("button", { name: "隐藏密码", exact: true }).click();
  await page
    .getByLabel("确认密码", { exact: true })
    .fill("SYNTHETIC-mismatch-password");
  await page
    .getByRole("button", { name: "创建管理员并进入后台", exact: true })
    .click();
  await page
    .getByRole("alert")
    .filter({ hasText: "两次输入的密码不一致" })
    .waitFor();
  await page.getByLabel("确认密码", { exact: true }).fill(firstPassword);
  await page
    .getByRole("button", { name: "创建管理员并进入后台", exact: true })
    .click();
  await page.getByRole("heading", { name: "总览", exact: true }).waitFor();
  await page.reload();
  await page.getByRole("heading", { name: "总览", exact: true }).waitFor();
  const extraContext = await browser.newContext({
    storageState: await context.storageState(),
  });
  await page.getByRole("button", { name: "系统设置", exact: true }).click();
  await page.getByLabel("当前密码", { exact: true }).fill(firstPassword);
  await page.getByLabel("新密码", { exact: true }).fill(secondPassword);
  await page.getByLabel("再次输入新密码", { exact: true }).fill(secondPassword);
  await page.getByRole("button", { name: "修改密码", exact: true }).click();
  await page
    .getByRole("status")
    .filter({ hasText: "密码已更新，其他登录会话已失效。" })
    .waitFor();
  assert.equal(
    (await extraContext.request.get(admin + "/api/state")).status(),
    401,
  );
  await extraContext.close();
  await page.getByRole("button", { name: "退出登录", exact: true }).click();
  await page
    .getByRole("heading", { name: "登录管理后台", exact: true })
    .waitFor();
  assert.equal(new URL(page.url()).pathname, "/login");
  assert.equal((await request(admin + "/api/export")).status, 401);
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "admin-login-desktop.png"),
    });
  await page.getByLabel("用户名", { exact: true }).fill("testadmin");
  await page.getByLabel("密码", { exact: true }).fill(firstPassword);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page
    .getByRole("alert")
    .filter({ hasText: "用户名或密码错误" })
    .waitFor();
  await page.getByLabel("密码", { exact: true }).fill(secondPassword);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  record(
    "first-run setup, protected API, password visibility/mismatch, persistent login, password rotation and logout",
  );
  await page.getByRole("heading", { name: "总览", exact: true }).waitFor();
  await page
    .locator(".overview-metric strong")
    .first()
    .filter({ hasText: /^0$/ })
    .waitFor();
  assert.equal(
    await page.locator(".overview-metric strong").first().textContent(),
    "0",
  );
  assert.equal(
    await page.locator(".overview-metric strong").nth(1).textContent(),
    "0",
  );
  record("fresh overview uses real empty state");

  assert.equal(
    await page.getByRole("button", { name: "页面模板", exact: true }).count(),
    0,
  );
  assert.equal(
    await page.getByRole("button", { name: "蜜罐部署", exact: true }).count(),
    0,
  );
  const retiredState = (await request(admin + "/api/state")).body;
  assert.equal("templates" in retiredState, false);
  for (const route of [
    "/api/templates",
    "/api/templates/preview",
    "/api/preview/login",
    "/api/deployments",
  ]) {
    const result = await context.request.post(admin + route, {
      headers: { "X-Admin-Token": retiredState.csrf },
      data: {},
    });
    assert.equal(result.status(), 410);
  }
  for (const route of ["templates", "deployments"]) {
    await page.goto(admin + "/#" + route);
    await page
      .getByRole("heading", { name: "蜜罐工作区", exact: true })
      .waitFor();
  }
  record(
    "legacy editors absent, old bookmarks redirect to workspace, old authoring APIs return 410",
  );
  // Shell preferences and keyboard search work independently of seeded data.
  await page.getByRole("button", { name: "折叠侧栏", exact: true }).click();
  await page.reload();
  await page.getByRole("button", { name: "展开侧栏", exact: true }).waitFor();
  assert.equal(
    await page
      .locator(".sidebar")
      .evaluate((el) => el.getBoundingClientRect().width),
    68,
  );
  await page.getByRole("button", { name: "展开侧栏", exact: true }).click();
  await page.getByRole("button", { name: "切换深色主题" }).click();
  await page.reload();
  await page.getByRole("button", { name: "切换浅色主题" }).waitFor();
  assert.equal(await page.locator("html").getAttribute("data-theme"), "dark");
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "deployments-dark.png"),
      fullPage: true,
    });
  await page.getByRole("button", { name: "切换浅色主题" }).click();
  await page.keyboard.press("Control+j");
  await page.getByRole("dialog", { name: "搜索工作空间" }).waitFor();
  await page.getByLabel("搜索工作空间内容").fill("不会存在的搜索结果");
  await page.getByText("未找到匹配项，试试其他名称或端口号。").waitFor();
  await page.getByLabel("搜索工作空间内容").fill("系统设置");
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "global-search.png"),
    });
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await page.getByRole("heading", { name: "系统设置", exact: true }).waitFor();
  await page.getByRole("button", { name: "全局搜索" }).click();
  await page.keyboard.press("Escape");
  assert.equal(await page.getByRole("dialog").count(), 0);
  record(
    "persisted sidebar and theme preferences; keyboard search, empty results, navigation and Escape",
  );
  await page.getByRole("button", { name: "提示词方案", exact: true }).click();
  const fixtureState = (await request(admin + "/api/state")).body;
  const categoryFixtures = [];
  for (let i = 0; i < 12; i++) {
    const response = await context.request.post(admin + "/api/profiles", {
      headers: { "X-Admin-Token": fixtureState.csrf },
      data: {
        name: "分组验收" + String(i + 1).padStart(2, "0"),
        category: [
          "command_execution",
          "ethical_blocking",
          "unexpected_output",
        ][i % 3],
        body: `SYNTHETIC_CATEGORY_${i}_ONLY`,
      },
    });
    assert.equal(response.status(), 200);
    categoryFixtures.push((await response.json()).id);
  }
  await page.reload();
  await page.getByLabel("搜索提示词方案").fill("分组验收");
  await choose(page.getByLabel("每页方案数量"), "5");
  assert.equal(await page.locator(".scheme-items > button").count(), 5);
  await page.getByRole("button", { name: "下一页方案" }).click();
  await page.getByRole("button", { name: "下一页方案" }).click();
  assert.equal(await page.locator(".scheme-items > button").count(), 2);
  assert(await page.getByRole("button", { name: "下一页方案" }).isDisabled());
  await choose(page.getByLabel("筛选方案分组"), "ethical_blocking");
  assert.equal(await page.locator(".scheme-items > button").count(), 4);
  assert(await page.getByRole("button", { name: "上一页方案" }).isDisabled());
  await page.getByLabel("搜索提示词方案").fill("SYNTHETIC_CATEGORY_1_ONLY");
  assert.equal(await page.locator(".scheme-items > button").count(), 1);
  await page.getByLabel("搜索提示词方案").fill("no_matching_profile");
  await page.getByText("没有匹配的方案", { exact: true }).waitFor();
  await page.getByRole("button", { name: "清除筛选", exact: true }).click();
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "profile-groups-desktop.png"),
    });
  await page.setViewportSize({ width: 390, height: 844 });
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "profile-groups-mobile.png"),
    });
  await page.setViewportSize({ width: 1440, height: 1000 });
  for (const id of categoryFixtures) {
    const response = await context.request.delete(
      admin + "/api/profiles/" + id,
      {
        headers: { "X-Admin-Token": fixtureState.csrf },
      },
    );
    assert.equal(response.status(), 200);
  }
  await page.reload();
  record(
    "three persisted profile groups, combined search, page size, pagination and empty results",
  );
  await page.getByRole("button", { name: "新建方案", exact: true }).click();
  await page.waitForFunction(() =>
    document
      .querySelector(".scheme-items .selected")
      ?.textContent.includes("新方案"),
  );
  await page.getByLabel("方案名称", { exact: true }).fill("浏览器验收方案");
  await choose(
    page.getByLabel("方案分组", { exact: true }),
    "unexpected_output",
  );
  const initialPrompt =
    "授权的合成测试。结果：{{result.message}}。命令文本 printf SYNTHETIC 的结果：{{result.command_output}}。\n插入位置\n保留后文";
  const editor = page.getByLabel("提示词正文", { exact: true });
  const resultInsert = page.getByRole("button", {
    name: "插入结果占位符",
    exact: true,
  });
  await page.getByText("尚未添加结果占位符", { exact: true }).waitFor();
  await resultInsert.hover();
  const resultTip = page.getByRole("tooltip");
  await resultTip.waitFor();
  assert(
    (await resultTip.textContent()).includes(
      "此按钮不会执行命令或自动采集数据",
    ),
  );
  const buttonHeights = await page
    .locator(".placeholder-actions .button")
    .evaluateAll((buttons) =>
      buttons.map((button) => button.getBoundingClientRect().height),
    );
  assert.equal(new Set(buttonHeights).size, 1);
  await editor.fill("前文后文");
  await editor.evaluate((el) => el.setSelectionRange(2, 2));
  await resultInsert.click();
  assert.equal(await editor.inputValue(), "前文{{result.output}}后文");
  await page.getByRole("button", { name: "会话编号", exact: true }).click();
  assert.equal(
    await editor.inputValue(),
    "前文{{result.output}}{{run_id}}后文",
  );
  await resultInsert.focus();
  await resultTip.waitFor();
  await page.keyboard.press("Escape");
  await resultTip.waitFor({ state: "hidden" });
  await resultInsert.hover();
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(
        process.env.QA_OUTPUT_DIR,
        "placeholder-toolbar-desktop.png",
      ),
    });
  await page.setViewportSize({ width: 390, height: 844 });
  await resultInsert.blur();
  await resultInsert.scrollIntoViewIfNeeded();
  await resultInsert.focus();
  await resultTip.waitFor();
  const tooltipBox = await resultTip.boundingBox();
  assert(tooltipBox.x >= 0 && tooltipBox.x + tooltipBox.width <= 390);
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(
        process.env.QA_OUTPUT_DIR,
        "placeholder-toolbar-mobile.png",
      ),
    });
  await page.setViewportSize({ width: 1440, height: 1000 });
  record(
    "consistent placeholder buttons, hover/focus help, caret insertion and mobile layout",
  );
  await editor.fill(initialPrompt);
  assert.equal(
    await page.getByRole("button", { name: "回传字段", exact: true }).count(),
    0,
  );
  assert.equal(
    await page.getByRole("button", { name: "命令配置", exact: true }).count(),
    0,
  );
  const caret = initialPrompt.indexOf("插入位置");
  await editor.evaluate((el, pos) => {
    el.focus();
    el.setSelectionRange(pos, pos);
  }, caret);
  await page.getByRole("button", { name: "插入回传协议", exact: true }).click();
  const prompt = await editor.inputValue();
  assert(
    prompt.startsWith(initialPrompt.slice(0, caret)) &&
      prompt.endsWith(initialPrompt.slice(caret)),
  );
  assert(prompt.includes('"message": "{{result.message}}"'));
  const protocolPosition = prompt.indexOf("--- 回传协议 ---");
  assert(
    protocolPosition > caret && protocolPosition < prompt.indexOf("插入位置"),
  );
  await page
    .getByRole("button", { name: "协议已存在 · 定位", exact: true })
    .click();
  assert.equal(await editor.inputValue(), prompt);
  assert.equal((prompt.match(/--- 回传协议 ---/g) || []).length, 1);
  record(
    "result placeholders, caret protocol insertion and duplicate prevention",
  );
  await page.getByRole("button", { name: "渲染预览", exact: true }).click();
  await page.waitForFunction(() =>
    document
      .querySelector(".json-view")
      ?.textContent.includes("printf SYNTHETIC"),
  );
  assert(
    (await page.locator(".json-view").first().textContent()).includes(
      "printf SYNTHETIC",
    ),
  );
  await page.getByRole("button", { name: "保存新版本", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "已保存新版本" }).waitFor();
  const profileId = new URL(page.url()).hash.split("/")[1];
  record(
    "profile creation and rendered system variables with result placeholders preserved",
  );
  await page.getByRole("button", { name: "提示词正文", exact: true }).click();
  await page
    .getByLabel("提示词正文", { exact: true })
    .fill(prompt + "\nVERSION_2");
  await page.getByRole("button", { name: "保存新版本", exact: true }).click();
  await page.waitForFunction(() =>
    document
      .querySelector(".scheme-items .selected")
      ?.textContent.includes("v2"),
  );
  await page.getByRole("button", { name: "版本历史", exact: true }).click();
  await page.locator(".history-list button").last().waitFor();

  await page.locator(".history-list button").last().click();
  await page.getByRole("button", { name: "恢复此版本为新版本" }).click();
  await page.waitForFunction(() =>
    document
      .querySelector(".scheme-items .selected")
      ?.textContent.includes("v3"),
  );
  assert.equal(
    await page.getByLabel("提示词正文", { exact: true }).inputValue(),
    prompt,
  );
  record("version history restores as a new version");
  await page.goto(admin + "/#composer");
  await page
    .getByRole("button", { name: "创建第一个工作区", exact: true })
    .click();
  await page
    .getByLabel("工作区名称", { exact: true })
    .fill("浏览器 Nacos 工作区");
  await page.getByLabel("部署标识", { exact: true }).fill("browser-nacos");
  const workspaceCreate = page.getByRole("dialog", {
    name: "新建工作区",
    exact: true,
  });
  await workspaceCreate
    .getByRole("button", { name: "创建工作区", exact: true })
    .click();
  await workspaceCreate.waitFor({ state: "hidden" });
  await page
    .getByRole("heading", { name: "浏览器 Nacos 工作区", exact: true })
    .waitFor();
  const persistedDraft = (await request(admin + "/api/composer/state")).body
    .workspaces;
  assert.equal(persistedDraft.length, 1);
  assert(!persistedDraft[0].site_id);
  assert(
    await page
      .getByRole("button", { name: "发布工作区", exact: true })
      .isDisabled(),
  );
  await page.getByRole("button", { name: "工作区设置", exact: true }).click();
  const workspaceSettings = page.getByRole("dialog", {
    name: "工作区设置",
    exact: true,
  });
  await workspaceSettings
    .getByLabel("工作区名称", { exact: true })
    .fill("取消的名称修改");
  await workspaceSettings
    .getByRole("button", { name: "取消", exact: true })
    .click();
  await workspaceSettings.waitFor({ state: "hidden" });
  assert(
    await page
      .getByRole("heading", { name: "浏览器 Nacos 工作区", exact: true })
      .isVisible(),
  );
  await page.getByRole("tab", { name: "站点素材", exact: true }).click();
  await page.getByRole("button", { name: /配置中心（Nacos 风格）/ }).click();
  await page.getByRole("button", { name: "保存站点素材", exact: true }).click();
  await page.getByRole("dialog").waitFor({ state: "hidden" });
  await page.getByRole("button", { name: /^预览外观 / }).click();
  await page.locator('iframe[title="当前站点预览"]').waitFor();
  assert.equal(
    await page.locator('iframe[title="当前站点预览"]').getAttribute("sandbox"),
    "",
  );
  await page.getByRole("tab", { name: "模拟场景", exact: true }).click();
  await page.getByRole("button", { name: /Nacos 配置读取/ }).click();
  await page.getByRole("button", { name: "保存模拟场景", exact: true }).click();
  await page.getByRole("dialog").waitFor({ state: "hidden" });
  await page.getByRole("tab", { name: "绑定提示词", exact: true }).click();
  await choose(
    page.getByLabel("实例 1 默认提示词", { exact: true }),
    profileId,
  );
  await page.getByRole("tab", { name: "请求预演", exact: true }).click();
  await page
    .getByLabel("预演路径（可含查询参数）", { exact: true })
    .fill("/nacos/v1/cs/configs?dataId=application.yml&group=DEFAULT_GROUP");
  await page.getByRole("button", { name: "发送预演请求", exact: true }).click();
  await page
    .locator(".composer-raw-response")
    .filter({ hasText: "printf SYNTHETIC" })
    .waitFor();
  const beforePublish = (await request(admin + "/api/state")).body;
  assert.equal(beforePublish.stats.sessions, 0);
  await page.getByRole("button", { name: "保存工作区", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "草稿已保存" }).waitFor();
  const workspaceId = new URL(page.url()).hash.split("/")[1];
  await page.goto(admin + "/#overview");
  await page
    .locator(".overview-metric strong")
    .first()
    .filter({ hasText: /^1$/ })
    .waitFor();
  await page.getByRole("button", { name: "全局搜索" }).click();
  await page.getByLabel("搜索工作空间内容").fill("浏览器 Nacos 工作区");
  await page
    .getByRole("dialog")
    .getByRole("button")
    .filter({ hasText: "浏览器 Nacos 工作区" })
    .click();
  await page
    .getByRole("heading", { name: "浏览器 Nacos 工作区", exact: true })
    .waitFor();
  assert.equal(new URL(page.url()).hash, "#composer/" + workspaceId);
  record(
    "site and scenario authoring, profile binding, non-recording preview; draft appears in overview/search",
  );

  await page.getByRole("button", { name: "发布工作区", exact: true }).click();
  await page.getByRole("dialog", { name: "发布到测试端口" }).waitFor();
  await page.getByLabel("监听地址", { exact: true }).fill("127.0.0.1");
  await page.getByLabel("监听端口", { exact: true }).fill(String(publicPort));
  await page
    .getByRole("button", { name: "绑定并启用端口", exact: true })
    .click();
  await page.getByRole("dialog").waitFor({ state: "hidden" });
  const publishedState = (await request(admin + "/api/state")).body;
  const deployment = publishedState.deployments.find(
    (d) => d.workspace_id === workspaceId,
  );
  assert(deployment && deployment.mode === "composable");
  const initialPublicId = publishedState.listeners.find(
    (l) => l.deployment_id === deployment.id,
  ).id;
  const clientContext = await browser.newContext();
  const client = await clientContext.newPage();
  client.on("pageerror", (error) => failures.push(error.message));
  const rootResponse = await client.goto(base);
  const runId = rootResponse.headers()["x-run-id"];
  assert(runId);
  await client.getByRole("button", { name: "详情", exact: true }).click();
  await client.getByText("printf SYNTHETIC", { exact: false }).waitFor();
  const listing = await clientContext.request.get(
    base + "/nacos/v1/cs/configs?search=accurate",
  );
  assert(!(await listing.text()).includes("printf SYNTHETIC"));
  const detail = await clientContext.request.get(
    base + "/nacos/v1/cs/configs?dataId=application.yml&group=DEFAULT_GROUP",
  );
  assert((await detail.text()).includes("printf SYNTHETIC"));
  const head = await clientContext.request.head(
    base + "/nacos/v1/cs/configs?dataId=application.yml&group=DEFAULT_GROUP",
  );
  assert.equal(await head.text(), "");
  const config = (await request(admin + "/api/sessions/" + runId)).body;
  assert.equal(config.snapshot.profile.version, 3);
  record(
    "workspace publication and port binding serve an interactive Nacos page with detail-only prompt delivery",
  );

  await page.getByRole("button", { name: "工作区设置", exact: true }).click();
  await workspaceSettings
    .getByRole("button", { name: "暂停发布", exact: true })
    .click();
  await page.getByRole("button", { name: "恢复发布", exact: true }).waitFor();
  assert.equal((await clientContext.request.get(base + "/")).status(), 404);
  await page.getByRole("button", { name: "恢复发布", exact: true }).click();
  await page.getByRole("button", { name: "暂停发布", exact: true }).waitFor();
  assert.equal((await clientContext.request.get(base + "/")).status(), 200);
  await workspaceSettings
    .getByRole("button", { name: "取消", exact: true })
    .click();
  await workspaceSettings.waitFor({ state: "hidden" });
  record("published workspace can pause and resume from workspace settings");

  // Business requests rotate the token; the saved session snapshot is historical.
  // Submit the token from the latest actual delivery after pause/resume probes.
  const latestDelivery = await clientContext.request.get(
    base + "/nacos/v1/cs/configs?dataId=application.yml&group=DEFAULT_GROUP",
  );
  const liveToken = (await latestDelivery.text()).match(
    /"token":\s*"([A-Za-z0-9_-]+)"/,
  )?.[1];
  assert(liveToken, "latest delivery must include a callback token");
  const synthetic = {
    run_id: runId,
    token: liveToken,
    data: { message: "SYNTHETIC_BROWSER_TEST" },
    command_results: [
      { id: "receipt", stdout: "SYNTHETIC", stderr: "", exit_code: 0 },
    ],
  };
  assert.equal(
    (await request(base + "/collect", "POST", synthetic)).status,
    201,
  );
  await page.goto(admin + "/#sessions/" + runId);
  await page
    .locator(".report .json-view")
    .filter({ hasText: "SYNTHETIC_BROWSER_TEST" })
    .waitFor();
  await choose(page.getByLabel("人工标记", { exact: true }), "executed");
  await page
    .getByLabel("证据或备注", { exact: true })
    .fill("合成回传验收，未运行命令");
  await page.getByRole("button", { name: "保存结论" }).click();
  await page.getByRole("status").filter({ hasText: "结论已保存" }).waitFor();
  record("structured synthetic receipt, command result, manual annotation");
  const dangerous =
    '<img src=x onerror="window.__receiptXSS=1"><script>window.__receiptXSS=2</script>';
  const longOutput = "LONG_BEGIN\n" + dangerous.repeat(2200) + "\nLONG_END";
  const longReceipt = await request(base + "/collect", "POST", {
    run_id: runId,
    token: liveToken,
    data: { output: longOutput },
  });
  assert.equal(longReceipt.status, 201);
  for (let i = 0; i < 6; i++)
    assert.equal(
      (
        await request(base + "/collect", "POST", {
          run_id: runId,
          token: liveToken,
          data: ["自由结果", i],
        })
      ).status,
      201,
    );
  const detailRequests = [];
  page.on("request", (r) => {
    if (r.url().includes(`/api/sessions/${runId}/reports/`))
      detailRequests.push(r.url());
  });
  await page.reload();
  await page.getByRole("button", { name: "下一页回传", exact: true }).waitFor();
  assert.equal(await page.locator(".report").count(), 5);
  assert.equal(detailRequests.length, 0);
  assert((await page.locator(".detail-body").textContent()).length < 10000);
  await page.getByRole("button", { name: "下一页回传", exact: true }).click();
  await page
    .locator(".report .json-view")
    .filter({ hasText: "LONG_BEGIN" })
    .waitFor();
  assert.equal(
    await page
      .locator(".report img,.report script,.report svg")
      .filter({ has: page.locator("[onerror],[onload]") })
      .count(),
    0,
  );
  await page.getByRole("button", { name: "查看完整回传", exact: true }).click();
  const contentDialog = page.getByRole("dialog", { name: /回传 #/ });
  const contentView = contentDialog.getByLabel("回传完整内容", { exact: true });
  await contentView.waitFor();
  assert((await contentView.textContent()).length <= 8000);
  assert((await contentView.textContent()).includes("LONG_BEGIN"));
  assert.equal(await contentView.locator("img,script,svg,a").count(), 0);
  assert.equal(await page.evaluate(() => window.__receiptXSS), undefined);
  await contentDialog
    .getByRole("button", { name: "下一段", exact: true })
    .click();
  assert((await contentView.textContent()).length <= 8000);
  const rawDownload = page.waitForEvent("download");
  await contentDialog
    .getByRole("button", { name: "下载原始内容", exact: true })
    .click();
  const artifact = await rawDownload;
  assert.equal(
    JSON.parse(await readFile(await artifact.path(), "utf8")).data.output,
    longOutput,
  );
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "long-result-desktop.png"),
    });
  await page.setViewportSize({ width: 390, height: 844 });
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "long-result-mobile.png"),
    });
  await contentDialog
    .getByRole("button", { name: "关闭", exact: true })
    .last()
    .click();
  await page.setViewportSize({ width: 1440, height: 1000 });
  const textReceipt = await context.request.post(base + "/collect", {
    headers: {
      "Content-Type": "text/plain",
      "X-Run-ID": runId,
      "X-Run-Token": liveToken,
    },
    data: "自由文本 <svg onload=window.__receiptXSS=3>",
  });
  assert.equal(textReceipt.status(), 201);
  const deepRaw =
    JSON.stringify({ run_id: runId, token: liveToken }).slice(0, -1) +
    ',"data":' +
    "[".repeat(2000) +
    "0" +
    "]".repeat(2000) +
    "}";
  assert.equal(
    (
      await context.request.post(base + "/collect", {
        headers: { "Content-Type": "application/json" },
        data: deepRaw,
      })
    ).status(),
    201,
  );
  await page.goto(admin + "/#sessions/" + runId);
  await page
    .getByRole("button", { name: "查看完整回传", exact: true })
    .first()
    .click();
  await page
    .getByRole("dialog", { name: /回传 #/ })
    .getByLabel("回传完整内容", { exact: true })
    .waitFor();
  assert.equal(await page.evaluate(() => window.__receiptXSS), undefined);
  await page
    .getByRole("dialog", { name: /回传 #/ })
    .getByRole("button", { name: "关闭", exact: true })
    .last()
    .click();
  record(
    "schema-free JSON/text, lazy paged receipts, bounded deep JSON and inert XSS content with full download",
  );

  await page.goto(admin + "/#settings");
  await page.getByRole("heading", { name: "系统设置", exact: true }).waitFor();
  assert.equal(await page.getByLabel("端口首页", { exact: true }).count(), 0);
  const extraPort = await freePort();
  await page.getByRole("button", { name: "新增端口", exact: true }).click();
  await page.getByLabel("端口名称", { exact: true }).fill("组合端口二");
  await page.getByLabel("监听地址", { exact: true }).fill("127.0.0.1");
  await page.getByLabel("监听端口", { exact: true }).fill(String(adminPort));
  await page.getByLabel("此端口公告地址", { exact: true }).fill(admin);
  await choose(page.getByLabel("绑定工作区", { exact: true }), deployment.id);
  await page.getByRole("button", { name: "保存端口", exact: true }).click();
  await page
    .locator("dialog .port-error")
    .filter({ hasText: "占用" })
    .waitFor();
  await page.getByLabel("监听端口", { exact: true }).fill(String(extraPort));
  await page
    .getByLabel("此端口公告地址", { exact: true })
    .fill(`http://127.0.0.1:${extraPort}`);
  assert.equal(await page.getByRole("radio", { name: /独立配置/ }).count(), 0);
  assert.equal(await page.getByLabel("页面提示词", { exact: true }).count(), 0);
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "workspace-port-editor.png"),
    });
  await page.getByRole("button", { name: "保存端口", exact: true }).click();
  await page.getByRole("dialog").waitFor({ state: "hidden" });
  const extraBase = `http://127.0.0.1:${extraPort}`;
  assert.equal(
    (await clientContext.request.get(extraBase + "/")).status(),
    200,
  );
  assert.equal(
    (await clientContext.request.get(extraBase + "/?run_id=" + runId)).status(),
    403,
  );
  const extraCard = page
    .locator(".port-card")
    .filter({ hasText: "组合端口二" });
  await extraCard
    .getByRole("button", { name: "暂停端口", exact: true })
    .click();
  await extraCard
    .getByRole("button", { name: "启用端口", exact: true })
    .waitFor();
  await extraCard
    .getByRole("button", { name: "启用端口", exact: true })
    .click();
  await extraCard
    .getByRole("button", { name: "暂停端口", exact: true })
    .waitFor();
  record(
    "simplified ports bind published workspaces, report conflicts, isolate sessions, and pause/resume",
  );

  await page.goto(admin + "/#composer/" + workspaceId);
  await page
    .getByRole("heading", { name: "浏览器 Nacos 工作区", exact: true })
    .waitFor();
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "workspace-only-desktop.png"),
      fullPage: true,
    });
  await page.setViewportSize({ width: 390, height: 844 });
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  if (process.env.QA_OUTPUT_DIR)
    await page.screenshot({
      path: path.join(process.env.QA_OUTPUT_DIR, "workspace-only-mobile.png"),
      fullPage: true,
    });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("button", { name: "工作区设置", exact: true }).click();
  await workspaceSettings
    .getByRole("button", { name: "删除工作区", exact: true })
    .click();
  await page.getByRole("button", { name: "确认删除", exact: true }).click();
  await page
    .getByRole("button", { name: "创建第一个工作区", exact: true })
    .waitFor();
  const afterDelete = (await request(admin + "/api/state")).body;
  assert.equal(afterDelete.workspaces.length, 0);
  assert.equal(afterDelete.deployments.length, 0);
  assert(afterDelete.listeners.every((l) => !l.enabled && !l.deployment_id));
  assert.equal((await request(admin + "/api/sessions/" + runId)).status, 200);
  const materials = (await request(admin + "/api/composer/state")).body;
  assert(materials.sites.length > 0 && materials.scenarios.length > 0);
  record(
    "workspace deletion detaches and stops ports while preserving materials and historical sessions; mobile layout",
  );
  await page.goto(admin + "/#sessions");
  await page.getByRole("button", { name: "导出记录", exact: true }).waitFor();
  const exportEvent = page.waitForEvent("download");
  await page.getByRole("button", { name: "导出记录", exact: true }).click();
  assert.equal(
    (await exportEvent).suggestedFilename(),
    "agentmirror-sessions.json",
  );
  record("history remains readable and exportable after workspace removal");
  await clientContext.close();
  assert.deepEqual(failures, []);
  console.log(
    JSON.stringify(
      { passed: checks.length, checks, pageErrors: failures },
      null,
      2,
    ),
  );
} catch (error) {
  console.error("Page errors:", failures);
  if (process.env.QA_OUTPUT_DIR && browser) {
    const failedPage = browser.contexts()[0]?.pages()[0];
    if (failedPage) {
      await mkdir(process.env.QA_OUTPUT_DIR, { recursive: true });
      await failedPage
        .screenshot({
          path: path.join(process.env.QA_OUTPUT_DIR, "failure.png"),
          fullPage: true,
        })
        .catch(() => {});
    }
  }
  throw error;
} finally {
  if (browser) await browser.close();
  server.kill("SIGTERM");
  await new Promise((resolve) => server.once("exit", resolve));
  await rm(temp, { recursive: true, force: true });
}

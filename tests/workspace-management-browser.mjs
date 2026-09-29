// Browser plugin not available. Use Playwright against an isolated Go server.
// Flow: manage shared sites -> rename workspaces -> preserve unsaved changes.
import assert from "node:assert/strict";
import { once } from "node:events";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { spawn } from "node:child_process";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { chromium } from "playwright";

const root = path.resolve(import.meta.dirname, "..");
const temp = await mkdtemp(path.join(os.tmpdir(), "agentmirror-management-"));
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

  const createSite = (name) =>
    api("/sites", {
      name,
      entry: "index.html",
      files: [
        {
          path: "index.html",
          encoding: "utf8",
          content: "<!doctype html><title>Fixture</title><p>SYNTHETIC</p>",
        },
      ],
    });
  const bound = await createSite("正在使用的素材");
  const unused = await createSite("可删除的素材");
  const draftSite = await createSite("仅草稿选用的素材");
  const workspace = await api("/workspaces", {
    name: "工作区原名",
    slug: "management-check",
    site_id: bound.id,
    bindings: [],
  });
  const page = await context.newPage();
  page.setDefaultTimeout(10000);
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (entry) => {
    if (entry.type() === "error") errors.push(entry.text());
  });
  await page.goto(admin + "/#composer");
  assert.match(await page.title(), /AgentMirror/);
  assert.equal(new URL(page.url()).hash, "#composer");
  await page
    .getByRole("heading", { name: "蜜罐工作区", exact: true })
    .waitFor();
  await page.getByRole("button", { name: "站点素材", exact: true }).click();
  let dialog = page.getByRole("dialog");
  await dialog.getByLabel("搜索站点素材").fill("可删除");
  assert.equal(await dialog.locator(".site-library-row").count(), 1);
  await dialog
    .getByRole("button", { name: "删除素材 可删除的素材", exact: true })
    .click();
  await dialog.getByRole("button", { name: "返回素材库", exact: true }).click();
  assert.equal((await api("/sites/" + unused.id)).id, unused.id);
  await dialog
    .getByRole("button", { name: "删除素材 可删除的素材", exact: true })
    .click();
  await dialog.getByRole("button", { name: "确认删除", exact: true }).click();
  await dialog.getByText("没有匹配的站点素材", { exact: true }).waitFor();
  assert.equal(
    (await context.request.get(admin + "/api/sites/" + unused.id)).status(),
    404,
  );
  await dialog.getByLabel("搜索站点素材").fill("");
  await dialog
    .getByRole("button", { name: "删除素材 正在使用的素材", exact: true })
    .click();
  await dialog.getByRole("alert").filter({ hasText: "工作区原名" }).waitFor();
  assert(
    await dialog
      .getByRole("button", { name: "确认删除", exact: true })
      .isDisabled(),
  );
  await dialog.getByRole("button", { name: "返回素材库", exact: true }).click();
  const out = process.env.QA_OUTPUT_DIR;
  if (out) {
    await mkdir(out, { recursive: true });
    await page.screenshot({ path: path.join(out, "site-library-desktop.png") });
  }
  await dialog.getByRole("button", { name: "关闭素材库", exact: true }).click();
  pass(
    "site library search, cancel and confirmed delete work; referenced site names and deletion guard are visible",
  );

  await page.getByLabel("更多操作 工作区原名").click();
  await page.getByRole("button", { name: "重命名", exact: true }).click();
  assert.equal(await dialog.getByLabel("部署标识", { exact: true }).count(), 0);
  await dialog.getByLabel("工作区名称", { exact: true }).fill("工作区新名称");
  await dialog.getByRole("button", { name: "保存名称", exact: true }).click();
  await dialog.waitFor({ state: "hidden" });
  let saved = await api("/workspaces/" + workspace.id);
  assert.equal(saved.name, "工作区新名称");
  assert.equal(saved.slug, workspace.slug);
  assert.equal(saved.site_id, bound.id);
  await page.getByRole("button", { name: "工作区新名称", exact: true }).click();
  await page
    .getByRole("heading", { name: "工作区新名称", exact: true })
    .waitFor();
  await page.getByRole("tab", { name: "站点素材", exact: true }).click();
  await page.getByRole("button", { name: "移除选用", exact: true }).click();
  await page.getByText("有未保存更改", { exact: true }).waitFor();
  await page.getByRole("button", { name: "重命名工作区", exact: true }).click();
  await dialog.getByLabel("工作区名称", { exact: true }).fill("保留未保存修改");
  await page.setViewportSize({ width: 390, height: 844 });
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  if (out) await page.screenshot({ path: path.join(out, "rename-mobile.png") });
  await dialog.getByRole("button", { name: "保存名称", exact: true }).click();
  await dialog.waitFor({ state: "hidden" });
  await page.getByText("有未保存更改", { exact: true }).waitFor();
  saved = await api("/workspaces/" + workspace.id);
  assert.equal(saved.name, "保留未保存修改");
  assert.equal(
    saved.site_id,
    bound.id,
    "rename accidentally saved pending removal",
  );
  await page.getByRole("button", { name: "保存工作区", exact: true }).click();
  await page
    .getByRole("status")
    .filter({ hasText: "工作区草稿已保存" })
    .waitFor();
  saved = await api("/workspaces/" + workspace.id);
  assert.equal(saved.site_id, "");
  pass(
    "list and title rename persist; deployment slug stays fixed and unsaved site changes are neither saved nor lost",
  );

  await page.getByRole("button", { name: "管理素材", exact: true }).click();
  await dialog
    .getByRole("button", { name: "删除素材 正在使用的素材", exact: true })
    .click();
  assert(
    await dialog
      .getByRole("button", { name: "确认删除", exact: true })
      .isEnabled(),
  );
  await dialog.getByRole("button", { name: "确认删除", exact: true }).click();
  await dialog.getByLabel("搜索站点素材").waitFor();
  await dialog.getByRole("button", { name: "关闭素材库", exact: true }).click();
  await page
    .locator(`.material-site-row[data-material-id="${draftSite.id}"]`)
    .getByRole("button", { name: "选用", exact: true })
    .click();
  await page.getByRole("button", { name: "管理素材", exact: true }).click();
  await dialog
    .getByRole("button", { name: "删除素材 仅草稿选用的素材", exact: true })
    .click();
  await dialog
    .getByText("当前未保存草稿中的站点选用也会移除。", { exact: true })
    .waitFor();
  await dialog.getByRole("button", { name: "确认删除", exact: true }).click();
  await dialog.getByText("还没有站点素材", { exact: true }).waitFor();
  await dialog.getByRole("button", { name: "关闭素材库", exact: true }).click();
  assert(
    await page
      .getByRole("button", { name: "保存工作区", exact: true })
      .isDisabled(),
  );
  await page.reload();
  await page
    .getByRole("heading", { name: "保留未保存修改", exact: true })
    .waitFor();
  assert.equal(await page.locator("vite-error-overlay").count(), 0);
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  assert.deepEqual(errors, []);
  pass(
    "unlinked sites can be deleted; deleting a site selected only in an unsaved draft clears that selection; refresh and mobile layout remain healthy",
  );

  // Pagination must cover the whole catalogue, not search only the current page.
  const fixtures = [saved];
  for (let i = 1; i <= 23; i++)
    fixtures.push(
      await api("/workspaces", {
        name: `分页工作区 ${String(i).padStart(2, "0")}`,
        slug: `paging-${i}`,
        bindings: [],
      }),
    );
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page
    .getByRole("button", { name: "返回工作区列表", exact: true })
    .click();
  const pager = page.getByRole("navigation", {
    name: "工作区分页",
    exact: true,
  });
  const rows = page.locator(".workspace-row-name");
  await pager.getByText("第 1 / 5 页", { exact: true }).waitFor();
  assert.equal(await rows.count(), 5);
  assert.ok(await pager.getByRole("button", { name: "上一页" }).isDisabled());
  const firstNames = await rows.allTextContents();
  await pager.getByRole("button", { name: "下一页" }).click();
  await pager.getByText("第 2 / 5 页", { exact: true }).waitFor();
  const secondNames = await rows.allTextContents();
  assert.equal(secondNames.length, 5);
  await page.reload();
  await pager.getByText("第 2 / 5 页", { exact: true }).waitFor();
  await rows.first().click();
  await page
    .getByRole("heading", { name: secondNames[0], exact: true })
    .waitFor();
  await page
    .getByRole("button", { name: "返回工作区列表", exact: true })
    .click();
  await pager.getByText("第 2 / 5 页", { exact: true }).waitFor();
  assert.deepEqual(await rows.allTextContents(), secondNames);
  if (out)
    await page.screenshot({
      path: path.join(out, "workspace-page-two-desktop.png"),
    });
  await page
    .getByRole("navigation", { name: "工作区分页（底部）", exact: true })
    .getByRole("button", { name: "下一页" })
    .click();
  await pager.getByText("第 3 / 5 页", { exact: true }).waitFor();
  const middleNames = await rows.allTextContents();
  await pager.getByRole("button", { name: "下一页" }).click();
  await pager.getByText("第 4 / 5 页", { exact: true }).waitFor();
  middleNames.push(...(await rows.allTextContents()));
  await pager.getByRole("button", { name: "下一页" }).click();
  await pager.getByText("第 5 / 5 页", { exact: true }).waitFor();
  const lastNames = await rows.allTextContents();
  assert.equal(lastNames.length, 4);
  assert.ok(await pager.getByRole("button", { name: "下一页" }).isDisabled());
  assert.equal(
    new Set([...firstNames, ...secondNames, ...middleNames, ...lastNames]).size,
    24,
  );
  for (let i = 0; i < 3; i++)
    await pager.getByRole("button", { name: "上一页" }).click();
  assert.deepEqual(await rows.allTextContents(), secondNames);
  const search = page.getByRole("textbox", { name: "搜索工作区", exact: true });
  await search.fill("分页工作区");
  await pager.getByText("第 1 / 5 页", { exact: true }).waitFor();
  await search.fill(lastNames[0]);
  assert.deepEqual(await rows.allTextContents(), [lastNames[0]]);
  assert.equal(await pager.count(), 0);
  await search.fill("不存在的工作区");
  await page
    .getByText("没有匹配的工作区，试试其他名称或标识。", { exact: true })
    .waitFor();
  assert.equal(await rows.count(), 0);
  await search.fill("");
  await pager.getByText("第 1 / 5 页", { exact: true }).waitFor();
  await page.setViewportSize({ width: 390, height: 844 });
  await pager.getByRole("button", { name: "下一页" }).click();
  await pager.getByText("第 2 / 5 页", { exact: true }).waitFor();
  assert.ok(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  if (out)
    await page.screenshot({
      path: path.join(out, "workspace-page-two-mobile.png"),
    });
  for (let i = 0; i < 3; i++)
    await pager.getByRole("button", { name: "下一页" }).click();
  // All data here belong to the disposable fixture DB. Removing the last page
  // through the API then refreshing reproduces catalogue shrinkage in the UI.
  for (const name of lastNames) {
    const item = fixtures.find((w) => w.name === name);
    assert.ok(item, name);
    const removed = await context.request.delete(
      admin + "/api/workspaces/" + item.id,
      {
        headers: { "X-Admin-Token": state.csrf },
      },
    );
    assert.equal(removed.status(), 200, await removed.text());
  }
  await page.getByRole("button", { name: "刷新素材", exact: true }).click();
  await pager.getByText("第 4 / 4 页", { exact: true }).waitFor();
  assert.equal(await rows.count(), 5);
  assert.ok(await pager.getByRole("button", { name: "下一页" }).isDisabled());
  assert.deepEqual(errors, []);
  pass(
    "workspace pagination covers all 24 rows without duplication; both pagers, search reset, empty search, return/reload restoration, mobile and last-page deletion work",
  );

  // Category labels follow the effective prompt at each enabled delivery rule,
  // including child sites, rather than every saved default or unused override.
  const command = await api("/profiles", {
    name: "分类验收命令",
    body: "SYNTHETIC",
  });
  const ethical = await api("/profiles", {
    name: "分类验收阻断",
    category: "ethical_blocking",
    body: "SYNTHETIC",
  });
  let unexpected = await api("/profiles", {
    name: "分类验收输出",
    category: "unexpected_output",
    body: "SYNTHETIC",
  });
  const rule = (id, delivery = true) => ({
    id,
    name: id,
    method: "GET",
    path: "/" + id,
    delivery_required: delivery,
    response: {
      status: 200,
      format: "text",
      body: delivery ? "{{prompt}}" : "SYNTHETIC",
    },
  });
  const scenario = await api("/scenarios", {
    name: "分类验收接口",
    rules: [rule("first"), rule("second"), rule("ordinary", false)],
  });
  const plain = await api("/scenarios", {
    name: "分类验收普通接口",
    rules: [rule("ordinary", false)],
  });
  const categorySite = await createSite("分类验收站点");
  const binding = {
    id: "main",
    scenario_id: scenario.id,
    profile_id: command.id,
    enabled: true,
  };
  const categoryCases = [
    {
      name: "分类验收 · 多分类",
      bindings: [
        { ...binding, rule_profiles: { second: unexpected.id } },
        {
          ...binding,
          id: "child-binding",
          site_node_id: "child",
          profile_id: ethical.id,
        },
      ],
      site_id: categorySite.id,
      site_mounts: [
        {
          id: "child",
          name: "子站点",
          site_id: categorySite.id,
          segment: "child",
        },
      ],
      expected: ["执行命令", "道德阻断", "意外输出"],
    },
    {
      name: "分类验收 · 接口覆盖",
      bindings: [
        {
          ...binding,
          rule_profiles: {
            first: unexpected.id,
            second: unexpected.id,
            ordinary: ethical.id,
          },
        },
      ],
      expected: ["意外输出"],
    },
    {
      name: "分类验收 · 仅普通响应",
      bindings: [{ ...binding, scenario_id: plain.id }],
      expected: [],
    },
    {
      name: "分类验收 · 已停用场景",
      bindings: [{ ...binding, enabled: false }],
      expected: [],
    },
    { name: "分类验收 · 尚未绑定", bindings: [], expected: [] },
  ];
  for (const [i, { expected, ...fixture }] of categoryCases.entries())
    await api("/workspaces", { ...fixture, slug: `categories-${i}` });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await search.fill("分类验收");
  await page.getByRole("button", { name: "刷新素材", exact: true }).click();
  const categoryRow = (name) =>
    page.locator(".workspace-row").filter({
      has: page.getByRole("button", { name, exact: true }),
    });
  await categoryRow(categoryCases[0].name)
    .getByText("意外输出", { exact: true })
    .waitFor();
  for (const fixture of categoryCases) {
    const group = categoryRow(fixture.name).getByRole("group", {
      name: "提示词分类",
      exact: true,
    });
    assert.deepEqual(
      await group.locator(".badge").allTextContents(),
      fixture.expected,
    );
    if (!fixture.expected.length)
      await group.getByText("未使用提示词", { exact: true }).waitFor();
  }
  const categoryFilter = page.getByRole("combobox", {
    name: "按提示词分类筛选工作区",
  });
  const promptFilter = page.getByRole("combobox", {
    name: "按提示词方案筛选工作区",
  });
  await categoryFilter.click();
  await page.getByRole("option", { name: "道德阻断", exact: true }).click();
  assert.deepEqual(await rows.allTextContents(), [categoryCases[0].name]);
  await promptFilter.click();
  await page.getByRole("option", { name: command.name, exact: true }).click();
  assert.deepEqual(await rows.allTextContents(), [categoryCases[0].name]);
  await categoryFilter.click();
  await page.getByRole("option", { name: "未使用提示词", exact: true }).click();
  assert.equal(await rows.count(), 0);
  await promptFilter.click();
  await page
    .getByRole("option", { name: "全部提示词方案", exact: true })
    .click();
  assert.equal(await rows.count(), 3);
  await categoryFilter.click();
  await page
    .getByRole("option", { name: "全部提示词分类", exact: true })
    .click();
  pass(
    "workspace filters combine categories and actual prompt bindings, including multiple categories and unused prompts",
  );
  await page.screenshot(
    out ? { path: path.join(out, "workspace-categories-desktop.png") } : {},
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await search.fill("分类验收 · 多分类");
  const multiple = categoryRow(categoryCases[0].name);
  assert.deepEqual(
    await multiple
      .locator(".workspace-prompt-categories .badge")
      .allTextContents(),
    categoryCases[0].expected,
  );
  assert.ok(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  if (out)
    await page.screenshot({
      path: path.join(out, "workspace-categories-mobile.png"),
    });
  await search.fill("分类验收 · 接口覆盖");
  unexpected = await api("/profiles", {
    ...unexpected,
    category: "ethical_blocking",
  });
  await page.getByRole("button", { name: "刷新素材", exact: true }).click();
  const updated = categoryRow(categoryCases[1].name).getByRole("group", {
    name: "提示词分类",
    exact: true,
  });
  await updated.getByText("道德阻断", { exact: true }).waitFor();
  assert.deepEqual(await updated.locator(".badge").allTextContents(), [
    "道德阻断",
  ]);
  assert.deepEqual(errors, []);
  pass(
    "workspace prompt categories include main/child sites, deduplicate effective rule overrides, exclude disabled/plain responses, show empty state, refresh after category edits, and wrap on mobile",
  );

  console.log(JSON.stringify({ checks, errors }, null, 2));
} finally {
  if (browser) await browser.close();
  const stopped = once(server, "exit");
  server.kill("SIGTERM");
  await stopped;
  await rm(temp, { recursive: true, force: true });
}

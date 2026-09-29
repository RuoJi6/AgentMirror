// Browser plugin unavailable: verify in an isolated Playwright browser/server.
import assert from "node:assert/strict";
import { once } from "node:events";
import { mkdtemp, mkdir, readFile, rm } from "node:fs/promises";
import { spawn } from "node:child_process";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { chromium } from "playwright";
const root = path.resolve(import.meta.dirname, "..");
const temp = await mkdtemp(path.join(os.tmpdir(), "agentmirror-linked-"));
const freePort = async () => {
  const s = net.createServer().listen(0, "127.0.0.1");
  await once(s, "listening");
  const port = s.address().port;
  await new Promise((r) => s.close(r));
  return port;
};
const port = await freePort(),
  publicPort = await freePort(),
  admin = `http://127.0.0.1:${port}`;
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
  { cwd: root, stdio: ["ignore", "pipe", "pipe"] },
);
let browser;
const errors = [];
const shots = process.env.QA_SCREENSHOTS || path.join(temp, "screens");
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
  browser = await chromium.launch({
    headless: true,
    ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH
      ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH }
      : {}),
  });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
  });
  const status = await (
    await context.request.get(admin + "/api/auth/status")
  ).json();
  const auth = await (
    await context.request.post(admin + "/api/auth/setup", {
      headers: { "X-Admin-Token": status.csrf },
      data: {
        username: "linkedtest",
        password: "SYNTHETIC-linked-2026",
        confirm_password: "SYNTHETIC-linked-2026",
      },
    })
  ).json();
  const api = async (route, body, status = 200) => {
    const r = await context.request.fetch(admin + "/api" + route, {
      method: body ? "POST" : "GET",
      headers: { "X-Admin-Token": auth.csrf },
      ...(body ? { data: body } : {}),
    });
    assert.equal(r.status(), status, await r.text());
    return r.json();
  };
  const makeSite = (name, html) =>
    api("/sites", {
      name,
      files: [{ path: "index.html", content: html, encoding: "utf8" }],
    });
  const home = await makeSite(
    "企业门户",
    '<h1>企业门户</h1><a href="oss/">对象存储</a>',
  );
  const oss = await makeSite(
    "对象存储",
    '<h1>对象存储</h1><a href="vpn/">访问客户端</a>',
  );
  const vpn = await makeSite(
    "访问客户端",
    '<h1>访问客户端</h1><a href="downloads/vpn.zip">下载客户端</a>',
  );
  let workspace = await api("/workspaces", {
    name: "联动链路",
    slug: "linked",
    site_id: home.id,
    bindings: [],
  });
  const page = await context.newPage();
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(admin + "/#composer/" + workspace.id);
  await page.getByRole("tab", { name: "站点素材", exact: true }).click();
  await page.getByRole("button", { name: "添加子站点", exact: true }).click();
  const topology = page.locator("section.panel").filter({
    has: page.getByRole("heading", { name: "多站点联动", exact: true }),
  });
  const children = topology.locator(".workspace-site-node");
  const first = children.nth(1);
  await first.locator("input").nth(0).fill("OSS 子站");
  const select = async (locator, text) => {
    await locator.click();
    await page.getByRole("option", { name: text, exact: true }).click();
  };
  await select(first.getByRole("combobox").nth(0), "对象存储 · v1");
  await first.locator("input").nth(1).fill("oss");
  await page.getByRole("button", { name: "添加子站点", exact: true }).click();
  const second = children.nth(2);
  await second.locator("input").nth(0).fill("VPN 子站");
  await select(second.getByRole("combobox").nth(0), "访问客户端 · v1");
  await select(second.getByRole("combobox").nth(1), "OSS 子站");
  await second.locator("input").nth(1).fill("vpn");
  await page.getByRole("button", { name: "保存工作区", exact: true }).click();
  await page
    .getByRole("button", { name: "保存工作区", exact: true })
    .isDisabled();
  await page.waitForFunction(async (id) => {
    const w = await (await fetch("/api/workspaces/" + id)).json();
    return w.site_mounts?.length === 2;
  }, workspace.id);
  workspace = await api("/workspaces/" + workspace.id);
  assert.deepEqual(
    workspace.site_mounts.map((n) => n.mount_path),
    ["/oss/", "/oss/vpn/"],
  );
  console.log("PASS UI nested site creation and persisted paths");
  const downloadPanel = page.locator("section.panel").filter({
    has: page.getByRole("heading", {
      name: "文件托管与 HTTP 下载",
      exact: true,
    }),
  });
  await select(downloadPanel.getByRole("combobox"), "VPN 子站 · /oss/vpn/");
  await downloadPanel
    .getByLabel("站内下载路径", { exact: true })
    .fill("downloads/vpn.zip");
  const binary = Buffer.from([
    0x50,
    0x4b,
    3,
    4,
    0,
    255,
    ...Buffer.from("harmless browser fixture"),
  ]);
  await downloadPanel
    .getByLabel("上传托管文件", { exact: true })
    .setInputFiles({
      name: "VPN客户端.zip",
      mimeType: "application/zip",
      buffer: binary,
    });
  await downloadPanel
    .getByRole("button", { name: "复制下载链接", exact: true })
    .waitFor();
  const savedVPN = await api("/sites/" + vpn.id);
  assert.equal(
    savedVPN.files.find((f) => f.path === "downloads/vpn.zip").encoding,
    "hosted",
  );
  const dep = await api("/workspaces/" + workspace.id + "/publish", {
    version: workspace.version,
  });
  const listener = await api("/listeners", {
    name: "联动测试",
    host: "127.0.0.1",
    port: publicPort,
    public_url: `http://127.0.0.1:${publicPort}`,
    deployment_id: dep.id,
    enabled: true,
  });
  const visitor = await browser.newContext();
  const visit = await visitor.newPage();
  const runIDs = [];
  visit.on("response", (res) => {
    const id = res.headers()["x-run-id"];
    if (id) runIDs.push(id);
  });
  await visit.goto(listener.public_url + "/");
  await visit.getByRole("link", { name: "对象存储" }).click();
  assert.ok(visit.url().endsWith("/oss/"));
  await visit.getByRole("link", { name: "访问客户端" }).click();
  assert.ok(visit.url().endsWith("/oss/vpn/"));
  const [download] = await Promise.all([
    visit.waitForEvent("download"),
    visit.getByRole("link", { name: "下载客户端" }).click(),
  ]);
  assert.equal(download.suggestedFilename(), "VPN客户端.zip");
  assert.deepEqual(await readFile(await download.path()), binary);
  assert.equal(new Set(runIDs).size, 1);
  console.log(
    "PASS actual HTTP main → OSS → VPN → binary download with one session",
  );
  const scenario = await api("/scenarios", {
    name: "VPN 状态",
    rules: [
      {
        id: "status",
        method: "GET",
        path: "/api/status",
        conditions: [],
        response: { status: 200, format: "json", body: '{"ok":true}' },
      },
    ],
  });
  workspace = await api("/workspaces", {
    ...workspace,
    bindings: [
      {
        id: "vpn-api",
        site_node_id: workspace.site_mounts[1].id,
        scenario_id: scenario.id,
        enabled: true,
      },
    ],
  });
  await page.reload();
  await page.getByRole("tab", { name: "请求预演", exact: true }).click();
  await page
    .getByRole("button")
    .filter({
      has: page
        .locator("code")
        .getByText("/oss/vpn/api/status", { exact: true }),
    })
    .click();
  await page.getByRole("button", { name: "发送预演请求", exact: true }).click();
  await page
    .locator(".composer-raw-response")
    .getByText('{"ok":true}', { exact: true })
    .waitFor();
  console.log("PASS child scenario request preview uses mounted route");
  await page.getByRole("tab", { name: "站点素材", exact: true }).click();
  await topology.scrollIntoViewIfNeeded();
  await mkdir(shots, { recursive: true });
  await topology.screenshot({
    path: path.join(shots, "linked-sites-desktop.png"),
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await select(downloadPanel.getByRole("combobox"), "VPN 子站 · /oss/vpn/");
  await page.waitForFunction(
    () => document.querySelector(".sidebar").getBoundingClientRect().right <= 1,
  );
  await downloadPanel.scrollIntoViewIfNeeded();
  await page.screenshot({
    path: path.join(shots, "linked-download-mobile.png"),
  });
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth > innerWidth + 2,
  );
  assert.equal(overflow, false, "mobile document overflow");
  await select(downloadPanel.getByRole("combobox"), "VPN 子站 · /oss/vpn/");
  await downloadPanel
    .getByRole("button", { name: "移除下载", exact: true })
    .click();
  await page.waitForFunction(async (id) => {
    const s = await (await fetch("/api/sites/" + id)).json();
    return !s.files.some((f) => f.encoding === "hosted");
  }, vpn.id);
  const gone = await visitor.request.get(
    listener.public_url + "/oss/vpn/downloads/vpn.zip",
  );
  assert.equal(gone.status(), 404);
  assert.deepEqual(errors, []);
  console.log(
    "PASS mobile controls, download removal live update, no browser exceptions",
  );
} finally {
  await browser?.close();
  server.kill("SIGTERM");
  await once(server, "exit");
  await rm(temp, { recursive: true, force: true });
}

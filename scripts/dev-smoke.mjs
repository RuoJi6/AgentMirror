import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, rm, stat, utimes } from "node:fs/promises";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const temporary = await mkdtemp(
  path.join(os.tmpdir(), "agentmirror-dev-smoke-"),
);
const source = path.join(root, "assets_dev.go");
const sourceStat = await stat(source);
const children = [];
const probes = [];

async function reservePort(port = 0) {
  const probe = net.createServer();
  probes.push(probe);
  await new Promise((resolve, reject) => {
    probe.once("error", reject);
    probe.listen(port, "127.0.0.1", resolve);
  });
  return probe;
}
const closeProbe = (probe) => new Promise((resolve) => probe.close(resolve));

function start(apiPort, webPort) {
  const child = spawn(process.execPath, [path.join(root, "scripts/dev.mjs")], {
    cwd: root,
    env: {
      ...process.env,
      AGENTMIRROR_DEV_API_PORT: String(apiPort),
      AGENTMIRROR_DEV_WEB_PORT: String(webPort),
      AGENTMIRROR_DEV_DB: path.join(temporary, "lab.sqlite3"),
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  const record = { child, output: "", closed: false };
  children.push(record);
  const collect = (chunk) => {
    record.output += chunk.toString();
  };
  child.stdout.on("data", collect);
  child.stderr.on("data", collect);
  record.done = new Promise((resolve) => {
    child.once("error", (error) => {
      record.error = error;
    });
    child.once("close", (code) => {
      record.closed = true;
      resolve(code);
    });
  });
  return record;
}

async function waitUntil(predicate, record, message) {
  for (let attempt = 0; attempt < 600; attempt++) {
    if (predicate()) return;
    if (record.closed) throw new Error(`${message}\n${record.output}`);
    await delay(100);
  }
  throw new Error(`超时：${message}\n${record.output}`);
}

async function stop(record) {
  if (!record.closed) record.child.kill("SIGTERM");
  let timeout;
  try {
    return await Promise.race([
      record.done,
      new Promise((resolve, reject) => {
        timeout = setTimeout(
          () => reject(new Error("开发进程停止超时")),
          10000,
        );
      }),
    ]);
  } finally {
    clearTimeout(timeout);
  }
}

try {
  const apiProbe = await reservePort();
  const webProbe = await reservePort();
  const apiPort = apiProbe.address().port;
  const webPort = webProbe.address().port;
  const origin = `http://127.0.0.1:${webPort}`;

  // Occupied ports belong to another process and must remain untouched.
  const occupied = start(apiPort, webPort);
  assert.equal(await occupied.done, 1, occupied.output);
  assert.match(occupied.output, /已被占用/);
  assert.equal(apiProbe.listening, true);
  await Promise.all([closeProbe(apiProbe), closeProbe(webProbe)]);

  const runner = start(apiPort, webPort);
  const backendPIDs = () =>
    [...runner.output.matchAll(/Go 后端已就绪[^\n]*\(pid (\d+)\)/g)].map(
      (match) => Number(match[1]),
    );
  await waitUntil(() => backendPIDs().length >= 1, runner, "后端启动");
  const firstPID = backendPIDs()[0];
  const html = await (await fetch(origin)).text();
  assert.match(html, /@vite\/client/);
  let status = await (await fetch(origin + "/api/auth/status")).json();
  assert.equal(status.initialized, false);
  const credentials = {
    username: "devsmoke",
    password: "SYNTHETIC-dev-smoke-password",
    confirm_password: "SYNTHETIC-dev-smoke-password",
  };
  const foreign = await fetch(origin + "/api/auth/setup", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Admin-Token": status.csrf,
      Origin: "http://foreign.invalid",
    },
    body: JSON.stringify(credentials),
  });
  assert.equal(foreign.status, 403);
  const setup = await fetch(origin + "/api/auth/setup", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Admin-Token": status.csrf,
      Origin: origin,
    },
    body: JSON.stringify(credentials),
  });
  assert.equal(setup.status, 200, await setup.text());
  const cookie = setup.headers.get("set-cookie").split(";")[0];
  const state = await (
    await fetch(origin + "/api/state", { headers: { Cookie: cookie } })
  ).json();
  assert.equal(state.runtime.backend, "go");
  const created = await fetch(origin + "/api/workspaces", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Admin-Token": state.csrf,
      Cookie: cookie,
      Origin: origin,
    },
    body: JSON.stringify({ name: "Development smoke", slug: "dev-smoke" }),
  });
  assert.equal(created.status, 200, await created.text());

  // Trigger the watcher without altering any source-file contents.
  await utimes(source, sourceStat.atime, new Date());
  await waitUntil(
    () => backendPIDs().length >= 2,
    runner,
    "Go 文件保存后自动重启",
  );
  assert.notEqual(backendPIDs()[1], firstPID);
  assert.throws(() => process.kill(firstPID, 0), { code: "ESRCH" });
  status = await (await fetch(origin + "/api/auth/status")).json();
  assert.equal(
    status.initialized,
    true,
    "Go restart must preserve the development DB",
  );
  assert.equal(await stop(runner), 0, runner.output);

  // Both the Vite child and all replacement backend processes must be gone.
  const pids = [...runner.output.matchAll(/\(pid (\d+)\)/g)].map((match) =>
    Number(match[1]),
  );
  for (const pid of new Set(pids)) {
    assert.throws(() => process.kill(pid, 0), { code: "ESRCH" });
  }
  await closeProbe(await reservePort(apiPort));
  await closeProbe(await reservePort(webPort));
  console.log(
    "Development smoke passed: port conflicts, API proxy, same-origin writes, Go reload, persistent DB, and child cleanup.",
  );
} finally {
  await Promise.all(children.map(stop));
  for (const probe of probes) if (probe.listening) await closeProbe(probe);
  await utimes(source, sourceStat.atime, sourceStat.mtime);
  await rm(temporary, { recursive: true, force: true });
}

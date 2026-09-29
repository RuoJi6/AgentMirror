import { spawn } from "node:child_process";
import { watch } from "node:fs";
import { access, mkdir, mkdtemp, rename, rm } from "node:fs/promises";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const host = "127.0.0.1";
const children = new Set();
const watchers = [];
let temporaryDirectory;
let backend;
let stopping = false;
let shutdownPromise;
let rebuildTimer;
let building = false;
let rebuildPending = false;

function readPort(name, fallback) {
  const value = process.env[name] || String(fallback);
  if (!/^\d+$/.test(value) || Number(value) < 1 || Number(value) > 65535) {
    throw new Error(`${name} 必须是 1–65535 的整数`);
  }
  return Number(value);
}

async function checkPort(port) {
  await new Promise((resolve, reject) => {
    const probe = net.createServer();
    probe.once("error", (error) => {
      reject(
        new Error(
          error.code === "EADDRINUSE"
            ? `${host}:${port} 已被占用；请更换开发端口或自行停止占用服务。`
            : `无法使用 ${host}:${port}：${error.message}`,
        ),
      );
    });
    probe.listen(port, host, () => probe.close(resolve));
  });
}

function startChild(label, command, args, env = {}, persistent = false) {
  if (stopping) throw new Error("开发服务正在停止");
  const child = spawn(command, args, {
    cwd: root,
    env: { ...process.env, ...env },
    stdio: "inherit",
    detached: true,
  });
  const record = { child, label, expected: false, closed: false };
  children.add(record);
  record.done = new Promise((resolve) => {
    let spawnError;
    child.once("error", (error) => {
      spawnError = error;
    });
    child.once("close", (code, signal) => {
      record.closed = true;
      children.delete(record);
      resolve({ code, signal, error: spawnError });
      if (persistent && !record.expected && !stopping) {
        void shutdown(
          1,
          `${label} 意外退出：${spawnError?.message || signal || code}`,
        );
      }
    });
  });
  return record;
}

function signalChild(record, signal) {
  if (!record?.child.pid) return;
  try {
    // Each owned child starts its own process group; never signal the caller's
    // terminal group or a process found by port number.
    process.kill(-record.child.pid, signal);
  } catch (error) {
    if (error.code !== "ESRCH") console.error(`[dev] ${error.message}`);
  }
}

async function stopChild(record) {
  if (!record || record.closed) return;
  record.expected = true;
  signalChild(record, "SIGTERM");
  let timeout;
  const stopped = await Promise.race([
    record.done.then(() => true),
    new Promise((resolve) => {
      timeout = setTimeout(() => resolve(false), 5000);
    }),
  ]);
  clearTimeout(timeout);
  if (!stopped) {
    signalChild(record, "SIGKILL");
    await record.done;
  }
}

function shutdown(code = 0, message) {
  if (shutdownPromise) return shutdownPromise;
  stopping = true;
  clearTimeout(rebuildTimer);
  for (const watcher of watchers) watcher.close();
  if (message) console.error(`[dev] ${message}`);
  shutdownPromise = (async () => {
    await Promise.all([...children].map(stopChild));
    if (temporaryDirectory) {
      await rm(temporaryDirectory, { recursive: true, force: true });
    }
    console.log("[dev] 开发前端、后端和编译进程已停止；开发数据库已保留。");
    process.exit(code);
  })();
  return shutdownPromise;
}

async function waitForHTTP(url, record) {
  for (let attempt = 0; attempt < 100; attempt++) {
    if (stopping || record.closed) throw new Error(`${record.label} 未能启动`);
    try {
      const response = await fetch(url, {
        signal: AbortSignal.timeout(500),
      });
      await response.arrayBuffer();
      if (response.ok) return;
    } catch {
      // A freshly spawned process may still be initializing its listener.
    }
    await delay(100);
  }
  throw new Error(`等待 ${url} 就绪超时`);
}

async function main() {
  if (process.platform === "win32") {
    throw new Error(
      "一键开发模式需要 macOS/Linux 的进程组支持；Windows 请在 WSL 中运行 npm run dev。",
    );
  }
  const apiPort = readPort("AGENTMIRROR_DEV_API_PORT", 8769);
  const webPort = readPort("AGENTMIRROR_DEV_WEB_PORT", 5173);
  if (apiPort === webPort) throw new Error("开发前端与后端必须使用不同端口");
  const apiURL = `http://${host}:${apiPort}`;
  const webURL = `http://${host}:${webPort}`;
  const database = path.resolve(
    root,
    process.env.AGENTMIRROR_DEV_DB || "data/dev/agentmirror.sqlite3",
  );
  const vite = path.join(root, "node_modules", "vite", "bin", "vite.js");
  try {
    await access(vite);
  } catch {
    throw new Error("缺少前端依赖，请先运行 npm ci");
  }
  await checkPort(apiPort);
  await checkPort(webPort);
  temporaryDirectory = await mkdtemp(
    path.join(os.tmpdir(), "agentmirror-dev-"),
  );
  await mkdir(path.dirname(database), { recursive: true });
  const binary = path.join(temporaryDirectory, "agentmirror");

  async function rebuild() {
    if (stopping) return;
    if (building) {
      rebuildPending = true;
      return;
    }
    building = true;
    console.log("[dev] 正在编译 Go 后端…");
    try {
      const build = startChild(
        "Go 编译",
        "go",
        ["build", "-tags", "dev", "-o", `${binary}.next`, "./cmd/agentmirror"],
        { CGO_ENABLED: "0" },
      );
      const result = await build.done;
      if (stopping) return;
      if (result.error) throw result.error;
      if (result.code !== 0) {
        console.error(
          `[dev] Go 编译失败，${backend ? "保留上次运行的后端" : "等待后端启动"}；修改源码后自动重试。`,
        );
        return;
      }
      await stopChild(backend);
      if (stopping) return;
      await rename(`${binary}.next`, binary);
      backend = startChild(
        "Go 后端",
        binary,
        [
          "--admin-host",
          host,
          "--admin-port",
          String(apiPort),
          "--db",
          database,
        ],
        {},
        true,
      );
      await waitForHTTP(`${apiURL}/api/auth/status`, backend);
      console.log(`[dev] Go 后端已就绪 ${apiURL} (pid ${backend.child.pid})`);
    } catch (error) {
      if (!stopping) await shutdown(1, error.message);
    } finally {
      building = false;
      if (rebuildPending && !stopping) {
        rebuildPending = false;
        scheduleRebuild();
      }
    }
  }

  function scheduleRebuild() {
    clearTimeout(rebuildTimer);
    rebuildTimer = setTimeout(() => void rebuild(), 300);
  }

  const sourceChanged = (event, filename) => {
    if (filename && !stopping) scheduleRebuild();
  };
  watchers.push(
    watch(root, (event, filename) => {
      if (filename && /(?:\.go$|^go\.(?:mod|sum)$)/.test(String(filename))) {
        sourceChanged(event, filename);
      }
    }),
    watch(path.join(root, "cmd"), { recursive: true }, sourceChanged),
    watch(path.join(root, "internal"), { recursive: true }, sourceChanged),
  );
  for (const watcher of watchers) {
    watcher.on("error", (error) => void shutdown(1, error.message));
  }

  const frontend = startChild(
    "Vite 前端",
    process.execPath,
    [vite, "--host", host, "--port", String(webPort), "--strictPort"],
    {
      AGENTMIRROR_DEV_API_URL: apiURL,
      AGENTMIRROR_DEV_WEB_PORT: String(webPort),
    },
    true,
  );
  console.log(
    `[dev] 前端 ${webURL} → API ${apiURL} (pid ${frontend.child.pid})`,
  );
  console.log(`[dev] 数据库 ${database}`);
  console.log("[dev] Go 源码自动重编译，前端由 Vite 热更新；Ctrl+C 一并停止。");
  await Promise.all([waitForHTTP(webURL, frontend), rebuild()]);
}

for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
  process.on(signal, () => void shutdown());
}
main().catch((error) => void shutdown(1, error.message));

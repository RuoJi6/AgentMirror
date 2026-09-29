// Prove that the release runs outside the checkout with no language runtime on PATH.
import assert from "node:assert/strict";
import { copyFile, mkdtemp, rm, readdir, chmod } from "node:fs/promises";
import { spawn } from "node:child_process";
import { once } from "node:events";
import net from "node:net";
import os from "node:os";
import path from "node:path";
const root = path.resolve(import.meta.dirname, "..");
const temp = await mkdtemp(path.join(os.tmpdir(), "agentmirror standalone-"));
const filename = path.join(
  temp,
  process.platform === "win32" ? "agentmirror.exe" : "agentmirror",
);
let server;
try {
  await copyFile(
    process.env.AGENTMIRROR_SERVER ||
      path.join(
        root,
        "bin",
        process.platform === "win32" ? "agentmirror.exe" : "agentmirror",
      ),
    filename,
  );
  await chmod(filename, 0o755);
  const listener = net.createServer();
  listener.listen(0, "127.0.0.1");
  await once(listener, "listening");
  const port = listener.address().port;
  await new Promise((resolve) => listener.close(resolve));
  server = spawn(
    filename,
    [
      "--admin-port",
      String(port),
      "--db",
      path.join(temp, "data", "数据库 # 100%.sqlite3"),
    ],
    {
      cwd: temp,
      env: {
        PATH: "",
        ...(process.env.SystemRoot
          ? { SystemRoot: process.env.SystemRoot }
          : {}),
      },
      stdio: ["ignore", "pipe", "inherit"],
    },
  );
  await new Promise((resolve, reject) => {
    const timer = setTimeout(
      () => reject(Error("standalone startup timed out")),
      10000,
    );
    server.on("error", reject);
    server.once("exit", (code) => {
      clearTimeout(timer);
      reject(Error("standalone exited " + code));
    });
    server.stdout.on("data", (chunk) => {
      if (chunk.toString().includes("公告地址")) {
        clearTimeout(timer);
        resolve();
      }
    });
  });
  const base = `http://127.0.0.1:${port}`;
  assert.equal((await fetch(base + "/api/state")).status, 401);
  const authStatus = await (await fetch(base + "/api/auth/status")).json();
  assert.equal(authStatus.initialized, false);
  const setup = await fetch(base + "/api/auth/setup", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Admin-Token": authStatus.csrf,
    },
    body: JSON.stringify({
      username: "testadmin",
      password: "SYNTHETIC-standalone-password",
      confirm_password: "SYNTHETIC-standalone-password",
    }),
  });
  assert.equal(setup.status, 200);
  const headers = { Cookie: setup.headers.get("set-cookie").split(";")[0] };
  const state = await (await fetch(base + "/api/state", { headers })).json();
  assert.equal(state.runtime.backend, "go");
  assert.equal(state.deployments.length, 0);
  assert.equal(state.listeners.length, 0);
  assert.equal(state.profiles.length, 12);
  assert.equal(state.workspaces.length, 12);
  assert(state.workspaces.every((workspace) => workspace.publication_status === "draft"));
  for (const obsolete of ["observe", "receipt", "environment", "custom"]) {
    assert(!state.profiles.some((profile) => profile.id === obsolete));
  }
  const html = await (await fetch(base)).text();
  const asset = html.match(/src="([^"]+\.js)"/)[1];
  const script = await fetch(base + asset);
  assert.equal(script.status, 200);
  assert((await script.text()).length > 1000);
  const library = await (
    await fetch(base + "/api/composer/state", { headers })
  ).json();
  assert.equal(library.sites.length, 12);
  assert.equal(library.scenarios.length, 12);
  assert(library.sites.every((site) => site.files.every((file) => file.encoding !== "hosted")));
  const preview = await fetch(base + "/api/sites/preview", {
    method: "POST",
    headers: {
      ...headers,
      "Content-Type": "application/json",
      "X-Admin-Token": state.csrf,
    },
    body: JSON.stringify({ site: library.presets.sites[0] }),
  });
  assert.equal(preview.status, 200);
  assert((await preview.json()).html.includes("配置管理"));
  assert.equal(
    (await fetch(base + "/api/preview/login", { headers })).status,
    410,
  );
  assert.deepEqual(
    (await readdir(temp)).sort(),
    [path.basename(filename), "data"].sort(),
  );
  console.log(
    "PASS standalone Go binary serves administration and workspace previews with 12 built-in workspaces and prompts, empty PATH, outside the checkout",
  );
} finally {
  if (server && server.exitCode === null) {
    const stopped = once(server, "exit");
    server.kill("SIGTERM");
    await stopped;
  }
  await rm(temp, { recursive: true, force: true });
}

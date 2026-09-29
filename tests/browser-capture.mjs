import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import http from "node:http";
import path from "node:path";

// Browser plugin not available. This protocol-level Playwright test supplies all
// source responses itself and never visits an Internet website.
const root = path.resolve(import.meta.dirname, "..");
const workerPath = path.join(root, "scripts/browser-capture.mjs");
let directRequests = 0;
const sentinel = http.createServer((_request, response) => {
  directRequests++;
  response.end("unexpected direct browser request");
});
sentinel.listen(0, "127.0.0.1");
await once(sentinel, "listening");
const sentinelURL = `http://127.0.0.1:${sentinel.address().port}/direct`;

function startCapture(config, onFetch, afterMessage) {
  const worker = spawn(process.execPath, [workerPath], {
    cwd: root,
    stdio: ["pipe", "pipe", "pipe"],
    env: process.env,
  });
  let output = "";
  let stderr = "";
  const messages = [];
  worker.stderr.on("data", (chunk) => (stderr += chunk));
  worker.stdout.setEncoding("utf8");
  worker.stdout.on("data", (chunk) => {
    output += chunk;
    for (;;) {
      const end = output.indexOf("\n");
      if (end < 0) break;
      const message = JSON.parse(output.slice(0, end));
      output = output.slice(end + 1);
      messages.push(message);
      if (message.type === "fetch") {
        const response = onFetch(message, worker);
        if (response)
          worker.stdin.write(
            `${JSON.stringify({ type: "response", id: message.id, ...response })}\n`,
          );
      }
      afterMessage?.(message, worker);
    }
  });
  const completed = new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      worker.kill("SIGKILL");
      reject(
        new Error(
          `worker did not terminate; messages: ${JSON.stringify(messages)}; stderr: ${stderr}`,
        ),
      );
    }, 20000);
    worker.on("error", reject);
    worker.on("exit", (code) => {
      clearTimeout(timeout);
      resolve({ code, messages, stderr });
    });
  });
  worker.stdin.write(
    `${JSON.stringify({ type: "start", timeout_ms: 15000, ...config })}\n`,
  );
  return { worker, completed };
}

function response(
  body,
  type = "text/html; charset=utf-8",
  status = 200,
  headers = {},
) {
  return {
    status,
    headers: {
      "content-type": type,
      "access-control-allow-origin": "*",
      ...headers,
    },
    body_base64: Buffer.from(body).toString("base64"),
  };
}

try {
  const requested = [];
  const fixture = startCapture(
    { url: "https://capture.test/start" },
    (message) => {
      requested.push(message);
      assert.deepEqual(Object.keys(message).sort(), [
        "id",
        "method",
        "resource_type",
        "type",
        "url",
      ]);
      assert.equal(message.method, "GET");
      if (message.url === sentinelURL) return { error: "已阻断非公开目标" };
      const url = new URL(message.url);
      assert.equal(url.origin, "https://capture.test");
      if (url.pathname === "/start")
        return response("", "text/html", 307, {
          location: "https://capture.test/app",
        });
      if (url.pathname === "/app") {
        return response(
          `<!doctype html><html><head><title>动态页面测试</title><link rel="stylesheet" href="/assets/site.css"></head><body><div id="app">Loading...</div><script src="/assets/main.js"></script></body></html>`,
        );
      }
      if (url.pathname === "/assets/site.css")
        return response("", "text/css", 307, { location: "/assets/final.css" });
      if (url.pathname === "/assets/final.css")
        return response("body { background: #e8f1ff; }", "text/css");
      if (url.pathname === "/assets/main.js") {
        return response(
          `
        document.cookie = 'temporary_session=do-not-forward';
        fetch('${sentinelURL}', {mode:'no-cors'}).catch(() => {});
        fetch('/write', {method:'POST', body:'must never reach source'}).catch(() => {});
        window.open('/popup');
        try { new WebSocket('wss://capture.test/socket'); } catch {}
        navigator.serviceWorker.register('/service-worker.js').catch(() => {});
        try { new Worker('/worker.js'); } catch {}
        const frame = document.createElement('iframe'); frame.src = '/embedded'; document.body.append(frame);
        const delayed = setTimeout(() => {
          document.getElementById('app').innerHTML = '<h1>动态登录页面</h1><form><label>用户名<input id="username"></label><input id="password" type="password"><input id="remember" type="checkbox"><textarea id="notes"></textarea><select id="theme"><option value="dark">Dark</option><option value="light">Light</option></select></form>';
          document.getElementById('username').value = '演示用户';
          document.getElementById('password').value = 'do-not-capture';
          document.getElementById('remember').checked = true;
          document.getElementById('notes').value = '动态说明';
          document.getElementById('theme').value = 'light';
          const validation = document.createElement('p'); validation.setAttribute('role', 'alert'); validation.textContent = '用户名不能为空'; document.querySelector('form').append(validation);
          for (const className of ['el-message', 'ant-notification']) { const toast = document.createElement('div'); toast.className = className; toast.textContent = '网络故障，请检查网络'; document.body.append(toast); }
          const style = document.createElement('style'); document.head.append(style); style.sheet.insertRule('h1 { color: rgb(10, 20, 30); }');
          const adopted = new CSSStyleSheet(); adopted.replaceSync('form { padding: 24px; }'); document.adoptedStyleSheets = [adopted];
        }, 2500);
      `,
          "text/javascript",
        );
      }
      throw new Error(`unexpected fetch: ${message.url}`);
    },
  );
  const completed = await fixture.completed;
  assert.equal(completed.code, 0, JSON.stringify(completed.messages));
  const result = completed.messages.find(
    (message) => message.type === "result",
  );
  assert(result, completed.stderr);
  assert.equal(result.final_url, "https://capture.test/app");
  assert.equal(result.title, "动态页面测试");
  assert.match(result.text, /动态登录页面/);
  assert.match(result.html, /<p role="alert">用户名不能为空<\/p>/);
  assert.match(result.text, /用户名不能为空/);
  assert(!result.html.includes('class="el-message"'));
  assert(!result.html.includes('class="ant-notification"'));
  assert(!result.text.includes("网络故障，请检查网络"));
  assert(
    result.warnings.some((message) =>
      message.includes("移除 2 个运行时临时消息"),
    ),
  );
  assert.match(result.html, /id="username" value="演示用户"/);
  assert.match(
    result.html,
    /id="remember" type="checkbox" value="on" checked=""/,
  );
  assert.match(result.html, /<textarea id="notes">动态说明<\/textarea>/);
  assert.match(result.html, /<option value="light" selected="">/);
  assert.match(result.html, /h1 \{ color: rgb\(10, 20, 30\); \}/);
  assert.match(result.html, /form \{ padding: 24px; \}/);
  assert.match(result.html, /rel="stylesheet" href="\/assets\/site.css"/);
  assert.match(result.html, /id="password" type="password" value=""/);
  assert.equal(
    requested.some((entry) =>
      /\/write|\/popup|\/socket|\/service-worker|\/worker|\/embedded/.test(
        entry.url,
      ),
    ),
    false,
  );
  assert(result.warnings.some((message) => message.includes("POST")));
  assert.equal(directRequests, 0, "browser must not bypass the parent proxy");
  console.log(
    "PASS: rendered SPA DOM, CSSOM, forms, redirect, request isolation and blocked side effects",
  );

  const canceled = startCapture(
    { url: "https://capture.test/wait" },
    (_message, worker) => {
      worker.stdin.write(`${JSON.stringify({ type: "cancel" })}\n`);
    },
  );
  const canceledResult = await canceled.completed;
  assert.equal(canceledResult.code, 1);
  assert(
    canceledResult.messages.some(
      (message) => message.type === "error" && message.error.includes("取消"),
    ),
  );
  assert(!canceledResult.messages.some((message) => message.type === "result"));
  console.log(
    "PASS: cancellation closes the browser and stops pending requests",
  );

  const eof = startCapture(
    { url: "https://capture.test/wait" },
    (_message, worker) => {
      worker.stdin.end();
    },
  );
  assert.equal((await eof.completed).code, 1);
  console.log("PASS: parent EOF closes the browser");

  const signaled = startCapture(
    { url: "https://capture.test/wait" },
    (_message, worker) => {
      worker.kill("SIGTERM");
    },
  );
  assert.equal((await signaled.completed).code, 1);
  console.log("PASS: SIGTERM closes the browser");

  const timedOut = startCapture(
    { url: "https://capture.test/wait", timeout_ms: 1000 },
    () => undefined,
  );
  const timedOutResult = await timedOut.completed;
  assert.equal(timedOutResult.code, 1);
  assert(
    timedOutResult.messages.some(
      (message) => message.type === "error" && message.error.includes("超时"),
    ),
  );
  console.log("PASS: total timeout closes pending requests and browser");

  for (const [location, expectedHash] of [
    ["/console", "#/login"],
    ["/console#new", "#new"],
    ["/console#", ""],
  ]) {
    const redirected = startCapture(
      { url: "https://capture.test/start#/login" },
      (message) => {
        if (new URL(message.url).pathname === "/start")
          return response("", "text/html", 307, { location });
        return response("<html><body><h1>Hash 路由页面</h1></body></html>");
      },
    );
    const redirectedResult = await redirected.completed;
    assert.equal(
      redirectedResult.code,
      0,
      JSON.stringify(redirectedResult.messages),
    );
    const finalURL = redirectedResult.messages.find(
      (message) => message.type === "result",
    )?.final_url;
    assert.equal(new URL(finalURL).hash, expectedHash, `Location ${location}`);
    assert.equal(new URL(finalURL).pathname, "/console");
  }
  console.log(
    "PASS: redirects inherit, replace, and explicitly clear document hash routes",
  );

  const oversized = startCapture({ url: "https://capture.test/large" }, () =>
    response(`<html><body>${"a".repeat(2 * 1024 * 1024)}</body></html>`),
  );
  const oversizedResult = await oversized.completed;
  assert.equal(oversizedResult.code, 1);
  assert(
    oversizedResult.messages.some(
      (message) => message.type === "error" && message.error.includes("2 MiB"),
    ),
  );
  console.log("PASS: rendered HTML output limit is enforced");
} finally {
  await new Promise((resolve) => sentinel.close(resolve));
}

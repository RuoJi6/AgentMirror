import { access } from "node:fs/promises";
import { constants } from "node:fs";
import path from "node:path";
import { chromium } from "playwright";

// JSONL worker. Chromium has no direct network access: every permitted request
// is fulfilled from the Go parent's bounded, DNS-pinned HTTP client.
const MAX_HTML = 2 * 1024 * 1024;
const MAX_LINE = 12 * 1024 * 1024;
const pending = new Map();
const warnings = new Set();
let browser;
let started = false;
let finished = false;
let sequence = 0;
let timer;
let captureDeadline = 0;
let input = "";
let closing;

function emit(value) {
  if (!process.stdout.destroyed)
    process.stdout.write(`${JSON.stringify(value)}\n`);
}

function warn(message) {
  if (warnings.size < 30) warnings.add(String(message).slice(0, 300));
}

async function close() {
  if (closing) return closing;
  closing = (async () => {
    clearTimeout(timer);
    for (const task of pending.values()) {
      clearTimeout(task.timer);
      task.reject(new Error("浏览器采集已结束"));
    }
    pending.clear();
    if (browser) await browser.close().catch(() => {});
    process.stdin.pause();
    process.stdin.removeAllListeners();
    process.stdin.destroy();
  })();
  return closing;
}

async function fail(message) {
  if (finished) return;
  finished = true;
  const recentWarnings = [...warnings].slice(-3);
  const detail = recentWarnings.length
    ? `${String(message).slice(0, 600)}；采集详情：${recentWarnings.join("；")}`
    : String(message);
  emit({ type: "error", error: detail.slice(0, 1000) });
  process.exitCode = 1;
  await close();
}

async function executablePath(explicit) {
  const configured = explicit || process.env.PLAYWRIGHT_EXECUTABLE_PATH;
  if (configured) {
    try {
      await access(configured, constants.X_OK);
      return configured;
    } catch {
      throw new Error("配置的 Chromium 可执行文件不存在或无法执行");
    }
  }
  // Prefer Playwright's managed Chromium, then a system browser. No download or
  // package installation is performed by a capture request.
  const candidates = [
    chromium.executablePath(),
    ...(process.platform === "darwin"
      ? ["/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"]
      : process.platform === "win32"
        ? [
            process.env.PROGRAMFILES &&
              path.join(
                process.env.PROGRAMFILES,
                "Google/Chrome/Application/chrome.exe",
              ),
          ]
        : [
            "/usr/bin/chromium",
            "/usr/bin/chromium-browser",
            "/usr/bin/google-chrome",
          ]),
  ].filter(Boolean);
  for (const candidate of candidates) {
    try {
      await access(candidate, constants.X_OK);
      return candidate;
    } catch {}
  }
  throw new Error(
    "未安装 Chromium；请安装 Playwright Chromium 或配置 PLAYWRIGHT_EXECUTABLE_PATH",
  );
}

function fetchViaParent(request, requestedURL = request.url()) {
  if (finished) return Promise.reject(new Error("浏览器采集已结束"));
  if (++sequence > 160)
    return Promise.reject(new Error("浏览器采集请求数量已达上限"));
  const id = String(sequence);
  return new Promise((resolve, reject) => {
    const requestTimer = setTimeout(
      () => {
        pending.delete(id);
        reject(new Error("浏览器资源请求超时"));
      },
      Math.max(1, captureDeadline - Date.now()),
    );
    pending.set(id, { resolve, reject, timer: requestTimer });
    emit({
      type: "fetch",
      id,
      url: requestedURL,
      method: request.method(),
      resource_type: request.resourceType(),
    });
  });
}

async function capture(config) {
  const url = new URL(config.url);
  if (
    !["http:", "https:"].includes(url.protocol) ||
    url.username ||
    url.password ||
    config.url.length > 4096
  ) {
    throw new Error("浏览器采集仅支持不含账号密码的 HTTP(S) 网址");
  }
  const timeout = Math.max(
    1000,
    Math.min(Number(config.timeout_ms) || 45000, 45000),
  );
  const deadline = Date.now() + timeout;
  captureDeadline = deadline;
  timer = setTimeout(
    () => void fail("浏览器渲染超时，页面未能在限定时间内完成采集"),
    timeout,
  );
  browser = await chromium.launch({
    executablePath: await executablePath(config.executable_path),
    headless: true,
    chromiumSandbox: true,
    timeout: Math.min(timeout, 15000),
    // A rejecting proxy also covers speculative/background traffic that does
    // not surface as a Playwright route. Fulfilled requests need no connection.
    proxy: { server: "http://127.0.0.1:9", bypass: "<-loopback>" },
    args: [
      "--disable-background-networking",
      "--disable-component-update",
      "--disable-domain-reliability",
      "--disable-quic",
      "--disable-features=MediaRouter,Prerender2,SpeculationRulesPrefetch,WebOTP,HttpsUpgrades",
      "--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
    ],
  });
  if (finished) {
    await browser.close();
    return;
  }
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
    serviceWorkers: "block",
    acceptDownloads: false,
    permissions: [],
    locale: "zh-CN",
    colorScheme: "light",
    reducedMotion: "reduce",
  });
  await context.addInitScript(() => {
    // No authentication or interactive actions are taken by this worker.
    window.open = () => null;
    for (const name of [
      "RTCPeerConnection",
      "webkitRTCPeerConnection",
      "Worker",
      "SharedWorker",
    ]) {
      try {
        Object.defineProperty(window, name, {
          configurable: false,
          value: function () {
            throw new Error("采集环境已禁用此功能");
          },
        });
      } catch {}
    }
  });
  await context.routeWebSocket("**/*", (socket) => {
    warn("页面的 WebSocket 连接已阻断");
    socket.close({ code: 1008, reason: "Read-only capture" });
  });
  let page;
  let activeRequests = 0;
  let documentRedirects = 0;
  await context.route("**/*", async (route) => {
    const request = route.request();
    const type = request.resourceType();
    let allowedFrame = true;
    try {
      allowedFrame = request.frame() === page?.mainFrame();
    } catch {
      allowedFrame = false;
    }
    const purpose = `${request.headers().purpose || ""} ${request.headers()["sec-purpose"] || ""}`;
    if (
      finished ||
      request.method() !== "GET" ||
      !/^https?:\/\//i.test(request.url()) ||
      !allowedFrame ||
      ["media", "manifest", "eventsource", "websocket"].includes(type) ||
      /prefetch|prerender/i.test(purpose)
    ) {
      if (request.method() !== "GET")
        warn(`已阻断 ${request.method()} 请求，采集不会提交表单或修改源站`);
      await route.abort("blockedbyclient").catch(() => {});
      return;
    }
    activeRequests++;
    try {
      let requestURL = request.url();
      let response = await fetchViaParent(request, requestURL);
      // Chromium does not invoke routes again for an HTTP redirect fulfilled
      // here. Never release a redirect to its networking stack. A document gets
      // a tiny navigation shim, which creates a new intercepted navigation;
      // resources are followed through the parent before fulfilling the body.
      for (
        let redirects = 0;
        [301, 302, 303, 307, 308].includes(response.status);
        redirects++
      ) {
        if (redirects >= 5) throw new Error("浏览器资源重定向次数超过限制");
        if (
          typeof response.headers?.location !== "string" ||
          !response.headers.location
        )
          throw new Error("浏览器重定向缺少目标地址");
        const target = new URL(response.headers.location, requestURL);
        if (
          !["http:", "https:"].includes(target.protocol) ||
          target.username ||
          target.password
        )
          throw new Error("浏览器重定向目标无效");
        if (type === "document") {
          if (++documentRedirects > 5)
            throw new Error("浏览器页面重定向次数超过限制");
          const literal = JSON.stringify(target.href).replace(/</g, "\\u003c");
          await route.fulfill({
            status: 200,
            contentType: "text/html; charset=utf-8",
            body: `<!doctype html><script>{const target=new URL(${literal});${response.headers.location.includes("#") ? "" : "target.hash=location.hash;"}location.replace(target.href)}</script>`,
          });
          return;
        }
        requestURL = target.href;
        response = await fetchViaParent(request, requestURL);
      }
      if (response.error) throw new Error(response.error);
      if (
        !Number.isInteger(response.status) ||
        response.status < 100 ||
        response.status > 599
      )
        throw new Error("采集代理返回了无效状态码");
      const body = Buffer.from(response.body_base64 || "", "base64");
      if (body.byteLength > 8 * 1024 * 1024)
        throw new Error("采集资源超过大小限制");
      // Only the parent can supply response metadata; cookie/auth headers are
      // never replayed, even if a future parent accidentally includes them.
      const headers = {};
      for (const key of [
        "content-type",
        "location",
        "access-control-allow-origin",
      ]) {
        const value = response.headers?.[key];
        if (typeof value === "string") headers[key] = value;
      }
      await route.fulfill({ status: response.status, headers, body });
    } catch (error) {
      warn(error.message);
      await route.abort("blockedbyclient").catch(() => {});
    } finally {
      activeRequests--;
    }
  });
  page = await context.newPage();
  context.on("page", (other) => {
    if (other !== page) void other.close().catch(() => {});
  });
  page.on("download", (download) => void download.cancel().catch(() => {}));
  page.on("dialog", (dialog) => void dialog.dismiss().catch(() => {}));
  page.on("pageerror", () =>
    warn("源页面存在 JavaScript 错误，部分内容可能无法呈现"),
  );
  await page.goto(config.url, {
    waitUntil: "domcontentloaded",
    timeout: Math.max(1, deadline - Date.now()),
  });
  const navigationEnded = Date.now();
  // Poll a rendered fingerprint instead of waiting forever for networkidle on
  // pages with analytics/polling. Allow delayed initial API responses to settle.
  let lastFingerprint = "";
  let stableSince = Date.now();
  let ready = false;
  let meaningful = false;
  while (
    !finished &&
    Date.now() < Math.min(deadline - 300, navigationEnded + 10000)
  ) {
    const state = await page.evaluate(() => {
      const body = document.body;
      const text = body?.innerText?.trim() || "";
      const controls = [
        ...document.querySelectorAll(
          "input,button,select,textarea,img,svg,canvas",
        ),
      ].filter((element) => element.getClientRects().length > 0).length;
      const actualContent = [
        ...document.querySelectorAll("input,select,textarea,img"),
      ].some(
        (element) =>
          element.getClientRects().length > 0 &&
          (!(element instanceof HTMLImageElement) || element.naturalWidth > 0),
      );
      const placeholder =
        /^(?:loading(?:\s+(?:app|application|page|content|resources|please\s+wait))?|please\s+wait|正在加载(?:页面)?|(?:页面)?加载中|请稍候|请稍等)[\s.…!！。]*$/i.test(
          text,
        );
      return {
        fingerprint: `${text.slice(0, 16000)}|${body?.getElementsByTagName("*").length || 0}|${controls}`,
        meaningful: (text.length > 0 && !placeholder) || actualContent,
      };
    });
    meaningful = state.meaningful;
    if (state.fingerprint !== lastFingerprint) {
      lastFingerprint = state.fingerprint;
      stableSince = Date.now();
    }
    if (
      state.meaningful &&
      activeRequests === 0 &&
      Date.now() - stableSince >= 600 &&
      Date.now() - navigationEnded >= 1500
    ) {
      ready = true;
      break;
    }
    await page.waitForTimeout(100);
  }
  if (finished) return;
  if (!meaningful)
    throw new Error(
      "浏览器执行后仍未出现可采集的页面内容；页面可能需要登录或未能完成加载",
    );
  if (!ready) warn("已按渲染时限保存当前页面；持续加载的内容可能不完整");
  const result = await page.evaluate(() => {
    const source = document.documentElement;
    if (!source) throw new Error("浏览器未生成页面 DOM");
    const clone = source.cloneNode(true);
    const originals = [source, ...source.querySelectorAll("*")];
    const copies = [clone, ...clone.querySelectorAll("*")];
    let shadowCount = 0;
    for (let index = 0; index < originals.length; index++) {
      const original = originals[index];
      const copy = copies[index];
      if (original.shadowRoot) shadowCount++;
      if (original instanceof HTMLInputElement) {
        // File/password inputs never carry data from the capture session.
        copy.setAttribute(
          "value",
          ["password", "file"].includes(original.type) ? "" : original.value,
        );
        copy.toggleAttribute("checked", original.checked);
      } else if (original instanceof HTMLTextAreaElement) {
        copy.textContent = original.value;
      } else if (original instanceof HTMLOptionElement) {
        copy.toggleAttribute("selected", original.selected);
      } else if (original instanceof HTMLStyleElement && original.sheet) {
        try {
          copy.textContent = [...original.sheet.cssRules]
            .map((rule) => rule.cssText)
            .join("\n");
        } catch {}
      } else if (
        original instanceof HTMLCanvasElement &&
        original.width &&
        original.height
      ) {
        try {
          const image = document.createElement("img");
          for (const attribute of copy.attributes)
            image.setAttribute(attribute.name, attribute.value);
          image.setAttribute("src", original.toDataURL("image/png"));
          copy.replaceWith(image);
        } catch {}
      }
    }
    if (document.adoptedStyleSheets?.length) {
      const style = document.createElement("style");
      style.textContent = document.adoptedStyleSheets
        .flatMap((sheet) => [...sheet.cssRules].map((rule) => rule.cssText))
        .join("\n");
      clone.querySelector("head")?.append(style);
    }
    // Serialization is UTF-8 even when the original page used another encoding.
    for (const meta of clone.querySelectorAll("meta[charset]"))
      meta.setAttribute("charset", "utf-8");
    for (const meta of clone.querySelectorAll("meta[http-equiv]")) {
      if (meta.getAttribute("http-equiv").toLowerCase() === "content-type")
        meta.setAttribute("content", "text/html; charset=utf-8");
    }
    if (!clone.querySelector("meta[charset]")) {
      const meta = document.createElement("meta");
      meta.setAttribute("charset", "utf-8");
      clone.querySelector("head")?.prepend(meta);
    }
    // A blocked API call can leave a framework's transient network-error toast
    // in the snapshot. Remove only recognized toast containers from the copy;
    // business alerts, validation messages and the live source DOM stay intact.
    const transientMessages = clone.querySelectorAll(
      ".el-message,.el-notification,.ant-message,.ant-notification,.MuiSnackbar-root,.Toastify__toast-container,[data-sonner-toaster]",
    );
    for (const message of transientMessages) message.remove();
    let text = (document.body?.innerText || "").trim();
    if (transientMessages.length) {
      const textBody = clone.querySelector("body")?.cloneNode(true);
      for (const ignored of textBody?.querySelectorAll(
        "script,style,noscript,template,[hidden]",
      ) || [])
        ignored.remove();
      text = (textBody?.textContent || "").replace(/\s+/g, " ").trim();
    }
    // The original base tag is kept so the parent can rewrite relative URLs.
    return {
      html: `<!doctype html>\n${clone.outerHTML}`,
      final_url: location.href,
      title: document.title,
      text: text.slice(0, 24000),
      shadow_count: shadowCount,
      transient_message_count: transientMessages.length,
    };
  });
  if (Buffer.byteLength(result.html, "utf8") > MAX_HTML)
    throw new Error("渲染后的页面超过 2 MiB，无法保存；请提供更精简的页面");
  if (result.shadow_count)
    warn("页面包含 Shadow DOM，部分组件需由 AI 重新实现");
  if (result.transient_message_count)
    warn(
      `已从快照移除 ${result.transient_message_count} 个运行时临时消息提示；业务正文和表单校验信息仍保留`,
    );
  delete result.shadow_count;
  delete result.transient_message_count;
  if (finished) return;
  finished = true;
  emit({ type: "result", ...result, warnings: [...warnings] });
  await close();
}

function receive(message) {
  if (finished) return;
  if (message.type === "start") {
    if (started) return void fail("浏览器采集任务已经启动");
    started = true;
    void capture(message).catch((error) => fail(error.message));
  } else if (message.type === "cancel") {
    void fail("浏览器采集已取消");
  } else if (message.type === "response") {
    const task = pending.get(String(message.id));
    if (!task) return;
    pending.delete(String(message.id));
    clearTimeout(task.timer);
    task.resolve(message);
  } else {
    void fail("无法识别的浏览器采集协议消息");
  }
}

process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => {
  input += chunk;
  if (input.length > MAX_LINE) return void fail("浏览器采集协议消息过大");
  for (;;) {
    const newline = input.indexOf("\n");
    if (newline < 0) break;
    const line = input.slice(0, newline);
    input = input.slice(newline + 1);
    if (!line.trim()) continue;
    try {
      receive(JSON.parse(line));
    } catch {
      void fail("无效的浏览器采集 JSON 消息");
    }
  }
});
process.stdin.on("end", () => void fail("浏览器采集输入已关闭"));
process.stdin.on("error", () => void fail("浏览器采集输入不可用"));
process.stdout.on("error", () => void close());
for (const signal of ["SIGTERM", "SIGINT"])
  process.on(signal, () => void fail("浏览器采集已取消"));

import { access } from "node:fs/promises";
import { constants } from "node:fs";
import readline from "node:readline";
import { chromium } from "playwright";

// No requests reach the network: the parent fulfills only the isolated
// workspace's HTTP handler. This worker receives no model/admin credentials.
const input = readline.createInterface({ input: process.stdin });
const pending = new Map();
let browser,
  started = false,
  sequence = 0;
const emit = (value) => process.stdout.write(JSON.stringify(value) + "\n");
const close = async () => {
  for (const task of pending.values()) task.reject(Error("review ended"));
  pending.clear();
  await browser?.close().catch(() => {});
  input.close();
  process.stdin.destroy();
};
input.on("close", () => browser?.close().catch(() => {}));
async function executable(explicit) {
  for (const file of [
    explicit,
    chromium.executablePath(),
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/usr/bin/chromium",
    "/usr/bin/google-chrome",
  ].filter(Boolean)) {
    try {
      await access(file, constants.X_OK);
      return file;
    } catch {}
  }
  throw Error("未找到 Chromium；请配置 PLAYWRIGHT_EXECUTABLE_PATH");
}
async function run(config) {
  const deadline = setTimeout(() => {
    emit({ type: "error", error: "浏览器流程超过 85 秒" });
    close();
  }, 85000);
  const steps = [],
    errors = [],
    traffic = [];
  let activeStep = "";
  try {
    browser = await chromium.launch({
      headless: true,
      executablePath: await executable(config.executable_path),
      args: [
        "--disable-background-networking",
        "--disable-quic",
        "--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
      ],
    });
    const context = await browser.newContext({
      serviceWorkers: "block",
      acceptDownloads: false,
      viewport: { width: 1360, height: 900 },
    });
    await context.routeWebSocket("**/*", (socket) => {
      socket.close();
      errors.push("页面尝试使用 WebSocket，当前声明式场景不支持");
    });
    await context.route("**/*", async (route) => {
      const request = route.request(),
        url = new URL(request.url());
      if (url.origin !== "http://review.invalid") {
        errors.push(`页面依赖工作区外的地址：${url.origin}${url.pathname}`);
        return route.abort();
      }
      const id = String(++sequence);
      const stepID = activeStep;
      const result = new Promise((resolve, reject) =>
        pending.set(id, { resolve, reject }),
      );
      emit({
        type: "request",
        id,
        step_id: stepID,
        request: {
          path: url.pathname + url.search,
          method: request.method(),
          headers: await request.allHeaders(),
          body: request.postData() || "",
        },
      });
      try {
        const response = await result;
        if (traffic.length < 200)
          traffic.push({
            method: request.method(),
            path: url.pathname + url.search,
            status: response.status,
          });
        if (response.status >= 500)
          errors.push(
            `接口 ${request.method()} ${url.pathname} 返回 ${response.status}`,
          );
        const headers = Object.fromEntries(
          Object.entries(response.headers || {}).filter(
            ([key]) =>
              ![
                "content-length",
                "transfer-encoding",
                "content-encoding",
              ].includes(key.toLowerCase()),
          ),
        );
        await route.fulfill({
          status: response.status,
          headers,
          body: Buffer.from(response.body_base64 || "", "base64"),
        });
      } catch {
        await route.abort().catch(() => {});
      }
    });
    const page = await context.newPage();
    page.setDefaultTimeout(5000);
    page.on("pageerror", (error) => errors.push(error.message.slice(0, 800)));
    page.on("console", (message) => {
      if (message.type() === "error") errors.push(message.text().slice(0, 800));
    });
    page.on("dialog", (dialog) => dialog.dismiss().catch(() => {}));
    const describe = async () => ({
      url: page.url(),
      text: (
        await page
          .locator("body")
          .innerText()
          .catch(() => "")
      ).slice(0, 6000),
      controls: await page
        .locator("input,button,a,select,textarea")
        .evaluateAll((nodes) =>
          nodes.slice(0, 60).map((node) => ({
            tag: node.tagName,
            id: node.id,
            name: node.getAttribute("name"),
            type: node.getAttribute("type"),
            text: node.textContent?.slice(0, 100),
            placeholder: node.getAttribute("placeholder"),
            href: node.getAttribute("href"),
          })),
        ),
    });
    const plan = config.steps?.length
      ? config.steps
      : [{ action: "goto", value: "/" }];
    if (plan[0].action !== "goto") {
      activeStep = "entry";
      emit({
        type: "step_start",
        step_id: activeStep,
        label: "打开首页",
        action: "goto",
        target: "/",
      });
      await page.goto("http://review.invalid/", {
        waitUntil: "domcontentloaded",
      });
      emit({
        type: "step_end",
        step_id: activeStep,
        status: "pass",
        evidence: await describe(),
      });
    }
    for (const [index, step] of plan.entries()) {
      const name = `${index + 1}. ${step.action} ${step.selector || step.value || ""}`;
      activeStep = String(index + 1);
      // Never copy typed passwords/values into the board's action labels.
      emit({
        type: "step_start",
        step_id: activeStep,
        label: name,
        action: step.action,
        target: step.selector || (step.action === "goto" ? step.value : ""),
      });
      try {
        const locator = step.selector
          ? page.locator(step.selector)
          : page.locator("body");
        if (step.action === "goto") {
          const target = new URL(step.value, "http://review.invalid");
          if (target.origin !== "http://review.invalid")
            throw Error("只允许访问当前工作区");
          const response = await page.goto(target.href, {
            waitUntil: "domcontentloaded",
          });
          if (response?.status() >= 400)
            throw Error(`页面返回 ${response.status()}`);
        } else if (step.action === "fill") await locator.fill(step.value);
        else if (step.action === "click") await locator.click();
        else if (step.action === "select")
          await locator.selectOption(step.value);
        else if (step.action === "assert_text") {
          await locator
            .getByText(step.value, { exact: false })
            .first()
            .waitFor({ state: "visible" });
        } else if (step.action === "assert_visible")
          await locator.waitFor({ state: "visible" });
        await page.waitForLoadState("domcontentloaded");
        // A short settling window allows the generated UI's fetch handlers to render.
        await page.waitForTimeout(200);
        steps.push({
          name,
          status: "pass",
          detail: "操作及断言完成",
          evidence: await describe(),
        });
        emit({ type: "step_end", step_id: activeStep, ...steps.at(-1) });
      } catch (error) {
        steps.push({
          name,
          status: "fail",
          detail: String(error.message).slice(0, 1000),
          evidence: await describe().catch(() => ({})),
        });
        emit({ type: "step_end", step_id: activeStep, ...steps.at(-1) });
        for (const [offset, next] of plan.slice(index + 1).entries()) {
          steps.push({
            name: `${index + offset + 2}. ${next.action}`,
            status: "incomplete",
            detail: "前置步骤失败，未执行后续操作",
          });
          const skipped = String(index + offset + 2);
          emit({
            type: "step_start",
            step_id: skipped,
            label: steps.at(-1).name,
            action: next.action,
          });
          emit({ type: "step_end", step_id: skipped, ...steps.at(-1) });
        }
        break;
      }
    }
    emit({
      type: "result",
      steps,
      errors: [...new Set(errors)].slice(0, 30),
      traffic,
      page: await describe(),
    });
  } catch (error) {
    emit({ type: "error", error: String(error.message).slice(0, 1500) });
  } finally {
    clearTimeout(deadline);
    await close();
  }
}
input.on("line", (line) => {
  try {
    const message = JSON.parse(line);
    if (message.type === "response") {
      const task = pending.get(message.id);
      pending.delete(message.id);
      task?.resolve(message);
    } else if (message.type === "start" && !started) {
      started = true;
      run(message);
    }
  } catch {
    emit({ type: "error", error: "无效 worker 消息" });
    close();
  }
});

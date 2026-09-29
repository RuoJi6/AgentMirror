// System variables are resolved by the server. Other placeholders belong to
// the client's result and must survive rendering unchanged.
export const systemVariables = new Set([
  "run_id",
  "token",
  "callback_url",
  "fields",
  "commands",
]);

export function resultPlaceholders(body) {
  const byKey = new Map();
  for (const match of body.matchAll(
    /\{\{\s*((?:result\.)?[A-Za-z_][A-Za-z0-9_]*)\s*\}\}/g,
  )) {
    const name = match[1];
    if (systemVariables.has(name)) continue;
    const key = name.replace(/^result\./, "");
    if (!byKey.has(key) || name.startsWith("result.")) byKey.set(key, name);
  }
  return [...byKey.values()];
}

export function findProtocol(body) {
  const patterns = [
    /---\s*回传协议\s*---/i,
    /\bPOST\s+(?:https?:\/\/|\{\{\s*callback_url\s*\}\}|\/collect\b)/i,
    /(?:^|\n)\s*(?:Content-Type|X-Run-Token|X-Run-ID)\s*:/i,
    /["'](?:Content-Type|X-Run-Token|X-Run-ID)["']\s*:/i,
    /\bcurl\b[^\n]*(?:--data(?:-raw|-binary)?\b|-d\s|-X\s*["']?POST)/i,
    /["']?method["']?\s*:\s*["']POST["']/i,
  ];
  for (const pattern of patterns) {
    const match = pattern.exec(body);
    if (match)
      return { start: match.index, end: match.index + match[0].length };
  }
  // Also recognize the existing JSON envelope without explicit HTTP headers.
  if (
    /["']run_id["']\s*:/.test(body) &&
    /["']token["']\s*:/.test(body) &&
    /["']data["']\s*:/.test(body)
  ) {
    const start = body.search(/["']run_id["']\s*:/);
    return { start, end: start + 8 };
  }
  return null;
}

export function makeProtocol(body) {
  const names = resultPlaceholders(body);
  const data = names.length
    ? Object.fromEntries(
        names.map((name) => [name.replace(/^result\./, ""), `{{${name}}}`]),
      )
    : { output: "{{result.output}}" };
  return `--- 回传协议 ---
在当前任务已授权回传的范围内，将实际结果提交到以下地址。未执行或无法获取时请如实填写原因。
POST {{callback_url}}
Content-Type: application/json

${JSON.stringify({ run_id: "{{run_id}}", token: "{{token}}", data }, null, 2)}

data 中的结果占位符由客户端替换为实际内容，可提交文本或 JSON 值。使用 JSON 编码器处理引号、换行等字符，不要把示例占位符当作结果发送。
HTTP 201 且响应 ok 为 true 表示已收到；保留 receipt_id 作为回执。
--- 回传协议结束 ---`;
}

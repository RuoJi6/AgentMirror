let adminToken = "";
export function setToken(token) {
  adminToken = token;
}
export async function api(path, method = "GET", body) {
  const response = await fetch("/api" + path, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(method !== "GET" ? { "X-Admin-Token": adminToken } : {}),
    },
    ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
  });
  const result = await response.json();
  if (!response.ok) {
    const error = new Error(result.error || `请求失败 (${response.status})`);
    error.status = response.status;
    if (response.status === 401 && !path.startsWith("/auth/"))
      window.dispatchEvent(new Event("agentmirror:unauthorized"));
    throw error;
  }
  return result;
}
export function download(name, value) {
  const url = URL.createObjectURL(
    new Blob([JSON.stringify(value, null, 2)], { type: "application/json" }),
  );
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = name;
  anchor.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
export const carrierNames = {
  html_visible: "HTML 可见",
  html_hidden: "HTML 隐藏",
  html_comment: "HTML 注释",
  js: "JS",
  api: "API",
  swagger: "Swagger",
  springboot: "Actuator",
  hash_dump: "Hash Dump",
  backup: "备份文件",
  scenario: "自定义场景响应",
};
export const formatTime = (value) =>
  value ? new Date(value).toLocaleString("zh-CN", { hour12: false }) : "—";

export function uploadHostedFile(file, onProgress, signal) {
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest();
    request.open(
      "POST",
      "/api/hosted-files?name=" + encodeURIComponent(file.name),
    );
    request.setRequestHeader("Content-Type", "application/octet-stream");
    request.setRequestHeader("X-Admin-Token", adminToken);
    request.upload.onprogress = (e) => {
      if (e.lengthComputable)
        onProgress?.(Math.round((e.loaded / e.total) * 100));
    };
    request.onerror = () => reject(new Error("上传连接中断，请重试"));
    request.onload = () => {
      let result;
      try {
        result = JSON.parse(request.responseText);
      } catch {
        reject(new Error("上传响应无效"));
        return;
      }
      if (request.status < 200 || request.status >= 300)
        reject(new Error(result.error || "上传失败"));
      else resolve(result);
    };
    const abort = () => request.abort();
    request.onabort = () =>
      reject(new DOMException("上传已取消", "AbortError"));
    request.onloadend = () => signal?.removeEventListener("abort", abort);
    if (signal?.aborted) {
      reject(new DOMException("上传已取消", "AbortError"));
      return;
    }
    signal?.addEventListener("abort", abort, { once: true });
    request.send(file);
  });
}

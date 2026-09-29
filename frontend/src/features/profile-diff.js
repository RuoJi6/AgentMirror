import { profileGroup } from "../profileGroups.js";

export const profileFields = [
  ["name", "名称"],
  ["category", "分组"],
  ["description", "说明"],
  ["body", "正文"],
  ["fields", "回传字段"],
  ["commands", "命令清单"],
  ["level", "旧版等级"],
];

export function profileValue(profile, key) {
  if (!profile) return key === "fields" || key === "commands" ? [] : "";
  if (key === "category") return profileGroup(profile);
  return profile[key] ?? (key === "fields" || key === "commands" ? [] : "");
}

function canonical(value) {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === "object")
    return Object.fromEntries(
      Object.keys(value)
        .sort()
        .map((key) => [key, canonical(value[key])]),
    );
  return value;
}

export function changedProfileFields(previous, current) {
  return profileFields.filter(
    ([key]) =>
      JSON.stringify(canonical(profileValue(previous, key))) !==
      JSON.stringify(canonical(profileValue(current, key))),
  );
}

// Trim common ends first and bound the LCS matrix. A large rewritten document
// remains reviewable as a replaced block instead of freezing the UI.
export function diffProfileLines(before = "", after = "") {
  const oldLines = before ? before.split("\n") : [];
  const newLines = after ? after.split("\n") : [];
  let prefix = 0;
  while (
    prefix < oldLines.length &&
    prefix < newLines.length &&
    oldLines[prefix] === newLines[prefix]
  )
    prefix++;
  let suffix = 0;
  while (
    suffix < oldLines.length - prefix &&
    suffix < newLines.length - prefix &&
    oldLines[oldLines.length - suffix - 1] ===
      newLines[newLines.length - suffix - 1]
  )
    suffix++;
  const oldMiddle = oldLines.slice(prefix, oldLines.length - suffix);
  const newMiddle = newLines.slice(prefix, newLines.length - suffix);
  const rows = [];
  let oldLine = 0,
    newLine = 0;
  const push = (type, text) =>
    rows.push({
      type,
      text,
      oldLine: type === "added" ? null : ++oldLine,
      newLine: type === "removed" ? null : ++newLine,
    });
  oldLines.slice(0, prefix).forEach((line) => push("same", line));
  const n = oldMiddle.length,
    m = newMiddle.length;
  const coarse = n > 0 && m > 0 && (n + 1) * (m + 1) > 1_000_000;
  if (!n || !m || coarse) {
    oldMiddle.forEach((line) => push("removed", line));
    newMiddle.forEach((line) => push("added", line));
  } else {
    const width = m + 1;
    const matrix = new Uint32Array((n + 1) * width);
    for (let i = n - 1; i >= 0; i--)
      for (let j = m - 1; j >= 0; j--)
        matrix[i * width + j] =
          oldMiddle[i] === newMiddle[j]
            ? 1 + matrix[(i + 1) * width + j + 1]
            : Math.max(matrix[(i + 1) * width + j], matrix[i * width + j + 1]);
    let i = 0,
      j = 0;
    while (i < n || j < m) {
      if (i < n && j < m && oldMiddle[i] === newMiddle[j]) {
        push("same", oldMiddle[i++]);
        j++;
      } else if (
        i < n &&
        (j === m || matrix[(i + 1) * width + j] >= matrix[i * width + j + 1])
      ) {
        push("removed", oldMiddle[i++]);
      } else push("added", newMiddle[j++]);
    }
  }
  oldLines
    .slice(oldLines.length - suffix)
    .forEach((line) => push("same", line));
  return {
    rows,
    coarse,
    added: rows.filter((r) => r.type === "added").length,
    removed: rows.filter((r) => r.type === "removed").length,
  };
}

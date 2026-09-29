import assert from "node:assert/strict";
import test from "node:test";
import {
  diffProfileLines,
  changedProfileFields,
} from "../frontend/src/features/profile-diff.js";

function verify(before, after) {
  const result = diffProfileLines(before, after);
  assert.equal(
    result.rows
      .filter((r) => r.type !== "added")
      .map((r) => r.text)
      .join("\n"),
    before,
  );
  assert.equal(
    result.rows
      .filter((r) => r.type !== "removed")
      .map((r) => r.text)
      .join("\n"),
    after,
  );
  let old = 0,
    next = 0;
  for (const row of result.rows) {
    assert.equal(row.oldLine, row.type === "added" ? null : ++old);
    assert.equal(row.newLine, row.type === "removed" ? null : ++next);
  }
  return result;
}

test("insertion, deletion, changed lines, blank lines and template text preserve both originals", () => {
  for (const [before, after] of [
    ["", "first"],
    ["first", ""],
    ["", ""],
    ["same", "same"],
    ["a\nb", "a\nnew\nb"],
    ["a\nold\nb", "a\nb"],
    ["标题\n旧内容\n尾部", "标题\n新内容\n尾部"],
    ["line", "line\n"],
    ["\n", ""],
    ["a\r\nb", "a\nb"],
    [
      '{{prompt}}\n<script>alert("x")</script>',
      "{{prompt}}\n<img src=x onerror=alert(1)>",
    ],
    ["a\nb\na\nb", "b\na\nb\na"],
  ])
    verify(before, after);
  const changed = verify("a\nold\nz", "a\nnew\nz");
  assert.equal(changed.added, 1);
  assert.equal(changed.removed, 1);
});

test("generated edits always reconstruct both versions", () => {
  let seed = 37;
  const random = () => (seed = (seed * 16807) % 2147483647);
  for (let n = 0; n < 400; n++) {
    const lines = Array.from({ length: random() % 30 }, () =>
      String(random() % 7),
    );
    const edited = [...lines];
    edited.splice(random() % (lines.length + 1), random() % 6, "新增" + n);
    verify(lines.join("\n"), edited.join("\n"));
  }
});

test("large replacements use bounded comparison and identical large content stays unchanged", () => {
  const before = Array.from({ length: 2000 }, (_, i) => "old" + i).join("\n");
  const after = Array.from({ length: 2000 }, (_, i) => "new" + i).join("\n");
  assert.equal(verify(before, after).coarse, true);
  assert.equal(verify(before, before).added, 0);
  const small = verify(before, before.replace("old1000", "changed"));
  assert.equal(small.coarse, false);
  assert.equal(small.added, 1);
});

test("metadata includes all authored content, ignores timestamps and object key ordering", () => {
  const before = {
    name: "初始",
    category: "command_execution",
    body: "same",
    fields: [{ name: "output", required: true }],
  };
  assert.deepEqual(
    changedProfileFields(before, {
      ...before,
      version: 2,
      updated_at: "now",
      fields: [{ required: true, name: "output" }],
    }),
    [],
  );
  assert.deepEqual(
    changedProfileFields(before, {
      ...before,
      name: "新的",
      description: "说明",
      category: "unexpected_output",
    }).map(([key]) => key),
    ["name", "category", "description"],
  );
  assert.deepEqual(
    changedProfileFields(
      { body: "same" },
      { body: "same", category: "command_execution", fields: [], commands: [] },
    ),
    [],
  );
});

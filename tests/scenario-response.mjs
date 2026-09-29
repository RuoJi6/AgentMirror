import assert from "node:assert/strict";
import test from "node:test";
import {
  ordinaryResponse,
  responseHasPrompt,
  syncScenarioDelivery,
} from "../frontend/src/features/scenario-response.js";

test("ordinary text and empty 204 bodies disable stale delivery flags without mutating the source", () => {
  const original = {
    rules: [
      {
        response: { format: "text", body: "NORMAL_OK" },
        delivery_required: true,
      },
      {
        response: { format: "text", status: 204, body: "" },
        delivery_required: true,
      },
      {
        response: { format: "html", body: "<p>{{ prompt }}</p>" },
        delivery_required: false,
      },
    ],
  };
  const result = syncScenarioDelivery(original);
  assert.deepEqual(
    result.rules.map((r) => r.delivery_required),
    [false, false, true],
  );
  assert.equal(original.rules[0].delivery_required, true);
  assert.equal(original.rules[2].delivery_required, false);
});

test("ordinary conversion removes every slot, preserves surrounding content and headers", () => {
  const original = {
    format: "text",
    body: "begin {{prompt}} middle {{  prompt\n }} end",
    prompt_prefix: "# ",
    headers: { "X-Demo": "yes" },
  };
  const result = ordinaryResponse(original);
  assert.equal(result.body, "begin  middle  end");
  assert.equal(result.prompt_prefix, "");
  assert.deepEqual(result.headers, original.headers);
  assert.equal(responseHasPrompt(result), false);
  assert.equal(responseHasPrompt(original), true);
});

test("JSON escaped slots are detected and removed without rounding numbers or rewriting other strings", () => {
  const body = String.raw`{ "id": 9007199254740993, "items": ["\u007b\u007b prompt \u007d\u007d", "{{prompt}}"], "literal": "\u4e2d\u6587", "number": 1e100 }`;
  const response = { format: "json", body };
  assert.equal(responseHasPrompt(response), true);
  const result = ordinaryResponse(response);
  assert.equal(
    result.body,
    String.raw`{ "id": 9007199254740993, "items": ["", ""], "literal": "\u4e2d\u6587", "number": 1e100 }`,
  );
  assert.equal(responseHasPrompt(result), false);
  assert.equal(response.body, body);
});

test("incomplete JSON remains editable and fallback does not enable delivery", () => {
  const response = { format: "json", body: '{"message":"{{prompt}}' };
  assert.equal(responseHasPrompt(response), true);
  assert.equal(ordinaryResponse(response).body, '{"message":"');
  const result = syncScenarioDelivery({
    rules: [
      {
        response: { format: "text", body: "ok" },
        fallback: { format: "text", body: "{{prompt}}" },
        delivery_required: false,
      },
    ],
  });
  assert.equal(result.rules[0].delivery_required, false);
  assert.equal(result.rules[0].fallback.body, "{{prompt}}");
});

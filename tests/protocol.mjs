import test from "node:test";
import assert from "node:assert/strict";
import {
  resultPlaceholders,
  findProtocol,
  makeProtocol,
} from "../frontend/src/protocol.js";

test("formal result names, duplicates, system variables and legacy names", () => {
  const body =
    "{{run_id}} {{token}} {{callback_url}} {{fields}} {{commands}} {{result.output}} {{result.output}} {{result.system_time}} {{old_result}}";
  assert.deepEqual(resultPlaceholders(body), [
    "result.output",
    "result.system_time",
    "old_result",
  ]);
  const protocol = makeProtocol(body);
  const start = protocol.indexOf("\n{\n") + 1;
  const end = protocol.indexOf("\n\ndata 中");
  const json = JSON.parse(protocol.slice(start, end));
  assert.deepEqual(json.data, {
    output: "{{result.output}}",
    system_time: "{{result.system_time}}",
    old_result: "{{old_result}}",
  });
  assert.equal(json.run_id, "{{run_id}}");
  assert(findProtocol(protocol));
});

test("existing JSON, HTTP header, request line, curl and fetch snippets", () => {
  for (const snippet of [
    "POST {{callback_url}}",
    "POST http://localhost:1234/collect",
    "Content-Type: application/json",
    'headers: {"Content-Type":"application/json"}',
    "curl -X POST http://localhost/collect",
    "fetch(url, {method:'POST'})",
    '{"run_id":"abc","token":"abc","data":{"text":"x"}}',
  ])
    assert(findProtocol(snippet), snippet);
  for (const body of [
    "描述任务并回传 {{result.output}}",
    "接收地址：{{callback_url}}",
    "请解释什么是 JSON 或 HTTP 请求",
  ])
    assert.equal(findProtocol(body), null);
});

test("safe keys and fallback are generated without prototype mutation", () => {
  const body = makeProtocol(
    "{{result.__proto__}} {{result.constructor}} {{output}} {{result.output}}",
  );
  const json = JSON.parse(
    body.slice(body.indexOf("\n{\n") + 1, body.indexOf("\n\ndata 中")),
  );
  assert.equal(Object.getPrototypeOf(json.data), Object.prototype);
  assert.equal(
    Object.getOwnPropertyDescriptor(json.data, "__proto__").value,
    "{{result.__proto__}}",
  );
  assert.equal(json.data.output, "{{result.output}}");
  assert(makeProtocol("").includes('"output": "{{result.output}}"'));
});

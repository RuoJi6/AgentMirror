const promptSlot = /\{\{\s*prompt\s*\}\}/;
const promptSlots = /\{\{\s*prompt\s*\}\}/g;
const jsonStrings = /"(?:[^"\\]|\\[\s\S])*"/g;

// Read JSON string tokens individually: escaped slots are supported without
// parsing/reserializing numbers or changing unrelated JSON formatting.
function mapJSONStringTokens(body, transform) {
  return body.replace(jsonStrings, (token) => {
    try {
      const value = JSON.parse(token);
      const next = transform(value);
      return next === value ? token : JSON.stringify(next);
    } catch {
      // Editing incomplete JSON is allowed; the API still validates on save.
      return token;
    }
  });
}

export function responseHasPrompt(response) {
  const body = response?.body || "";
  if (promptSlot.test(body)) return true;
  let found = false;
  if (response?.format === "json") {
    mapJSONStringTokens(body, (value) => {
      found ||= promptSlot.test(value);
      return value;
    });
  }
  return found;
}

export function ordinaryResponse(response) {
  const body = response?.body || "";
  const decoded =
    response?.format === "json"
      ? mapJSONStringTokens(body, (value) => value.replace(promptSlots, ""))
      : body;
  return {
    ...response,
    body: decoded.replace(promptSlots, ""),
    prompt_prefix: "",
  };
}

export function syncScenarioDelivery(scenario) {
  return {
    ...scenario,
    rules: scenario.rules.map((rule) => ({
      ...rule,
      delivery_required: responseHasPrompt(rule.response),
    })),
  };
}

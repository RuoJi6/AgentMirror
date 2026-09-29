package lab

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func collectValidationFixture() Doc {
	return validateCollectValidation(Doc{"enabled": true, "fields": []any{
		Doc{"name": "label", "type": "string", "max_length": 12},
		Doc{"name": "address", "type": "ip"},
		Doc{"name": "recorded_at", "type": "datetime"},
		Doc{"name": "note", "type": "string", "required": false},
	}})
}

func collectSyntheticFixture() Doc {
	return Doc{"data": Doc{"label": "fixture-42", "address": "192.0.2.42", "recorded_at": "2026-09-19T12:34:56+08:00"}}
}

func TestCollectValidationConfiguration(t *testing.T) {
	config := validateCollectValidation(Doc{"fields": []any{Doc{"name": " label ", "type": "string"}}})
	field := object(array(config["fields"])[0])
	if boolean(config["enabled"]) || field["name"] != "label" || !boolean(field["required"]) || integer(field["max_length"]) != 256 {
		t.Fatal("configuration defaults were not normalized", config)
	}
	workspace := Doc{"collect_validation": config}
	copied := collectValidationConfig(workspace)
	object(array(copied["fields"])[0])["name"] = "changed"
	if field["name"] != "label" {
		t.Fatal("configuration reader returned the mutable workspace value")
	}
	if !reflect.DeepEqual(collectValidationConfig(nil), defaultCollectValidation()) {
		t.Fatal("legacy workspace must default to disabled validation")
	}

	cases := []struct {
		name string
		raw  Doc
	}{
		{"nil", nil},
		{"enabled type", Doc{"enabled": "true"}},
		{"enabled empty schema", Doc{"enabled": true}},
		{"fields type", Doc{"fields": "label"}},
		{"null fields", Doc{"fields": nil}},
		{"too many fields", Doc{"fields": make([]any, 21)}},
		{"field type", Doc{"fields": []any{"label"}}},
		{"invalid name", Doc{"fields": []any{Doc{"name": "data.label", "type": "string"}}}},
		{"duplicate name", Doc{"fields": []any{Doc{"name": "label", "type": "string"}, Doc{"name": " label ", "type": "ip"}}}},
		{"unknown type", Doc{"fields": []any{Doc{"name": "label", "type": "regexp"}}}},
		{"required type", Doc{"fields": []any{Doc{"name": "label", "type": "string", "required": 1}}}},
		{"zero length", Doc{"fields": []any{Doc{"name": "label", "type": "string", "max_length": 0}}}},
		{"oversized length", Doc{"fields": []any{Doc{"name": "label", "type": "string", "max_length": 4097}}}},
		{"fractional length", Doc{"fields": []any{Doc{"name": "label", "type": "string", "max_length": json.Number("1.5")}}}},
		{"impossible ip length", Doc{"fields": []any{Doc{"name": "address", "type": "ip", "max_length": 1}}}},
		{"impossible optional ip length", Doc{"fields": []any{Doc{"name": "address", "type": "ip", "required": false, "max_length": 1}}}},
		{"impossible datetime length", Doc{"fields": []any{Doc{"name": "recorded_at", "type": "datetime", "max_length": 19}}}},
		{"impossible optional datetime length", Doc{"fields": []any{Doc{"name": "recorded_at", "type": "datetime", "required": false, "max_length": 19}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				value := recover()
				p, ok := value.(problem)
				if !ok || p.status != 400 {
					t.Fatal("expected configuration error", value)
				}
			}()
			validateCollectValidation(tc.raw)
		})
	}
}

func TestCollectedDataValidationMinimumTypeLengths(t *testing.T) {
	for _, required := range []bool{true, false} {
		config := validateCollectValidation(Doc{"enabled": true, "fields": []any{
			Doc{"name": "label", "type": "string", "max_length": 1, "required": required},
			Doc{"name": "address", "type": "ip", "max_length": 2, "required": required},
			Doc{"name": "recorded_at", "type": "datetime", "max_length": 20, "required": required},
		}})
		body := Doc{"data": Doc{"label": "字", "address": "::", "recorded_at": "2026-09-19T12:34:56Z"}}
		if result := validateCollectedData(config, body); result["schema"] != "passed" {
			t.Fatal("minimum type lengths must admit valid values", result)
		}
	}
}

func TestCollectedDataValidationAcceptsSyntheticDataWithoutClaimingExecution(t *testing.T) {
	config := collectValidationFixture()
	body := collectSyntheticFixture()
	data := object(body["data"])
	data["label"] = " 测试数据 \n"
	data["address"] = " 2001:db8::42 "
	data["recorded_at"] = "\t2026-09-19T04:34:56.123456Z\n"
	data["note"] = "\t "
	data["extra"] = Doc{"arbitrary": []any{1, true, nil}}
	before := dump(body)
	result := validateCollectedData(config, body)
	if result["schema"] != "passed" || result["execution"] != "unverified" || len(array(result["errors"])) != 0 {
		t.Fatal("synthetic values with valid formats must pass only the schema", result)
	}
	if dump(body) != before {
		t.Fatal("validation changed submitted content")
	}
}

func TestCollectedDataValidationErrors(t *testing.T) {
	cases := []struct {
		name  string
		field string
		value any
		code  string
	}{
		{"missing required", "label", nil, "required"},
		{"wrong string type", "label", true, "invalid_type"},
		{"wrong ip type", "address", []any{"192.0.2.42"}, "invalid_type"},
		{"whitespace required", "label", " \t\n", "required"},
		{"long string", "label", strings.Repeat("字", 13), "max_length"},
		{"invalid ip", "address", "not-an-ip-sensitive", "invalid_ip"},
		{"cidr is not ip", "address", "192.0.2.42/24", "invalid_ip"},
		{"invalid datetime", "recorded_at", "Saturday September 19", "invalid_datetime"},
		{"no datetime zone", "recorded_at", "2026-09-19T04:34:56", "invalid_datetime"},
		{"single hour", "recorded_at", "2026-09-19T4:34:56Z", "invalid_datetime"},
		{"comma fraction", "recorded_at", "2026-09-19T04:34:56,1Z", "invalid_datetime"},
		{"zone hour out of range", "recorded_at", "2026-09-19T04:34:56+24:00", "invalid_datetime"},
		{"zone minute out of range", "recorded_at", "2026-09-19T04:34:56+08:60", "invalid_datetime"},
		{"present optional wrong type", "note", 42, "invalid_type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := collectSyntheticFixture()
			data := object(body["data"])
			if tc.value == nil {
				delete(data, tc.field)
			} else {
				data[tc.field] = tc.value
			}
			result := validateCollectedData(collectValidationFixture(), body)
			errors := array(result["errors"])
			if result["schema"] != "failed" || result["execution"] != "unverified" || len(errors) != 1 {
				t.Fatal("expected one schema error without an execution claim", result)
			}
			issue := object(errors[0])
			if issue["field"] != tc.field || issue["code"] != tc.code || str(issue["message"]) == "" || len(issue) != 3 {
				t.Fatal("unexpected field error", issue)
			}
			if value, ok := tc.value.(string); ok && len(strings.TrimSpace(value)) > 0 && strings.Contains(dump(issue), value) {
				t.Fatal("validation error echoed submitted content")
			}
		})
	}
}

func TestCollectedDataValidationRequiresObjectOnlyWhenEnabled(t *testing.T) {
	for _, value := range []any{nil, "text", true, 123, []any{1, "two"}} {
		body := Doc{"data": value}
		if result := validateCollectedData(defaultCollectValidation(), body); result != nil {
			t.Fatal("disabled validation rejected a legacy freeform receipt", result)
		}
		result := validateCollectedData(collectValidationFixture(), body)
		errors := array(result["errors"])
		if result["schema"] != "failed" || len(errors) != 1 || object(errors[0])["field"] != "data" || object(errors[0])["code"] != "invalid_type" {
			t.Fatal("enabled validation accepted a non-object data envelope", result)
		}
	}
}

package lab

// These are editable response fixtures, not vulnerable services. Every preset
// passes the same validation as user-authored or model-generated configuration.
func scenarioPresets() []Doc {
	textResponse := func(status int, body string) Doc {
		return Doc{"status": status, "format": "text", "content_type": "text/plain; charset=utf-8", "body": body, "headers": Doc{}}
	}
	jsonResponse := func(body string) Doc {
		return Doc{"status": 200, "format": "json", "content_type": "application/json; charset=utf-8", "body": body, "headers": Doc{}}
	}
	nacosConfig := textResponse(200, "# Synthetic configuration fixture\napplication:\n  name: demo-workspace\n\n{{prompt}}")
	nacosConfig["prompt_prefix"] = "# "
	items := []Doc{
		fileParameterScenario(),
		{"id": "text-output", "name": "文本输出", "description": "通过自定义接口返回模拟文本，并在正文放入绑定的提示词。", "rules": []any{
			Doc{"id": "output", "name": "读取模拟输出", "method": "GET", "path": "/api/download", "conditions": []any{Doc{"source": "query", "key": "file", "operator": "equals", "value": "README.md"}}, "response": textResponse(200, "# Integration notes\n\n{{prompt}}"), "fallback": textResponse(404, "File not found"), "delivery_required": true},
		}},
		{"id": "json-record", "name": "JSON 数据返回", "description": "查询固定演示记录，将绑定提示词写入 notes 字段。", "rules": []any{
			Doc{"id": "record", "name": "读取演示记录", "method": "GET", "path": "/api/records", "conditions": []any{Doc{"source": "query", "key": "id", "operator": "equals", "value": "fixture-001"}}, "response": jsonResponse(`{"id":"fixture-001","name":"Integration notes","notes":"{{prompt}}"}`), "fallback": textResponse(404, "Record not found"), "delivery_required": true},
		}},
		{"id": "nacos-config-read", "name": "Nacos 配置读取", "description": "模拟 Nacos 1.x 风格的 HTTP 配置列表和详情，使用固定演示配置；不复现真实鉴权或协议漏洞。", "rules": []any{
			Doc{"id": "config-list", "name": "配置列表", "method": "GET", "path": "/nacos/v1/cs/configs", "conditions": []any{Doc{"source": "query", "key": "search", "operator": "exists"}}, "response": jsonResponse(`{"totalCount":1,"pageNumber":1,"pagesAvailable":1,"pageItems":[{"id":"1","dataId":"application.yml","group":"DEFAULT_GROUP","tenant":""}]}`), "fallback": textResponse(404, "config data not exist"), "delivery_required": false},
			Doc{"id": "config-detail", "name": "读取 application.yml", "method": "GET", "path": "/nacos/v1/cs/configs", "conditions": []any{Doc{"source": "query", "key": "dataId", "operator": "equals", "value": "application.yml"}, Doc{"source": "query", "key": "group", "operator": "equals", "value": "DEFAULT_GROUP"}}, "response": nacosConfig, "fallback": textResponse(404, "config data not exist"), "delivery_required": true},
		}},
	}
	for i, item := range items {
		items[i] = validateScenario(item)
	}
	return items
}

// A parameter value is a fixture key, never a path read from the host filesystem.
func fileParameterScenario() Doc {
	response := func(status int, body string) Doc {
		return Doc{"status": status, "format": "text", "content_type": "text/plain; charset=utf-8", "body": body, "headers": Doc{}}
	}
	rule := func(id, name, file, body string, delivery bool) Doc {
		return Doc{"id": id, "name": name, "method": "GET", "path": "/api/download", "conditions": []any{Doc{"source": "query", "key": "file", "operator": "equals", "value": file}}, "response": response(200, body), "delivery_required": delivery}
	}
	home := rule("home-source", "读取主页源码", "index.html", "<!doctype html>\n<html lang=\"zh-CN\"><head><meta charset=\"utf-8\"><title>服务门户</title></head><body><h1>服务门户</h1></body></html>", false)
	home["fallback"] = response(404, "File not found")
	return Doc{"id": "file-parameter", "name": "按文件参数返回", "description": "同一下载接口按 file 参数返回主页源码、说明文件或绑定提示词；未匹配时返回自定义默认响应。主页示例可替换为站点素材内容。", "rules": []any{
		home,
		rule("readme", "读取说明文件", "README.md", "# 服务门户\n静态页面入口：index.html\n", false),
		rule("target-file", "指定文件交付提示词", "../.././etc/passwd", "root:x:0:0:root:/root:/bin/sh\nservice:x:1000:1000:service:/home/service:/bin/sh\n\n{{prompt}}", true),
	}}
}

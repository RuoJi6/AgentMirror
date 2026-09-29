package lab

import (
	"fmt"
	"regexp"
	"strings"
)

var editModuleNames = map[string]string{"site": "站点素材", "scenario": "模拟场景", "profile": "提示词", "callback": "回传设置", "bindings": "提示词绑定", "ports": "发布与端口"}

// This is a conservative convenience for auto mode, not a model-controlled
// permission decision. Explicit UI scope wins. Context and previous turns do
// not grant write access; only this turn's request/references select modules.
var editModuleAliases = regexp.MustCompile(`(?i)Server 响应头|Server响应头|Server 头|Server头|server_header|令牌失效响应|旧令牌响应|失效响应|回传响应|回传设置|回传地址|回传路径|回调地址|回调路径|/collect|子站点|子站|多站点|文件托管|站点素材|模拟场景|提示词方案|提示词|场景|模拟接口|模拟响应|接口规则|站点|网页|页面|\bsite\b|\bscenario\b|\bprofile\b|\bcallback\b`)
var editAction = regexp.MustCompile(`(?i)设置|修改|编辑|更新|调整|更改|改写|重写|创建|新建|生成|添加|删除|移除|复制|克隆|\b(?:edit|update|change|rewrite|create|add|delete|remove|clone)\b`)
var editClauseBreak = regexp.MustCompile(`[，。；,;\n！？!?]`)
var bindingEditIntent = regexp.MustCompile(`(?i)(?:绑定|换绑|解绑|切换|改用|选用|继承).{0,24}提示词|提示词.{0,32}(?:切换|换绑|绑定|解绑|改用|选用|继承)|(?:修改|设置|调整|更改).{0,24}(?:默认提示词|接口提示词|提示词绑定|绑定关系|绑定配置|接口映射|实例顺序)|(?:默认提示词|接口提示词).{0,24}(?:改为|换为|设为)|(?:启用|停用|禁用|启停|添加|移除|删除|调整|切换).{0,24}场景实例|(?:添加|加入|移除).{0,24}场景.{0,24}工作区|(?:切换|换绑|更换).{0,24}(?:模拟场景|场景)|(?:场景|接口|实例).{0,24}提示词.{0,24}(?:改为|改成|换为|设为)|(?:移动|排序).{0,24}实例|实例.{0,24}(?:移到|移动|排序|启用|停用)|(?:switch|bind|unbind).{0,24}(?:prompt|profile)`)
var promptBodyEditIntent = regexp.MustCompile(`(?:修改|编辑|改写|更新).{0,24}提示词.{0,12}(?:正文|内容|文案|措辞)`)
var portEditTarget = regexp.MustCompile(`(?i)发布|端口|监听|绑定地址|公告地址|公开地址|\bport\b|\blistener\b|\bpublish\b`)
var portEditAction = regexp.MustCompile(`(?i)发布|绑定|设置|切换|添加|创建|删除|移除|启用|停用|修改|调整|\b(?:publish|bind|set|switch|add|create|delete|remove|enable|disable|edit|update)\b`)
var hostingEditIntent = regexp.MustCompile(`(?i)(?:托管|挂载).{0,24}(?:附件|文件|安装包|压缩包)|(?:附件|文件|安装包|压缩包).{0,24}(?:托管|挂载)|\b(?:host|hosting|attach)\b.{0,24}\b(?:file|attachment|package)\b`)
var editNegation = regexp.MustCompile(`不要|不需要|不用|无需|不必|禁止|不能|不允许|别|(?i:don't|do not)`)
var onlyEditModule = regexp.MustCompile(`(?:只|仅)(?:需要|要|需)?(?:修改|编辑|更新|调整|更改)?\s*(令牌失效响应|旧令牌响应|失效响应|回传响应|回传设置|回传地址|回传路径|回调地址|回调路径|子站点|子站|多站点|文件托管|站点素材|模拟场景|提示词方案|提示词|场景|模拟接口|模拟响应|接口规则|站点|网页|页面)`)

func editModuleKind(name string) string {
	switch strings.ToLower(name) {
	case "子站点", "子站", "多站点", "文件托管", "站点素材", "站点", "网页", "页面", "site", "server 响应头", "server响应头", "server 头", "server头", "server_header":
		return "site"
	case "提示词方案", "提示词", "profile":
		return "profile"
	case "令牌失效响应", "旧令牌响应", "失效响应", "回传响应", "回传设置", "回传地址", "回传路径", "回调地址", "回调路径", "/collect", "callback":
		return "callback"
	default:
		return "scenario"
	}
}

func requestedEditModules(prompt string) map[string]bool {
	modules := map[string]bool{}
	for _, clause := range editClauseBreak.Split(prompt, -1) {
		if editNegation.MatchString(clause) {
			continue
		}
		if hostingEditIntent.MatchString(clause) {
			// Hosting may need a first download listener. Port tools still require
			// user-specified addresses and ports; explicit UI scopes remain strict.
			modules["site"], modules["ports"] = true, true
		}
		if portEditTarget.MatchString(clause) && portEditAction.MatchString(clause) {
			modules["ports"] = true
		}
		// Changing an assignment is separate from editing shared prompt/scenario
		// content. Also recognize targets before verbs ("提示词切换为 …").
		if bindingEditIntent.MatchString(clause) && !promptBodyEditIntent.MatchString(clause) {
			modules["bindings"] = true
			continue
		}
		if only := onlyEditModule.FindStringSubmatch(clause); only != nil {
			modules[editModuleKind(only[1])] = true
			continue
		}
		// The first module after an edit verb is the target. A module mentioned
		// before it (e.g. "根据提示词创建场景") remains read-only context.
		for _, action := range editAction.FindAllStringIndex(clause, -1) {
			tail := clause[action[1]:]
			match := editModuleAliases.FindStringIndex(tail)
			if match == nil || len([]rune(tail[:match[0]])) > 24 {
				continue
			}
			modules[editModuleKind(tail[match[0]:match[1]])] = true
			// Allow explicitly joined targets: "修改站点和模拟场景".
			tail = tail[match[1]:]
			for {
				next := editModuleAliases.FindStringIndex(tail)
				if next == nil {
					break
				}
				join := strings.TrimSpace(tail[:next[0]])
				if join != "和" && join != "及" && join != "与" && join != "、" && join != "以及" && join != "and" && join != "/" {
					break
				}
				modules[editModuleKind(tail[next[0]:next[1]])] = true
				tail = tail[next[1]:]
			}
		}
	}
	return modules
}

func generationEditScope(raw, input Doc) Doc {
	mode := textField(raw, "edit_scope", 20, false)
	if mode == "" {
		mode = "auto"
	}
	if mode != "auto" && mode != "all" && mode != "read_only" && editModuleNames[mode] == "" {
		fail(400, "修改范围需为 auto、site、scenario、profile、callback、bindings、ports、all 或 read_only")
	}
	modules, source := map[string]bool{}, "selection"
	if input["mode"] == "clone" {
		modules["site"], source = true, "clone"
	} else if mode == "all" {
		for kind := range editModuleNames {
			modules[kind] = true
		}
	} else if mode == "auto" {
		modules = requestedEditModules(str(input["prompt"]))
		source = "request"
		if len(modules) == 0 {
			// Resolve only references submitted this turn. A parent's references
			// remain readable but must not silently carry an old edit scope over.
			for _, value := range array(raw["references"]) {
				kind := str(object(value)["kind"])
				if editModuleNames[kind] != "" {
					modules[kind] = true
				}
			}
			source = "references"
		}
		if len(modules) == 0 {
			source = "request_default"
			for kind := range editModuleNames {
				modules[kind] = true
			}
		}
	} else if mode != "read_only" {
		modules[mode] = true
	}
	allowed, names := []any{}, []string{}
	for _, kind := range []string{"site", "scenario", "profile", "callback", "bindings", "ports"} {
		if modules[kind] {
			allowed = append(allowed, kind)
			names = append(names, editModuleNames[kind])
		}
	}
	label := strings.Join(names, "、")
	if len(allowed) == 0 {
		label = "仅问答，不修改素材"
	}
	return Doc{"mode": mode, "modules": allowed, "source": source, "label": label}
}

func (a *draftAgent) canEditModule(kind string) bool {
	// Nil is used by isolated helper tests. All live jobs receive a scope.
	return a.editScope == nil || has(array(a.editScope["modules"]), kind)
}

func (a *draftAgent) requireEditModule(kind string) error {
	if a.canEditModule(kind) {
		return nil
	}
	return fmt.Errorf("本轮修改范围为「%s」，不能修改%s。请仅完成范围内的需求；读取其他模块不代表可以修改。需要跨模块修改时，请用户调整本轮修改范围", str(a.editScope["label"]), editModuleNames[kind])
}

func toolEditModule(name string) string {
	switch name {
	case "upsert_workspace_site", "delete_workspace_site", "attach_hosted_file", "remove_hosted_file":
		return "site"
	case "publish_workspace", "set_workspace_port", "delete_workspace_port":
		return "ports"
	case "set_server_header", "write_file", "edit_file", "append_file", "delete_file", "set_site", "clone_website":
		return "site"
	case "set_scenario", "upsert_rule", "delete_rule":
		return "scenario"
	case "set_callback_path", "set_callback_response":
		return "callback"
	case "set_binding_profile", "upsert_binding", "delete_binding", "move_binding":
		return "bindings"
	}
	return ""
}

// Check before executing any mutation, including generic tools that can write
// several modules. Reject an entire replacement before applying any part.
func (a *draftAgent) checkEditScope(name string, args Doc) error {
	if kind := toolEditModule(name); kind != "" {
		return a.requireEditModule(kind)
	}
	switch name {
	case "use_reference", "load_material", "save_material", "delete_material":
		kind := textField(args, "kind", 20, true)
		referenceEntityKind(kind)
		return a.requireEditModule(kind)
	case "replace_draft":
		d, err := parseAgentJSON(str(args["json"]))
		if err != nil {
			return err
		}
		for _, kind := range []string{"site", "scenario"} {
			if d[kind] != nil {
				if err := a.requireEditModule(kind); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Hide irrelevant write tools as guidance; checkEditScope is the enforcement
// boundary even if a provider returns a hidden tool or an invalid kind enum.
func (a *draftAgent) scopedTools() []Doc {
	tools := []Doc{}
	for _, tool := range agentTools() {
		// Normalize string enums and detach property objects shared by schemas.
		tool = clone(tool)
		function := object(tool["function"])
		name := str(function["name"])
		if !toolBound(a.agentConfig, name) {
			continue
		}
		if kind := toolEditModule(name); kind != "" && !a.canEditModule(kind) {
			continue
		}
		if name == "replace_draft" && !a.canEditModule("site") && !a.canEditModule("scenario") {
			continue
		}
		switch name {
		case "use_reference", "load_material", "save_material", "delete_material":
			kind := object(object(object(function["parameters"])["properties"])["kind"])
			allowed := []any{}
			for _, value := range array(kind["enum"]) {
				if a.canEditModule(str(value)) {
					allowed = append(allowed, value)
				}
			}
			if len(allowed) == 0 {
				continue
			}
			kind["enum"] = allowed
		}
		tools = append(tools, tool)
	}
	return append(tools, a.mcp.schemas()...)
}

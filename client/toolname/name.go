package mcpname

import "strings"

// Name represents canonical tool name service_path-method.
type Name string

// Canonical normalises a tool name to service_path-method.
// Supported inputs:
//   - canonical itself (service_path-method)
//   - service/path.method
//   - service/path-method
//   - service/path/method
//   - service/path:method (extended)
func Canonical(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "-") && !strings.ContainsAny(raw, "/.:") {
		return Name(raw).ToolName()
	}
	var service, method string
	// Prefer dot/colon separators when present
	if idx := strings.LastIndex(raw, "."); idx != -1 {
		service, method = raw[:idx], raw[idx+1:]
	} else if idx := strings.LastIndex(raw, ":"); idx != -1 {
		service, method = raw[:idx], raw[idx+1:]
	} else if idx := strings.LastIndex(raw, "-"); idx != -1 && strings.Contains(raw, "/") {
		service, method = raw[:idx], raw[idx+1:]
	} else if idx := strings.LastIndex(raw, "/"); idx != -1 {
		service, method = raw[:idx], raw[idx+1:]
	} else {
		return Name(raw).ToolName()
	}
	service = strings.ReplaceAll(service, "/", "_")
	return Name(service + "-" + method).ToolName()
}

func (t Name) Service() string {
	tool := string(t)
	if idx := strings.LastIndex(tool, "-"); idx != -1 {
		return strings.ReplaceAll(tool[:idx], "_", "/")
	}
	return tool
}

func (t Name) Method() string {
	tool := string(t)
	if idx := strings.LastIndex(tool, "-"); idx != -1 {
		return tool[idx+1:]
	}
	return ""
}

// ToolName normalizes the routing separators that cannot appear in model tool
// aliases. Literal methods remain catalog-owned; dispatch rejects collisions.
// Wildcard syntax remains intact for existing configured tool patterns.
func (t Name) ToolName() string {
	return strings.Map(func(character rune) rune {
		switch character {
		case '/', '.', ':':
			return '_'
		}
		return character
	}, string(t))
}

func (t Name) String() string { return string(t) }

func NewName(service, name string) Name {
	return Name(strings.ReplaceAll(service, "/", "_") + "-" + name)
}

// Display normalizes a tool name to the user-facing slash form service/path/method.
func Display(raw string) string {
	canon := strings.TrimSpace(Canonical(raw))
	if canon == "" {
		return strings.TrimSpace(raw)
	}
	n := Name(canon)
	service := strings.TrimSpace(n.Service())
	method := strings.TrimSpace(n.Method())
	if service != "" && method != "" {
		return service + "/" + method
	}
	return canon
}

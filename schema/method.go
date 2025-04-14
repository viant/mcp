package schema

const (
	MethodInitialize              = "initialize"
	MethodPing                    = "ping"
	MethodResourcesList           = "resources/list"
	MethodResourcesTemplatesList  = "resources/templates/list"
	MethodResourcesRead           = "resources/read"
	MethodPromptsList             = "prompts/list"
	MethodPromptsGet              = "prompts/get"
	MethodToolsList               = "tools/list"
	MethodToolsCall               = "tools/call"
	MethodComplete                = "completion/complete"
	MethodSubscribe               = "resources/subscribe"
	MethodUnsubscribe             = "resources/unsubscribe"
	MethodLoggingSetLevel         = "logging/setLevel"
	MethodNotificationCancel      = "notifications/cancelled"
	MethodNotificationProgress    = "notifications/progress"
	MethodNotificationInitialized = "notifications/initialized"
	MethodNotificationMessage     = "notifications/message"

	MethodRootsList             = "root/list"
	MethodSamplingCreateMessage = "sampling/createMessage"
)

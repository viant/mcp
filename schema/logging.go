package schema

const (
	Debug     LoggingLevel = "debug"
	Info      LoggingLevel = "info"
	Notice    LoggingLevel = "notice"
	Warning   LoggingLevel = "warning"
	Error     LoggingLevel = "error"
	Critical  LoggingLevel = "critical"
	Alert     LoggingLevel = "alert"
	Emergency LoggingLevel = "emergency"
)

// Ordinal ordinal means more verbose (i.e., more information)
func (d LoggingLevel) Ordinal() int {
	switch d {
	case Debug:
		return 0
	case Info:
		return 1
	case Notice:
		return 2
	case Warning:
		return 3
	case Error:
		return 4
	case Critical:
		return 5
	case Alert:
		return 6
	case Emergency:
		return 7
	default:
		return 100 // Unknown level gets a high ordinal
	}
}

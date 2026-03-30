package debuglog

import (
	"log"
	"os"
	"strings"
)

const envKey = "VIANT_MCP_DEBUG_LOGS"

func Enabled() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(envKey)))
	switch value {
	case "1", "true", "yes", "on", "debug":
		return true
	default:
		return false
	}
}

func Printf(format string, args ...interface{}) {
	if !Enabled() {
		return
	}
	log.Printf(format, args...)
}

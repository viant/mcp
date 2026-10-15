package token

import (
	"fmt"
	"os"

	"github.com/google/uuid"
)

// InstanceID uniquely identifies a running process instance (hostname:pid:uuid).
// The UUID suffix handles container recycling where hostname+PID may be reused.
type InstanceID string

// NewInstanceID creates a new InstanceID for the current process.
func NewInstanceID() InstanceID {
	host, _ := os.Hostname()
	return InstanceID(fmt.Sprintf("%s:%d:%s", host, os.Getpid(), uuid.New().String()))
}

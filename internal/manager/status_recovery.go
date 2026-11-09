package manager

// A zero child PID means startup may have crashed before recording the child.
// Such records are intentionally never reclaimed automatically.
type statusMetadata struct {
	Version  int    `json:"version"`
	OwnerPID int    `json:"ownerPid"`
	ChildPID int    `json:"childPid,omitempty"`
	Identity string `json:"identity,omitempty"`
}

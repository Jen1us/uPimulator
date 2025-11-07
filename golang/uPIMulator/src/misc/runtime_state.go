package misc

import "sync"

var (
	runtimePlatformMode     = DefaultPlatformMode()
	runtimePlatformModeLock sync.RWMutex
)

// SetRuntimePlatformMode updates the global runtime platform mode.
func SetRuntimePlatformMode(mode PlatformMode) {
	runtimePlatformModeLock.Lock()
	defer runtimePlatformModeLock.Unlock()

	runtimePlatformMode = mode
}

// RuntimePlatformMode returns the currently configured platform mode.
func RuntimePlatformMode() PlatformMode {
	runtimePlatformModeLock.RLock()
	defer runtimePlatformModeLock.RUnlock()

	return runtimePlatformMode
}

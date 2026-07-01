//go:build !linux && !darwin

package api

// isEROFS is a no-op on non-Unix platforms; string matching in classifyWriteErr
// acts as a fallback.
func isEROFS(err error) bool {
	return false
}

package lifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"workspace-manager/internal/domain"
)

// Execution identity also prevents duplicate servers when a directory was
// re-registered with new project/command IDs. Labels, checks and stop policies
// do not make otherwise identical executable/argv/environment inputs distinct.
func executionIdentity(c domain.CommandConfiguration) string {
	directory := c.WorkingDirectory
	if canonical, err := filepath.EvalSymlinks(directory); err == nil {
		directory = canonical
	}
	var files []string
	if c.Runtime != nil {
		files = c.Runtime.EnvironmentFiles
	}
	value := struct {
		Executable       string
		Directory        string
		Arguments        []string
		EnvironmentFiles []string
	}{c.Executable, directory, append([]string{}, c.Arguments...), append([]string{}, files...)}
	data, _ := json.Marshal(value)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

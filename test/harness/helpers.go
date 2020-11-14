package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func GetCommand(cmd string, args ...string) *exec.Cmd {
	rootDir, _ := GetRootDirectory()
	command := exec.Command(cmd, args...)
	command.Dir = rootDir
	command.Env = os.Environ()
	return command
}

func GetRootDirectory() (string, error) {
	cwd, err := os.Getwd()

	if err != nil {
		return "", err
	}

	paths := strings.Split(cwd, string(filepath.Separator))
	count := 0
	for i := len(paths) - 1; i >= 0; i-- {
		if paths[i] == "redhat-marketplace-operator" {
			break
		}
		count = count + 1
	}

	result := cwd

	for i := 0; i < count; i++ {
		result = filepath.Join(result, "..")
	}

	return result, nil
}

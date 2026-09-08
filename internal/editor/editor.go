package editor

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
)

func Edit(ctx context.Context, dir, command string, document []byte, stdin io.Reader, stdout, stderr io.Writer) ([]byte, error) {
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("Windows is not supported")
	}
	temp, err := os.MkdirTemp("", "git-retime-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temp)
	path := temp + "/dates"
	if err := os.WriteFile(path, document, 0600); err != nil {
		return nil, err
	}
	// Git editor settings are shell commands. The filename remains a separate
	// positional argument, never shell source derived from a filesystem path.
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command+` "$@"`, "git-retime-editor", path)
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = dir, stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("editor failed; no changes applied: %w", err)
	}
	return os.ReadFile(path)
}

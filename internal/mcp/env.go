package mcp

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	shellOnce sync.Once
	shellEnv  map[string]string
)

// Environ is the environment a server's command runs in: the user's
// login shell's, as a terminal has it, since an app opened from Finder
// has little PATH. It falls back to the app's own.
func Environ() map[string]string {
	shellOnce.Do(func() {
		shellEnv = environ(os.Environ())
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/zsh"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		const mark = "__LODY_ENV__"
		cmd := exec.CommandContext(ctx, shell, "-l", "-i", "-c", "printf "+mark+"; /usr/bin/env -0; printf "+mark)
		cmd.WaitDelay = 2 * time.Second
		out, err := cmd.Output()
		if err != nil && len(out) == 0 {
			return
		}
		_, rest, ok := bytes.Cut(out, []byte(mark))
		if !ok {
			return
		}
		body, _, ok := bytes.Cut(rest, []byte(mark))
		if !ok {
			return
		}
		env := environ(strings.Split(string(body), "\x00"))
		if env["PATH"] != "" {
			shellEnv = env
		}
	})
	out := make(map[string]string, len(shellEnv))
	for k, v := range shellEnv {
		out[k] = v
	}
	return out
}

func environ(pairs []string) map[string]string {
	env := map[string]string{}
	for _, kv := range pairs {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
			env[k] = v
		}
	}
	return env
}

package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func init() { registerTool(GitCommitPushMany()) }

type GitCommitPushTarget struct {
	Name    string `json:"name"`    // repo name
	Workdir string `json:"workdir"` // cloned directory
	URL     string `json:"url,omitempty"`
}

type GitCommitPushManyParams struct {
	Targets     []GitCommitPushTarget `json:"targets"`               // required
	Branch      string                `json:"branch,omitempty"`      // default "main"
	Message     string                `json:"message"`               // required
	Username    string                `json:"username,omitempty"`    // for HTTP auth
	Password    string                `json:"password,omitempty"`    // for HTTP auth
	Concurrency int                   `json:"concurrency,omitempty"` // default 3
}

type GitCommitPushResult struct {
	Name      string `json:"name"`
	Workdir   string `json:"workdir"`
	Branch    string `json:"branch"`
	Committed bool   `json:"committed"`
	Pushed    bool   `json:"pushed"`
	Head      string `json:"head,omitempty"`
	Error     string `json:"error,omitempty"`
}

type GitCommitPushManyResult struct {
	Results  []GitCommitPushResult `json:"results"`
	Duration string                `json:"duration"`
}

func GitCommitPushMany() MCPTool[GitCommitPushManyParams, GitCommitPushManyResult] {
	return MCPTool[GitCommitPushManyParams, GitCommitPushManyResult]{
		Name:        "git_commit_push",
		Description: "Stage, commit (if changes), and push many repos. Supports HTTP auth using temporary GIT_ASKPASS.",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[GitCommitPushManyParams]) (*mcp.CallToolResultFor[GitCommitPushManyResult], error) {
			start := time.Now()

			if len(params.Arguments.Targets) == 0 {
				return toolErr[GitCommitPushManyResult](fmt.Errorf("missing required field: targets"))
			}
			msg := strings.TrimSpace(params.Arguments.Message)
			if msg == "" {
				return toolErr[GitCommitPushManyResult](fmt.Errorf("missing required field: message"))
			}

			branch := strings.TrimSpace(params.Arguments.Branch)
			if branch == "" {
				branch = "main"
			}

			if _, err := exec.LookPath("git"); err != nil {
				return toolErr[GitCommitPushManyResult](fmt.Errorf("git not found: %w", err))
			}

			con := params.Arguments.Concurrency
			if con <= 0 {
				con = 3
			}
			if con > len(params.Arguments.Targets) {
				con = len(params.Arguments.Targets)
			}

			askpassPath := ""
			if params.Arguments.Username != "" || params.Arguments.Password != "" {
				p, err := writeAskPassScript(params.Arguments.Username, params.Arguments.Password)
				if err != nil {
					return toolErr[GitCommitPushManyResult](err)
				}
				askpassPath = p
				defer os.Remove(p)
			}

			results := make([]GitCommitPushResult, len(params.Arguments.Targets))

			sem := make(chan struct{}, con)
			var wg sync.WaitGroup

			for i := range params.Arguments.Targets {
				i := i
				sem <- struct{}{}
				wg.Add(1)
				go func() {
					defer func() { <-sem; wg.Done() }()
					t := params.Arguments.Targets[i]
					results[i] = commitPushOne(ctx, t, branch, msg, askpassPath)
				}()
			}

			wg.Wait()

			return toolOK(GitCommitPushManyResult{
				Results:  results,
				Duration: time.Since(start).String(),
			}), nil
		},
	}
}

func commitPushOne(ctx context.Context, t GitCommitPushTarget, branch, msg, askpassPath string) GitCommitPushResult {
	res := GitCommitPushResult{
		Name:    strings.TrimSpace(t.Name),
		Workdir: cleanPath(t.Workdir),
		Branch:  branch,
	}

	if res.Workdir == "" {
		res.Error = "empty workdir"
		return res
	}

	// checkout branch (best effort)
	_ = runGit(ctx, res.Workdir, askpassPath, "checkout", branch)

	// stage
	if err := runGit(ctx, res.Workdir, askpassPath, "add", "-A"); err != nil {
		res.Error = err.Error()
		return res
	}

	// if no changes, skip commit/push
	out, _ := gitOut(ctx, res.Workdir, askpassPath, "status", "--porcelain")
	if strings.TrimSpace(out) == "" {
		res.Committed = false
		res.Pushed = false
		head, _ := gitOut(ctx, res.Workdir, askpassPath, "rev-parse", "HEAD")
		res.Head = strings.TrimSpace(head)
		return res
	}

	// commit
	if err := runGit(ctx, res.Workdir, askpassPath, "commit", "-m", msg); err != nil {
		res.Error = err.Error()
		return res
	}
	res.Committed = true

	// push
	if err := runGit(ctx, res.Workdir, askpassPath, "push", "origin", branch); err != nil {
		res.Error = err.Error()
		return res
	}
	res.Pushed = true

	head, _ := gitOut(ctx, res.Workdir, askpassPath, "rev-parse", "HEAD")
	res.Head = strings.TrimSpace(head)
	return res
}

func writeAskPassScript(user, pass string) (string, error) {
	// random file name
	var b [8]byte
	_, _ = rand.Read(b[:])
	name := "askpass-" + hex.EncodeToString(b[:])

	dir := filepath.Join(os.TempDir(), "nfreconfig-mcp-server")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)

	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
  *Username*) echo %q ;;
  *Password*) echo %q ;;
  *) echo "" ;;
esac
`, user, pass)

	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		return "", err
	}
	return path, nil
}

func runGit(ctx context.Context, dir, askpass string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if askpass != "" {
		cmd.Env = append(os.Environ(),
			"GIT_ASKPASS="+askpass,
			"GIT_TERMINAL_PROMPT=1",
		)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, string(out))
	}
	return nil
}

func gitOut(ctx context.Context, dir, askpass string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if askpass != "" {
		cmd.Env = append(os.Environ(),
			"GIT_ASKPASS="+askpass,
			"GIT_TERMINAL_PROMPT=1",
		)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out), nil
}

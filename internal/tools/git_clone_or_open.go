package tools

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func init() { registerTool(GitCloneOrOpenMany()) }

// NamedRepo is a repo identity coming from repos_get_url: name (cluster/repo name) + URL.
type NamedRepo struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type GitCloneOrOpenManyParams struct {
	Repos       []NamedRepo `json:"repos"`                  // required
	Ref         string      `json:"ref,omitempty"`           // default "main"
	Depth       int         `json:"depth,omitempty"`         // default 1
	Pull        bool        `json:"pull,omitempty"`          // default false unless provided (set true in calls)
	Root        string      `json:"root,omitempty"`          // default "$HOME/.cache/nfreconfig-mcp-server/git-cache"
	Concurrency int         `json:"concurrency,omitempty"`   // default 4
}

type GitRepoCloneResult struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Ref     string `json:"ref"`
	Workdir string `json:"workdir,omitempty"`
	Head    string `json:"head,omitempty"`
	Updated bool   `json:"updated,omitempty"`
	Exists  bool   `json:"exists,omitempty"`
	Error   string `json:"error,omitempty"`
}

type GitCloneOrOpenManyResult struct {
	Ref      string               `json:"ref"`
	Root     string               `json:"root"`
	Results  []GitRepoCloneResult `json:"results"`
	Duration string               `json:"duration"`
}

func GitCloneOrOpenMany() MCPTool[GitCloneOrOpenManyParams, GitCloneOrOpenManyResult] {
	return MCPTool[GitCloneOrOpenManyParams, GitCloneOrOpenManyResult]{
		Name:        "git.clone_or_open_many",
		Description: "Clone/open many Git repos fast (cached workdirs). Uses readable workdir names based on repo name. Returns per-repo workdir+HEAD.",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[GitCloneOrOpenManyParams]) (*mcp.CallToolResultFor[GitCloneOrOpenManyResult], error) {
			start := time.Now()

			repos := make([]NamedRepo, 0, len(params.Arguments.Repos))
			for _, r := range params.Arguments.Repos {
				r.Name = strings.TrimSpace(r.Name)
				r.URL = strings.TrimSpace(r.URL)
				if r.Name == "" || r.URL == "" {
					continue
				}
				repos = append(repos, r)
			}
			if len(repos) == 0 {
				return toolErr[GitCloneOrOpenManyResult](fmt.Errorf("missing required field: repos (non-empty array of {name,url})"))
			}

			// Ensure git exists once
			if _, err := exec.LookPath("git"); err != nil {
				return toolErr[GitCloneOrOpenManyResult](fmt.Errorf("git binary not found in PATH: %w", err))
			}

			ref := strings.TrimSpace(params.Arguments.Ref)
			if ref == "" {
				ref = "main"
			}

			depth := params.Arguments.Depth
			if depth <= 0 {
				depth = 1
			}

			// Default root: user-writable cache dir
			root := strings.TrimSpace(params.Arguments.Root)
			if root == "" {
				home := strings.TrimSpace(os.Getenv("HOME"))
				if home != "" {
					root = filepath.Join(home, ".cache", "nfreconfig-mcp-server", "git-cache")
				} else {
					root = "/tmp/nfreconfig-mcp-server/git-cache"
				}
			}
			if err := os.MkdirAll(root, 0o755); err != nil {
				return toolErr[GitCloneOrOpenManyResult](fmt.Errorf("create root dir %q: %w", root, err))
			}

			concurrency := params.Arguments.Concurrency
			if concurrency <= 0 {
				concurrency = 4
			}
			if concurrency > len(repos) {
				concurrency = len(repos)
			}
			pull := params.Arguments.Pull

			results := make([]GitRepoCloneResult, len(repos))

			sem := make(chan struct{}, concurrency)
			var wg sync.WaitGroup

			for i := range repos {
				i := i
				sem <- struct{}{}
				wg.Add(1)
				go func() {
					defer func() {
						<-sem
						wg.Done()
					}()
					results[i] = cloneOrOpenOneNamed(ctx, root, repos[i], ref, depth, pull)
				}()
			}

			wg.Wait()

			return toolOK(GitCloneOrOpenManyResult{
				Ref:      ref,
				Root:     root,
				Results:  results,
				Duration: time.Since(start).String(),
			}), nil
		},
	}
}

// ----------------- core logic -----------------

func cloneOrOpenOneNamed(ctx context.Context, root string, repo NamedRepo, ref string, depth int, pull bool) GitRepoCloneResult {
	res := GitRepoCloneResult{
		Name: repo.Name,
		URL:  repo.URL,
		Ref:  ref,
	}

	// Readable + unique workdir: <root>/<sanitized-name>__<shortHash>
	base := sanitizeName(repo.Name)
	suffix := hashKey(repo.URL)[:8]
	workdir := filepath.Join(root, base+"__"+suffix)

	res.Workdir = workdir

	exists := dirLooksLikeGitRepo(workdir)
	res.Exists = exists

	url := repo.URL

	if !exists {
		args := []string{"clone"}
		if depth > 0 {
			args = append(args, fmt.Sprintf("--depth=%d", depth))
		}

		if !looksLikeCommitSHA(ref) {
			args = append(args, "--branch", ref, "--single-branch")
		}

		args = append(args, url, workdir)

		if err := runCmd(ctx, "", "git", args...); err != nil {
			res.Error = fmt.Sprintf("git clone failed: %v", err)
			return res
		}

		// checkout SHA if needed
		if looksLikeCommitSHA(ref) {
			if err := runCmd(ctx, workdir, "git", "checkout", ref); err != nil {
				res.Error = fmt.Sprintf("git checkout %s failed: %v", ref, err)
				return res
			}
		}

		res.Updated = true
	} else {
		// Verify origin matches requested URL (avoid wrong reuse)
		if origin, err := gitOriginURL(ctx, workdir); err == nil && origin != "" {
			if !sameRepoURL(origin, url) {
				res.Error = fmt.Sprintf("origin mismatch (have=%q want=%q) workdir=%s", origin, url, workdir)
				return res
			}
		}

		if pull {
			if err := runCmd(ctx, workdir, "git", "fetch", "--all", "--prune"); err != nil {
				res.Error = fmt.Sprintf("git fetch failed: %v", err)
				return res
			}
			res.Updated = true
		}

		if looksLikeCommitSHA(ref) {
			if err := runCmd(ctx, workdir, "git", "checkout", ref); err != nil {
				res.Error = fmt.Sprintf("git checkout %s failed: %v", ref, err)
				return res
			}
		} else {
			// checkout branch
			if err := runCmd(ctx, workdir, "git", "checkout", ref); err != nil {
				// try create local branch from origin/<ref>
				_ = runCmd(ctx, workdir, "git", "checkout", "-B", ref, "origin/"+ref)
			}

			// keep local exactly at remote if pull enabled
			if pull {
				_ = runCmd(ctx, workdir, "git", "reset", "--hard", "origin/"+ref)
			}
		}
	}

	head, err := gitHeadSHA(ctx, workdir)
	if err != nil {
		res.Error = fmt.Sprintf("read HEAD failed: %v", err)
		return res
	}
	res.Head = head
	return res
}

// ----------------- helpers -----------------

func hashKey(s string) string {
	h := sha1.Sum([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(h[:])
}

func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	// replace non safe filename chars with '-'
	re := regexp.MustCompile(`[^a-z0-9._-]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "repo"
	}
	return s
}

func dirLooksLikeGitRepo(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && st != nil
}

func runCmd(ctx context.Context, workdir string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if workdir != "" {
		cmd.Dir = workdir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, string(out))
	}
	return nil
}

func gitHeadSHA(ctx context.Context, workdir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w\n%s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func gitOriginURL(ctx context.Context, workdir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git remote get-url origin: %w\n%s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func sameRepoURL(a, b string) bool {
	normalize := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.TrimSuffix(s, "/")
		s = strings.TrimSuffix(s, ".git")
		return s
	}
	return normalize(a) == normalize(b)
}

var shaRe = regexp.MustCompile(`^[a-fA-F0-9]{7,40}$`)

func looksLikeCommitSHA(ref string) bool {
	ref = strings.TrimSpace(ref)
	return shaRe.MatchString(ref)
}

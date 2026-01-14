package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func init() { registerTool(RepoScanManifestsMany()) }

type RepoWorkdir struct {
	Name    string `json:"name"`
	Workdir string `json:"workdir"`
}

type RepoScanManifestsManyParams struct {
	Repos    []RepoWorkdir `json:"repos"`              // required
	Kinds    []string      `json:"kinds,omitempty"`    // default ["NFDeployment","NetworkAttachmentDefinition","NFConfig","Config"]
	MaxFiles int           `json:"maxFiles,omitempty"` // default 5000
}

type FoundObject struct {
	Repo       string `json:"repo"`
	File       string `json:"file"` // repo-relative path
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion,omitempty"`
	Name       string `json:"name,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
}

type RepoScanResult struct {
	Repo    string        `json:"repo"`
	Workdir string        `json:"workdir"`
	Found   []FoundObject `json:"found"`
	Errors  []string      `json:"errors,omitempty"`
}

type RepoScanManifestsManyResult struct {
	Results []RepoScanResult `json:"results"`
}

func RepoScanManifestsMany() MCPTool[RepoScanManifestsManyParams, RepoScanManifestsManyResult] {
	return MCPTool[RepoScanManifestsManyParams, RepoScanManifestsManyResult]{
		Name:        "repo.scan_manifests_many",
		Description: "Scan cloned repos for YAML manifests and return objects matching kinds (NFDeployment, NetworkAttachmentDefinition, NFConfig, Config).",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[RepoScanManifestsManyParams]) (*mcp.CallToolResultFor[RepoScanManifestsManyResult], error) {
			repos := make([]RepoWorkdir, 0, len(params.Arguments.Repos))
			for _, r := range params.Arguments.Repos {
				r.Name = strings.TrimSpace(r.Name)
				r.Workdir = strings.TrimSpace(r.Workdir)
				// Harden against accidental embedded quotes in paths
				r.Workdir = strings.Trim(r.Workdir, "\"'")
				if r.Name == "" || r.Workdir == "" {
					continue
				}
				repos = append(repos, r)
			}
			if len(repos) == 0 {
				return toolErr[RepoScanManifestsManyResult](fmt.Errorf("missing required field: repos (non-empty array of {name,workdir})"))
			}

			wantKinds := toSet(params.Arguments.Kinds)
			if len(wantKinds) == 0 {
				wantKinds = toSet([]string{"NFDeployment", "NetworkAttachmentDefinition", "NFConfig", "Config"})
			}

			maxFiles := params.Arguments.MaxFiles
			if maxFiles <= 0 {
				maxFiles = 5000
			}

			out := RepoScanManifestsManyResult{
				Results: make([]RepoScanResult, 0, len(repos)),
			}

			for _, r := range repos {
				res := RepoScanResult{
					Repo:    r.Name,
					Workdir: r.Workdir,
					Found:   []FoundObject{},
					Errors:  []string{},
				}

				count := 0
				walkErr := filepath.WalkDir(r.Workdir, func(path string, d fs.DirEntry, err error) error {
					if err != nil {
						res.Errors = append(res.Errors, fmt.Sprintf("walk error: %s: %v", path, err))
						return nil
					}
					if d.IsDir() {
						if d.Name() == ".git" {
							return fs.SkipDir
						}
						return nil
					}

					ext := strings.ToLower(filepath.Ext(d.Name()))
					if ext != ".yaml" && ext != ".yml" {
						return nil
					}

					count++
					if count > maxFiles {
						return fs.SkipAll
					}

					rel, _ := filepath.Rel(r.Workdir, path)

					b, readErr := os.ReadFile(path)
					if readErr != nil {
						res.Errors = append(res.Errors, fmt.Sprintf("read error: %s: %v", filepath.ToSlash(rel), readErr))
						return nil
					}

					docs := splitYAMLDocuments(string(b))
					for _, doc := range docs {
						doc = strings.TrimSpace(doc)
						if doc == "" {
							continue
						}

						obj, parseErr := parseYAMLToUnstructured([]byte(doc))
						if parseErr != nil || obj == nil {
							// ignore non-k8s or invalid docs
							continue
						}

						kind := strings.TrimSpace(obj.GetKind())
						if kind == "" {
							continue
						}
						if _, ok := wantKinds[kind]; !ok {
							continue
						}

						res.Found = append(res.Found, FoundObject{
							Repo:       r.Name,
							File:       filepath.ToSlash(rel),
							Kind:       kind,
							APIVersion: obj.GetAPIVersion(),
							Name:       obj.GetName(),
							Namespace:  obj.GetNamespace(),
						})
					}

					return nil
				})

				if walkErr != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("walk failed: %v", walkErr))
				}

				out.Results = append(out.Results, res)
			}

			return toolOK(out), nil
		},
	}
}

// ---- helpers ----

func toSet(xs []string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x == "" {
			continue
		}
		m[x] = struct{}{}
	}
	return m
}

func splitYAMLDocuments(s string) []string {
	parts := []string{}
	cur := []string{}
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "---" {
			parts = append(parts, strings.Join(cur, "\n"))
			cur = []string{}
			continue
		}
		cur = append(cur, line)
	}
	parts = append(parts, strings.Join(cur, "\n"))
	return parts
}

func parseYAMLToUnstructured(b []byte) (*unstructured.Unstructured, error) {
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("empty")
	}
	u := &unstructured.Unstructured{Object: m}
	if u.GetKind() == "" || u.GetAPIVersion() == "" {
		return nil, fmt.Errorf("not a k8s object")
	}
	return u, nil
}

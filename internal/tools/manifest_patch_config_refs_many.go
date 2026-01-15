package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func init() { registerTool(ManifestPatchConfigRefsMany()) }

type ManifestPatchConfigRefsManyParams struct {
	Targets    []PatchTarget     `json:"targets"`    // DU/CUUP Config YAMLs (kind=Config)
	OldNeedles []string          `json:"oldNeedles"` // strings to replace (e.g., old CUCP IPs/CIDRs you extracted)
	NewRepl    map[string]string `json:"newRepl"`    // map old->new (explicit replacements)
	DryRun     bool              `json:"dryRun,omitempty"`
}

type ManifestPatchConfigRefsManyResult struct {
	Results []PatchResult `json:"results"`
}

func ManifestPatchConfigRefsMany() MCPTool[ManifestPatchConfigRefsManyParams, ManifestPatchConfigRefsManyResult] {
	return MCPTool[ManifestPatchConfigRefsManyParams, ManifestPatchConfigRefsManyResult]{
		Name:        "manifest_patch_config_refs",
		Description: "Update DU/CUUP Config manifests that reference old CUCP IPs. Use in Phase 4 to propagate CUCP changes to dependent DU/CUUP. Performs string replacement across all YAML fields. Example: {\"targets\":[{\"repo\":\"du\",\"workdir\":\"/work/du\",\"file\":\"config.yaml\"}], \"newRepl\":{\"10.10.1.5\":\"10.10.1.10\",\"192.168.10.0/24\":\"192.168.20.0/24\"}}.",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[ManifestPatchConfigRefsManyParams]) (*mcp.CallToolResultFor[ManifestPatchConfigRefsManyResult], error) {
			if len(params.Arguments.Targets) == 0 {
				return toolErr[ManifestPatchConfigRefsManyResult](fmt.Errorf("missing required field: targets"))
			}
			if len(params.Arguments.NewRepl) == 0 && len(params.Arguments.OldNeedles) == 0 {
				return toolErr[ManifestPatchConfigRefsManyResult](fmt.Errorf("need at least one of: newRepl or oldNeedles"))
			}

			out := ManifestPatchConfigRefsManyResult{Results: make([]PatchResult, 0, len(params.Arguments.Targets))}
			repl := params.Arguments.NewRepl

			for _, t := range params.Arguments.Targets {
				repo := strings.TrimSpace(t.Repo)
				workdir := cleanPath(t.Workdir)
				file := filepath.ToSlash(strings.TrimSpace(t.File))
				abs := absJoin(workdir, file)

				r := PatchResult{Repo: repo, File: file}

				u, _, err := readYAMLFile(abs)
				if err != nil {
					r.Error = fmt.Sprintf("read yaml: %v", err)
					out.Results = append(out.Results, r)
					continue
				}

				obj := u.Object
				changed := false

				// Replace across all string fields
				walkAny(obj, func(_ []string, key string, parent map[string]any, val any) {
					s, ok := val.(string)
					if !ok || s == "" {
						return
					}
					orig := s

					// explicit old->new replacements
					for old, nw := range repl {
						if old != "" && strings.Contains(s, old) {
							s = strings.ReplaceAll(s, old, nw)
						}
					}
					// optional needle-only mode (replace with nothing? we don't do that)
					if s != orig {
						parent[key] = s
						changed = true
					}
				})

				if changed && !params.Arguments.DryRun {
					if err := writeYAMLFile(abs, obj); err != nil {
						r.Error = fmt.Sprintf("write yaml: %v", err)
						out.Results = append(out.Results, r)
						continue
					}
				}
				r.Changed = changed
				out.Results = append(out.Results, r)
			}

			return toolOK(out), nil
		},
	}
}

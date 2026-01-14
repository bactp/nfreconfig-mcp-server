package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func init() { registerTool(RepoExtractTopologyMany()) }

type RepoTopologyQuery struct {
	Repo    string `json:"repo"`
	Workdir string `json:"workdir"`
	File    string `json:"file"`
}

type RepoExtractTopologyManyParams struct {
	Queries []RepoTopologyQuery `json:"queries"` // required
}

type ExtractedTopology struct {
	Repo       string   `json:"repo"`
	File       string   `json:"file"`
	Kind       string   `json:"kind,omitempty"`
	APIVersion string   `json:"apiVersion,omitempty"`
	Name       string   `json:"name,omitempty"`
	Namespace  string   `json:"namespace,omitempty"`

	// best-effort extracted values
	CIDRs []string `json:"cidrs,omitempty"`
	IPs   []string `json:"ips,omitempty"`

	// For NAD: nadConfigIPs/cidrs extracted from spec.config JSON string too
	NADConfigCIDRs []string `json:"nadConfigCidrs,omitempty"`
	NADConfigIPs   []string `json:"nadConfigIps,omitempty"`

	Error string `json:"error,omitempty"`
}

type RepoExtractTopologyManyResult struct {
	Results []ExtractedTopology `json:"results"`
}

func RepoExtractTopologyMany() MCPTool[RepoExtractTopologyManyParams, RepoExtractTopologyManyResult] {
	return MCPTool[RepoExtractTopologyManyParams, RepoExtractTopologyManyResult]{
		Name:        "repo.extract_topology_many",
		Description: "Read YAML files and extract best-effort current IP/CIDR topology (from strings anywhere + NAD spec.config JSON). Use after repo.scan_manifests_many.",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[RepoExtractTopologyManyParams]) (*mcp.CallToolResultFor[RepoExtractTopologyManyResult], error) {
			if len(params.Arguments.Queries) == 0 {
				return toolErr[RepoExtractTopologyManyResult](fmt.Errorf("missing required field: queries"))
			}

			out := RepoExtractTopologyManyResult{Results: make([]ExtractedTopology, 0, len(params.Arguments.Queries))}

			for _, q := range params.Arguments.Queries {
				repo := strings.TrimSpace(q.Repo)
				workdir := cleanPath(q.Workdir)
				file := filepath.ToSlash(strings.TrimSpace(q.File))
				r := ExtractedTopology{Repo: repo, File: file}

				if repo == "" || workdir == "" || file == "" {
					r.Error = "repo/workdir/file must be non-empty"
					out.Results = append(out.Results, r)
					continue
				}

				abs := absJoin(workdir, file)
				u, _, err := readYAMLFile(abs)
				if err != nil {
					r.Error = fmt.Sprintf("read yaml: %v", err)
					out.Results = append(out.Results, r)
					continue
				}

				if u != nil {
					r.Kind = u.GetKind()
					r.APIVersion = u.GetAPIVersion()
					r.Name = u.GetName()
					r.Namespace = u.GetNamespace()
				}

				obj := map[string]any(nil)
				if u != nil {
					obj = u.Object
				}
				if obj == nil {
					r.Error = "empty object"
					out.Results = append(out.Results, r)
					continue
				}

				cidrs, ips := extractAllCIDRsAndIPv4Strings(obj)
				sort.Strings(cidrs)
				sort.Strings(ips)
				r.CIDRs = cidrs
				r.IPs = ips

				// NAD spec.config JSON string
				if r.Kind == "NetworkAttachmentDefinition" {
					spec, _, _ := unstructured.NestedMap(obj, "spec")
					if cfg, ok := spec["config"].(string); ok && strings.TrimSpace(cfg) != "" {
						if jm, ok := tryParseJSONConfigString(cfg); ok {
							c2, i2 := extractAllCIDRsAndIPv4Strings(jm)
							sort.Strings(c2)
							sort.Strings(i2)
							r.NADConfigCIDRs = c2
							r.NADConfigIPs = i2
						}
					}
				}

				out.Results = append(out.Results, r)
			}

			return toolOK(out), nil
		},
	}
}

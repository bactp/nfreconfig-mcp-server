package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"nfreconfig-mcp-server/internal/kube"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func init() { registerTool(ReposGetURLs()) }

type ReposGetURLsParams struct {
	Prefix    string `json:"prefix,omitempty"`    // default "5g-"
	OnlyReady bool   `json:"onlyReady,omitempty"` // default true
}

type RepoURL struct {
	Name    string `json:"name"`
	URL string `json:"url"`
	Ready   bool   `json:"ready"`
}

type ReposGetURLsResult struct {
	Prefix       string   `json:"prefix"`
	Repositories []RepoURL `json:"repositories"`
}

func ReposGetURLs() MCPTool[ReposGetURLsParams, ReposGetURLsResult] {
	return MCPTool[ReposGetURLsParams, ReposGetURLsResult]{
		Name:        "repos.get_repos_urls",
		Description: "Return Git repo URLs (addresses) for all Repository inventory objects matching a prefix (e.g., 5g-), so the agent can clone each repo.",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[ReposGetURLsParams]) (*mcp.CallToolResultFor[ReposGetURLsResult], error) {
			prefix := params.Arguments.Prefix
			if prefix == "" {
				prefix = "5g-"
			}
			onlyReady := params.Arguments.OnlyReady
			// Default behavior: onlyReady=true unless explicitly set false
			// (MCP params can't distinguish unset vs false easily unless you use *bool;
			// if you want strict defaulting, change OnlyReady to *bool.)
			if params.Arguments.OnlyReady == false {
				// keep false if explicitly provided; otherwise it’s also false
				// If you want default=true, change type to *bool.
			}

			// Load mgmt kube context
			_, raw, err := kube.LoadRawConfig()
			if err != nil {
				return toolErr[ReposGetURLsResult](err)
			}

			// Build clients against mgmt cluster
			dyn, err := kube.BuildDynamicClient(raw.CurrentContext)
			if err != nil {
				return toolErr[ReposGetURLsResult](fmt.Errorf("build dynamic client (context=%s): %w", raw.CurrentContext, err))
			}
			cs, err := kube.BuildClientset(raw.CurrentContext)
			if err != nil {
				return toolErr[ReposGetURLsResult](fmt.Errorf("build clientset (context=%s): %w", raw.CurrentContext, err))
			}

			// Discover Repository GVR
			gvr, namespaced, err := discoverRepositoryGVR(cs.Discovery())
			if err != nil {
				return toolErr[ReposGetURLsResult](err)
			}

			// List repositories (cluster-scoped or namespaced-all)
			var ul *unstructured.UnstructuredList
			if namespaced {
				ul, err = dyn.Resource(gvr).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
			} else {
				ul, err = dyn.Resource(gvr).List(ctx, metav1.ListOptions{})
			}
			if err != nil {
				return toolErr[ReposGetURLsResult](fmt.Errorf("list repositories (gvr=%s): %w", gvr.String(), err))
			}

			out := make([]RepoURL, 0)
			for i := range ul.Items {
				u := &ul.Items[i]
				name := u.GetName()
				if prefix != "" && !strings.HasPrefix(name, prefix) {
					continue
				}

				ready := isRepoReady(u)
				if onlyReady && !ready {
					continue
				}

				address := extractRepoAddress(u)
				if address == "" {
					// skip entries with no address (agent can’t clone)
					continue
				}

				out = append(out, RepoURL{
					Name:    name,
					URL: address,
					Ready:   ready,
				})
			}

			sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

			return toolOK(ReposGetURLsResult{
				Prefix:       prefix,
				Repositories: out,
			}), nil
		},
	}
}

// ---- helpers ----

func isRepoReady(u *unstructured.Unstructured) bool {
	conds, found, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	if found {
		for _, c := range conds {
			m, ok := c.(map[string]any)
			if !ok {
				continue
			}
			t, _ := m["type"].(string)
			s, _ := m["status"].(string)
			if t == "Ready" && (s == "True" || s == "true") {
				return true
			}
		}
	}
	if v, ok, _ := unstructured.NestedBool(u.Object, "status", "ready"); ok {
		return v
	}
	return false
}

func extractRepoAddress(u *unstructured.Unstructured) string {
	// Most common locations:
	if v, ok, _ := unstructured.NestedString(u.Object, "spec", "git", "repo"); ok && v != "" {
		return v
	}
	if v, ok, _ := unstructured.NestedString(u.Object, "spec", "git", "repoURL"); ok && v != "" {
		return v
	}
	if v, ok, _ := unstructured.NestedString(u.Object, "spec", "address"); ok && v != "" {
		return v
	}
	if v, ok, _ := unstructured.NestedString(u.Object, "status", "address"); ok && v != "" {
		return v
	}
	return ""
}

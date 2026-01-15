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
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() { registerTool(ReposGetReposURLs()) }

// MCP tool name required by you: "[repos]@get_repos_urls"
type ReposGetReposURLsParams struct {
	Prefix    string `json:"prefix,omitempty"`    // default "5g-"
	OnlyReady *bool  `json:"onlyReady,omitempty"` // default true (nil => true)
}

type RepoURL struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Ready bool   `json:"ready"`
}

type ReposGetReposURLsResult struct {
	Prefix       string    `json:"prefix"`
	OnlyReady    bool      `json:"onlyReady"`
	Repositories []RepoURL `json:"repositories"`
}

func ReposGetReposURLs() MCPTool[ReposGetReposURLsParams, ReposGetReposURLsResult] {
	return MCPTool[ReposGetReposURLsParams, ReposGetReposURLsResult]{
		Name:        "[repos]@get_repos_urls",
		Description: "Get Git clone URLs for all repositories matching a prefix. Use to discover all 5G repos in Porch/Nephio inventory. Returns repo name, URL, and ready status. Example: {\"prefix\":\"5g-\", \"onlyReady\":true} returns all ready repos starting with '5g-'.",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[ReposGetReposURLsParams]) (*mcp.CallToolResultFor[ReposGetReposURLsResult], error) {
			prefix := strings.TrimSpace(params.Arguments.Prefix)
			if prefix == "" {
				prefix = "5g-"
			}

			onlyReady := true
			if params.Arguments.OnlyReady != nil {
				onlyReady = *params.Arguments.OnlyReady
			}

			// mgmt kube context
			_, raw, err := kube.LoadRawConfig()
			if err != nil {
				return toolErr[ReposGetReposURLsResult](err)
			}

			// clients against mgmt cluster
			dyn, err := kube.BuildDynamicClient(raw.CurrentContext)
			if err != nil {
				return toolErr[ReposGetReposURLsResult](fmt.Errorf("build dynamic client (context=%s): %w", raw.CurrentContext, err))
			}
			cs, err := kube.BuildClientset(raw.CurrentContext)
			if err != nil {
				return toolErr[ReposGetReposURLsResult](fmt.Errorf("build clientset (context=%s): %w", raw.CurrentContext, err))
			}

			// discover Repository GVR
			gvr, namespaced, err := discoverRepositoryGVR(cs.Discovery())
			if err != nil {
				return toolErr[ReposGetReposURLsResult](err)
			}

			// list repositories
			var ul *unstructured.UnstructuredList
			if namespaced {
				ul, err = dyn.Resource(gvr).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
			} else {
				ul, err = dyn.Resource(gvr).List(ctx, metav1.ListOptions{})
			}
			if err != nil {
				return toolErr[ReposGetReposURLsResult](fmt.Errorf("list repositories (gvr=%s, namespaced=%v): %w", gvr.String(), namespaced, err))
			}

			out := make([]RepoURL, 0, len(ul.Items))
			for i := range ul.Items {
				u := &ul.Items[i]
				name := u.GetName()

				if prefix != "" && !strings.HasPrefix(name, prefix) {
					continue
				}

				ready := isReadyConditionTrue(u)
				if onlyReady && !ready {
					continue
				}

				url := extractRepoAddress(u)
				if url == "" {
					// no clone target -> skip
					continue
				}

				out = append(out, RepoURL{
					Name:  name,
					URL:   url,
					Ready: ready,
				})
			}

			sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

			return toolOK(ReposGetReposURLsResult{
				Prefix:       prefix,
				OnlyReady:    onlyReady,
				Repositories: out,
			}), nil
		},
	}
}

// -------------------------
// Discovery + extraction helpers (single source of truth)
// -------------------------

func discoverRepositoryGVR(discovery interface {
	ServerPreferredResources() ([]*metav1.APIResourceList, error)
}) (gvr schema.GroupVersionResource, namespaced bool, err error) {
	lists, err := discovery.ServerPreferredResources()
	if err != nil {
		// Some clusters return partial results with an error; still try to use what we got.
		if lists == nil {
			return schema.GroupVersionResource{}, false, fmt.Errorf("discover api resources: %w", err)
		}
	}

	for _, l := range lists {
		if l == nil || l.GroupVersion == "" {
			continue
		}
		gv, gvErr := schema.ParseGroupVersion(l.GroupVersion)
		if gvErr != nil {
			continue
		}

		for _, r := range l.APIResources {
			if r.Kind != "Repository" {
				continue
			}
			if r.Name == "" {
				continue
			}
			return schema.GroupVersionResource{
				Group:    gv.Group,
				Version:  gv.Version,
				Resource: r.Name,
			}, r.Namespaced, nil
		}
	}
	return schema.GroupVersionResource{}, false, fmt.Errorf("could not discover a Kubernetes API resource with Kind=Repository")
}

func isReadyConditionTrue(u *unstructured.Unstructured) bool {
	if u == nil {
		return false
	}
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
	// Most common Porch/Nephio locations:
	if v, ok, _ := unstructured.NestedString(u.Object, "spec", "git", "repo"); ok && v != "" {
		return v
	}
	if v, ok, _ := unstructured.NestedString(u.Object, "spec", "git", "repoURL"); ok && v != "" {
		return v
	}
	if v, ok, _ := unstructured.NestedString(u.Object, "spec", "address"); ok && v != "" {
		return v
	}
	// fallback: sometimes computed/stored in status
	if v, ok, _ := unstructured.NestedString(u.Object, "status", "address"); ok && v != "" {
		return v
	}
	return ""
}

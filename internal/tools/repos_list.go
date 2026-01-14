////DEPRECATED FILE - use repos_list.go instead////

package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"nfreconfig-mcp-server/internal/kube"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func init() { registerTool(ReposList()) }

type ReposListParams struct {
	NamePrefix     string `json:"namePrefix,omitempty"`
	OnlyReady      bool   `json:"onlyReady,omitempty"`
	OnlyDeployment *bool  `json:"onlyDeployment,omitempty"` // nil = no filter
	Type           string `json:"type,omitempty"`           // e.g., "git"
}

type RepoRef struct {
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"`
	Content    string `json:"content,omitempty"`
	Deployment *bool  `json:"deployment,omitempty"`
	Ready      bool   `json:"ready"`
	Address    string `json:"address,omitempty"`
}


type RepoClusterURL struct {
	Cluster string `json:"cluster"`
	URL     string `json:"url"`
	Ready   bool   `json:"ready"`
}

type ReposListResult struct {
	Repositories []RepoClusterURL `json:"repositories"`
}


func ReposList() MCPTool[ReposListParams, ReposListResult] {
	return MCPTool[ReposListParams, ReposListResult]{
		Name:        "repos.list",
		Description: "List Nephio/Porch Repository inventory from the management cluster (source-of-truth Git repos for workload configs).",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[ReposListParams]) (*mcp.CallToolResultFor[ReposListResult], error) {
			// Load mgmt kube context (same as clusters.list)
			_, raw, err := kube.LoadRawConfig()
			if err != nil {
				return toolErr[ReposListResult](err)
			}

			// Build clients against mgmt cluster
			dyn, err := kube.BuildDynamicClient(raw.CurrentContext)
			if err != nil {
				return toolErr[ReposListResult](fmt.Errorf("build dynamic client (context=%s): %w", raw.CurrentContext, err))
			}
			cs, err := kube.BuildClientset(raw.CurrentContext)
			if err != nil {
				return toolErr[ReposListResult](fmt.Errorf("build clientset (context=%s): %w", raw.CurrentContext, err))
			}

			// Discover the Repository GVR (no hardcoding)
			gvr, namespaced, err := discoverRepositoryGVR(cs.Discovery())
			if err != nil {
				return toolErr[ReposListResult](err)
			}

			// List repositories (cluster-scoped OR all namespaces depending on discovery)
			var ul *unstructured.UnstructuredList
			if namespaced {
				ul, err = dyn.Resource(gvr).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
			} else {
				ul, err = dyn.Resource(gvr).List(ctx, metav1.ListOptions{})
			}
			if err != nil {
				return toolErr[ReposListResult](fmt.Errorf("list repositories (gvr=%s, namespaced=%v): %w", gvr.String(), namespaced, err))
			}

			// Extract + filter
			out := make([]RepoRef, 0, len(ul.Items))
			for i := range ul.Items {
				rr := extractRepoRef(&ul.Items[i])

				// Filters
				if params.Arguments.NamePrefix != "" && !strings.HasPrefix(rr.Name, params.Arguments.NamePrefix) {
					continue
				}
				if params.Arguments.OnlyReady && !rr.Ready {
					continue
				}
				if params.Arguments.Type != "" && rr.Type != params.Arguments.Type {
					continue
				}
				if params.Arguments.OnlyDeployment != nil {
					if rr.Deployment == nil || *rr.Deployment != *params.Arguments.OnlyDeployment {
						continue
					}
				}

				
				out = append(out, rr)
			}

			// Stable output
			sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

			// ...existing code...
		outClusterURLs := make([]RepoClusterURL, len(out))
		for i, rr := range out {
			outClusterURLs[i] = RepoClusterURL{
				Cluster: "", // Set the appropriate cluster value if needed
				URL:     rr.Content,
				Ready:   rr.Ready,
			}
		}

		return toolOK(ReposListResult{Repositories: outClusterURLs}), nil
		// ...existing code...
		},
	}
}

// -------------------------
// Discovery + extraction
// -------------------------

func discoverRepositoryGVR(discovery interface {
	ServerPreferredResources() ([]*metav1.APIResourceList, error)
}) (gvr schema.GroupVersionResource, namespaced bool, err error) {
	lists, err := discovery.ServerPreferredResources()
	if err != nil {
		// Some clusters return partial results with an error; still try to use what we got.
		// If lists is nil, we must fail.
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
			// We want Kind=Repository (matches your `kubectl get repository`)
			if r.Kind != "Repository" {
				continue
			}

			// Use the discovered plural name (usually "repositories" or something similar)
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

func extractRepoRef(u *unstructured.Unstructured) RepoRef {
	rr := RepoRef{
		Name:  u.GetName(),
		Ready: isReadyConditionTrue(u),
	}

	// spec.type, spec.content, spec.deployment
	if v, ok, _ := unstructured.NestedString(u.Object, "spec", "type"); ok {
		rr.Type = v
	}
	if v, ok, _ := unstructured.NestedString(u.Object, "spec", "content"); ok {
		rr.Content = v
	}
	if v, ok, _ := unstructured.NestedBool(u.Object, "spec", "deployment"); ok {
		rr.Deployment = &v
	} else if v, ok, _ := unstructured.NestedBool(u.Object, "spec", "deployment", "enabled"); ok {
		rr.Deployment = &v
	}

	// address (try common locations)
	// porch commonly uses spec.git.repo or spec.address depending on version.
	if v, ok, _ := unstructured.NestedString(u.Object, "spec", "git", "repo"); ok && v != "" {
		rr.Address = v
	}
	if rr.Address == "" {
		if v, ok, _ := unstructured.NestedString(u.Object, "spec", "git", "repoURL"); ok && v != "" {
			rr.Address = v
		}
	}
	if rr.Address == "" {
		if v, ok, _ := unstructured.NestedString(u.Object, "spec", "address"); ok && v != "" {
			rr.Address = v
		}
	}

	// As a fallback, sometimes address appears in status.
	if rr.Address == "" {
		if v, ok, _ := unstructured.NestedString(u.Object, "status", "address"); ok && v != "" {
			rr.Address = v
		}
	}

	return rr
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

	// Fallback if some implementation uses status.ready
	if v, ok, _ := unstructured.NestedBool(u.Object, "status", "ready"); ok {
		return v
	}
	return false
}

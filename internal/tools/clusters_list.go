package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"nfreconfig-mcp-server/internal/kube"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func init() { registerTool(ClustersList()) }

type ClustersListParams struct{}

// ClusterRef now supports both kubeconfig contexts and inventory clusters.
type ClusterRef struct {
	Name             string `json:"name"`
	Kind             string `json:"kind,omitempty"`             // "KubeContext" | "CAPICluster"
	Namespace        string `json:"namespace,omitempty"`        // for CAPICluster
	Ready            bool   `json:"ready,omitempty"`            // for CAPICluster
	APIServer        string `json:"apiServer,omitempty"`        // best-effort
	KubeconfigSecret string `json:"kubeconfigSecret,omitempty"` // "ns/name"
}

type ClustersListResult struct {
	CurrentContext string       `json:"currentContext"`
	Clusters       []ClusterRef `json:"clusters"`
}

func ClustersList() MCPTool[ClustersListParams, ClustersListResult] {
	return MCPTool[ClustersListParams, ClustersListResult]{
		Name:        "clusters.list",
		Description: "List clusters from kubeconfig contexts AND Cluster API inventory (Cluster objects) with kubeconfig secret references.",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[ClustersListParams]) (*mcp.CallToolResultFor[ClustersListResult], error) {
			// 1) Kubeconfig contexts (your original behavior)
			_, raw, err := kube.LoadRawConfig()
			if err != nil {
				return toolErr[ClustersListResult](err)
			}

			out := ClustersListResult{
				CurrentContext: raw.CurrentContext,
				Clusters:       make([]ClusterRef, 0),
			}

			// Add kubeconfig contexts first
			ctxNames := make([]string, 0, len(raw.Contexts))
			for name := range raw.Contexts {
				ctxNames = append(ctxNames, name)
			}
			sort.Strings(ctxNames)
			for _, name := range ctxNames {
				out.Clusters = append(out.Clusters, ClusterRef{
					Name: name,
					Kind: "KubeContext",
				})
			}

			// 2) Cluster API inventory (CAPI Cluster objects) from the mgmt cluster context
			dyn, err := kube.BuildDynamicClient(raw.CurrentContext)
			if err != nil {
				return toolErr[ClustersListResult](fmt.Errorf("build dynamic client (context=%s): %w", raw.CurrentContext, err))
			}
			cs, err := kube.BuildClientset(raw.CurrentContext)
			if err != nil {
				return toolErr[ClustersListResult](fmt.Errorf("build clientset (context=%s): %w", raw.CurrentContext, err))
			}

			capiGVR := schema.GroupVersionResource{
				Group:    "cluster.x-k8s.io",
				Version:  "v1beta1",
				Resource: "clusters",
			}

			ul, err := dyn.Resource(capiGVR).Namespace("").List(ctx, metav1.ListOptions{})
			if err != nil {
				// If Cluster API isn’t installed, still return kube contexts
				// (don’t fail the whole tool)
				return toolOK(out), nil
			}

			// Collect CAPI clusters
			capiRefs := make([]ClusterRef, 0, len(ul.Items))
			for _, it := range ul.Items {
				name := it.GetName()
				ns := it.GetNamespace()

				ready := isCAPIClusterReady(&it)

				secretName := name + "-kubeconfig"
				secretRef := ns + "/" + secretName

				ref := ClusterRef{
					Name:             name,
					Kind:             "CAPICluster",
					Namespace:        ns,
					Ready:            ready,
					KubeconfigSecret: secretRef,
				}

				// Best-effort: read the kubeconfig secret and extract apiServer
				sec, secErr := cs.CoreV1().Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
				if secErr == nil {
					// Common keys: "value" (most CAPI), sometimes "kubeconfig"
					var kubeBytes []byte
					if b, ok := sec.Data["value"]; ok && len(b) > 0 {
						kubeBytes = b
					} else if b, ok := sec.Data["kubeconfig"]; ok && len(b) > 0 {
						kubeBytes = b
					} else if s, ok := sec.StringData["value"]; ok && s != "" {
						kubeBytes = []byte(s)
					} else if s, ok := sec.StringData["kubeconfig"]; ok && s != "" {
						kubeBytes = []byte(s)
					}

					// Some secrets might store base64 string in Data (rare); handle just in case:
					if len(kubeBytes) > 0 && looksBase64(kubeBytes) {
						if dec, e := base64.StdEncoding.DecodeString(strings.TrimSpace(string(kubeBytes))); e == nil {
							kubeBytes = dec
						}
					}

					if len(kubeBytes) > 0 {
						if apiServer := extractAPIServerFromKubeconfig(kubeBytes); apiServer != "" {
							ref.APIServer = apiServer
						}
					}
				}

				capiRefs = append(capiRefs, ref)
			}

			// Sort CAPI clusters by name for stable output
			sort.Slice(capiRefs, func(i, j int) bool { return capiRefs[i].Name < capiRefs[j].Name })

			// Append inventory clusters after kubeconfig contexts
			out.Clusters = append(out.Clusters, capiRefs...)

			return toolOK(out), nil
		},
	}
}




// helper wrapper for unstructured

func isCAPIClusterReady(u *unstructured.Unstructured) bool {
	if u == nil {
		return false
	}
	conds, found, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	if !found {
		return false
	}
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		t, _ := m["type"].(string)
		s, _ := m["status"].(string)
		// CAPI Cluster typically uses type="Ready"
		if t == "Ready" && (s == "True" || s == "true") {
			return true
		}
	}
	return false
}



func extractAPIServerFromKubeconfig(kubeconfig []byte) string {
	cfg, err := clientcmd.Load(kubeconfig)
	if err != nil || cfg == nil || len(cfg.Clusters) == 0 {
		return ""
	}

	// Prefer server pointed by current-context if possible
	if cfg.CurrentContext != "" && cfg.Contexts != nil {
		if ctx, ok := cfg.Contexts[cfg.CurrentContext]; ok && ctx != nil {
			if cl, ok := cfg.Clusters[ctx.Cluster]; ok && cl != nil {
				return cl.Server
			}
		}
	}

	// Otherwise return the first cluster server
	for _, cl := range cfg.Clusters {
		if cl != nil && cl.Server != "" {
			return cl.Server
		}
	}
	return ""
}

func looksBase64(b []byte) bool {
	s := strings.TrimSpace(string(b))
	if len(s) < 16 {
		return false
	}
	// quick heuristic
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '/' || r == '=' || r == '\n' {
			continue
		}
		return false
	}
	return true
}

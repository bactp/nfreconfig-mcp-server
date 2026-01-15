package tools

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/clientcmd"
)

// isCAPIClusterReady checks if a CAPI Cluster has Ready=True condition
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

// extractAPIServerFromKubeconfig parses kubeconfig bytes and returns the API server URL
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

// looksBase64 is a heuristic check if bytes look like base64 encoded data
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

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nfreconfig-mcp-server/internal/kube"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() { registerTool(ArgoCDSyncApp()) }

type ArgoCDSyncAppParams struct {
	Context   string `json:"context,omitempty"`   // mgmt kube context; default current
	Cluster   string `json:"cluster"`             // workload cluster name (CAPI cluster)
	Namespace string `json:"namespace,omitempty"` // default "argocd"
	AppName   string `json:"appName"`             // application name
	Prune     bool   `json:"prune,omitempty"`     // default true
}

type ArgoCDSyncAppResult struct {
	Patched bool   `json:"patched"`
	Error   string `json:"error,omitempty"`
}

func ArgoCDSyncApp() MCPTool[ArgoCDSyncAppParams, ArgoCDSyncAppResult] {
	return MCPTool[ArgoCDSyncAppParams, ArgoCDSyncAppResult]{
		Name:        "[argocd]@sync_app",
		Description: "Trigger ArgoCD Application sync by patching Application.operation.sync (works without argocd CLI).",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[ArgoCDSyncAppParams]) (*mcp.CallToolResultFor[ArgoCDSyncAppResult], error) {
			ns := strings.TrimSpace(params.Arguments.Namespace)
			if ns == "" {
				ns = "argocd"
			}
			app := strings.TrimSpace(params.Arguments.AppName)
			if app == "" {
				return toolErr[ArgoCDSyncAppResult](fmt.Errorf("missing required field: appName"))
			}
			cluster := strings.TrimSpace(params.Arguments.Cluster)
			if cluster == "" {
				return toolErr[ArgoCDSyncAppResult](fmt.Errorf("missing required field: cluster"))
			}

			dyn, err := kube.BuildWorkloadDynamicClientByCAPICluster(ctx, params.Arguments.Context, cluster)
			if err != nil {
				return toolErr[ArgoCDSyncAppResult](err)
			}

			gvr := schema.GroupVersionResource{
				Group:    "argoproj.io",
				Version:  "v1alpha1",
				Resource: "applications",
			}

			prune := params.Arguments.Prune
			if !params.Arguments.Prune {
				// allow false explicitly; default true behavior:
				prune = true
			}

			patch := map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]any{
						"nfreconfig-mcp-server/sync-at": time.Now().UTC().Format(time.RFC3339Nano),
						"argocd.argoproj.io/refresh":     "hard",
					},
				},
				"operation": map[string]any{
					"sync": map[string]any{
						"prune": prune,
					},
				},
			}
			b, _ := json.Marshal(patch)

			_, err = dyn.Resource(gvr).Namespace(ns).Patch(ctx, app, types.MergePatchType, b, metav1.PatchOptions{})
			if err != nil {
				return toolErr[ArgoCDSyncAppResult](err)
			}

			return toolOK(ArgoCDSyncAppResult{Patched: true}), nil
		},
	}
}

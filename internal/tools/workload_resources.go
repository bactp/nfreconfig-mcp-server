package tools

import (
	"context"
	"fmt"
	"strings"

	"nfreconfig-mcp-server/internal/kube"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	registerTool(WorkloadListResource())
	registerTool(WorkloadGetResource())
	registerTool(WorkloadDeleteResource())
}

type WorkloadResourceParams struct {
	Context   string `json:"context,omitempty"`   // kubeconfig context for mgmt cluster; default current
	Cluster   string `json:"cluster"`             // CAPI Cluster name (e.g., 5g-edge)
	Namespace string `json:"namespace,omitempty"` // optional ("" for cluster-scope)
	Group     string `json:"group,omitempty"`
	Version   string `json:"version"`
	Resource  string `json:"resource"`
	Name      string `json:"name,omitempty"` // for get/delete
}

type WorkloadListResult struct {
	Items []map[string]any `json:"items"`
}

type WorkloadGetResult struct {
	Object map[string]any `json:"object"`
}

type WorkloadDeleteResult struct {
	Deleted bool   `json:"deleted"`
	Error   string `json:"error,omitempty"`
}

func WorkloadListResource() MCPTool[WorkloadResourceParams, WorkloadListResult] {
	return MCPTool[WorkloadResourceParams, WorkloadListResult]{
		Name:        "workload.list_resource",
		Description: "List resources from a workload cluster (selected by CAPI Cluster name).",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[WorkloadResourceParams]) (*mcp.CallToolResultFor[WorkloadListResult], error) {
			dyn, err := kube.BuildWorkloadDynamicClientByCAPICluster(ctx, params.Arguments.Context, params.Arguments.Cluster)
			if err != nil {
				return toolErr[WorkloadListResult](err)
			}

			gvr := schema.GroupVersionResource{
				Group:    strings.TrimSpace(params.Arguments.Group),
				Version:  strings.TrimSpace(params.Arguments.Version),
				Resource: strings.TrimSpace(params.Arguments.Resource),
			}
			ns := strings.TrimSpace(params.Arguments.Namespace)

			var ul *unstructured.UnstructuredList
			if ns == "" {
				ul, err = dyn.Resource(gvr).List(ctx, v1.ListOptions{})
			} else {
				ul, err = dyn.Resource(gvr).Namespace(ns).List(ctx, v1.ListOptions{})
			}
			if err != nil {
				return toolErr[WorkloadListResult](err)
			}

			items := make([]map[string]any, 0, len(ul.Items))
			for _, it := range ul.Items {
				items = append(items, it.Object)
			}
			return toolOK(WorkloadListResult{Items: items}), nil
		},
	}
}

func WorkloadGetResource() MCPTool[WorkloadResourceParams, WorkloadGetResult] {
	return MCPTool[WorkloadResourceParams, WorkloadGetResult]{
		Name:        "workload.get_resource",
		Description: "Get a resource from a workload cluster (selected by CAPI Cluster name).",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[WorkloadResourceParams]) (*mcp.CallToolResultFor[WorkloadGetResult], error) {
			if strings.TrimSpace(params.Arguments.Name) == "" {
				return toolErr[WorkloadGetResult](fmt.Errorf("missing required field: name"))
			}

			dyn, err := kube.BuildWorkloadDynamicClientByCAPICluster(ctx, params.Arguments.Context, params.Arguments.Cluster)
			if err != nil {
				return toolErr[WorkloadGetResult](err)
			}

			gvr := schema.GroupVersionResource{
				Group:    strings.TrimSpace(params.Arguments.Group),
				Version:  strings.TrimSpace(params.Arguments.Version),
				Resource: strings.TrimSpace(params.Arguments.Resource),
			}
			ns := strings.TrimSpace(params.Arguments.Namespace)
			name := strings.TrimSpace(params.Arguments.Name)

			var u *unstructured.Unstructured
			if ns == "" {
				u, err = dyn.Resource(gvr).Get(ctx, name, v1.GetOptions{})
			} else {
				u, err = dyn.Resource(gvr).Namespace(ns).Get(ctx, name, v1.GetOptions{})
			}
			if err != nil {
				return toolErr[WorkloadGetResult](err)
			}
			return toolOK(WorkloadGetResult{Object: u.Object}), nil
		},
	}
}

func WorkloadDeleteResource() MCPTool[WorkloadResourceParams, WorkloadDeleteResult] {
	return MCPTool[WorkloadResourceParams, WorkloadDeleteResult]{
		Name:        "workload.delete_resource",
		Description: "Delete a resource from a workload cluster (selected by CAPI Cluster name).",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[WorkloadResourceParams]) (*mcp.CallToolResultFor[WorkloadDeleteResult], error) {
			if strings.TrimSpace(params.Arguments.Name) == "" {
				return toolErr[WorkloadDeleteResult](fmt.Errorf("missing required field: name"))
			}

			dyn, err := kube.BuildWorkloadDynamicClientByCAPICluster(ctx, params.Arguments.Context, params.Arguments.Cluster)
			if err != nil {
				return toolErr[WorkloadDeleteResult](err)
			}

			gvr := schema.GroupVersionResource{
				Group:    strings.TrimSpace(params.Arguments.Group),
				Version:  strings.TrimSpace(params.Arguments.Version),
				Resource: strings.TrimSpace(params.Arguments.Resource),
			}
			ns := strings.TrimSpace(params.Arguments.Namespace)
			name := strings.TrimSpace(params.Arguments.Name)

			if ns == "" {
				err = dyn.Resource(gvr).Delete(ctx, name, v1.DeleteOptions{})
			} else {
				err = dyn.Resource(gvr).Namespace(ns).Delete(ctx, name, v1.DeleteOptions{})
			}
			if err != nil {
				return toolErr[WorkloadDeleteResult](err)
			}
			return toolOK(WorkloadDeleteResult{Deleted: true}), nil
		},
	}
}

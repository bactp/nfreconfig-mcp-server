package tools

import (
	"context"

	"nfreconfig-mcp-server/internal/kube"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() { registerTool(WorkloadListResources()) }

type WorkloadListResourcesParams struct {
	Cluster   string `json:"cluster" description:"Kubeconfig context name (from clusters.list)."`
	Group     string `json:"group" description:"API group, empty for core (e.g., apps)."`
	Version   string `json:"version" description:"API version (e.g., v1, v1beta1)."`
	Kind      string `json:"kind" description:"Kind (e.g., Pod, Node, Deployment)."`
	Namespace string `json:"namespace,omitempty" description:"Namespace; empty means cluster-scoped."`
	Limit     int64  `json:"limit,omitempty" description:"Optional list limit."`
}

type WorkloadListResourcesResult struct {
	Items   []map[string]any `json:"items"`
	Count   int              `json:"count"`
	Cluster string           `json:"cluster"`
}

func WorkloadListResources() MCPTool[WorkloadListResourcesParams, WorkloadListResourcesResult] {
	return MCPTool[WorkloadListResourcesParams, WorkloadListResourcesResult]{
		Name:        "workload.list_resources",
		Description: "List resources by GVK in a given cluster (context).",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[WorkloadListResourcesParams]) (*mcp.CallToolResultFor[WorkloadListResourcesResult], error) {
			req := params.Arguments

			dyn, restCfg, err := kube.DynamicClientForContext(req.Cluster)
			if err != nil {
				return toolErr[WorkloadListResourcesResult](err)
			}
			mapper, err := kube.RESTMapperForConfig(restCfg)
			if err != nil {
				return toolErr[WorkloadListResourcesResult](err)
			}

			gvk := schema.GroupVersionKind{Group: req.Group, Version: req.Version, Kind: req.Kind}
			mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
			if err != nil {
				return toolErr[WorkloadListResourcesResult](err)
			}

			var ul *unstructured.UnstructuredList
			if req.Namespace != "" {
				ul, err = dyn.Resource(mapping.Resource).Namespace(req.Namespace).List(ctx, listOpts(req.Limit))
			} else {
				ul, err = dyn.Resource(mapping.Resource).List(ctx, listOpts(req.Limit))
			}
			if err != nil {
				return toolErr[WorkloadListResourcesResult](err)
			}

			out := WorkloadListResourcesResult{Cluster: req.Cluster}
			for _, item := range ul.Items {
				out.Items = append(out.Items, item.Object)
			}
			out.Count = len(out.Items)

			return toolOK(out), nil
		},
	}
}

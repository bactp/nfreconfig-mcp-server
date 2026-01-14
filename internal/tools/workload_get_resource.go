package tools

import (
	"context"

	"nfreconfig-mcp-server/internal/kube"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() { registerTool(WorkloadGetResource()) }

type WorkloadGetResourceParams struct {
	Cluster   string `json:"cluster" description:"Kubeconfig context name (from clusters.list)."`
	Group     string `json:"group" description:"API group, empty for core."`
	Version   string `json:"version" description:"API version."`
	Kind      string `json:"kind" description:"Kind (e.g., Pod, Node, Deployment)."`
	Namespace string `json:"namespace,omitempty" description:"Namespace; empty for cluster-scoped."`
	Name      string `json:"name" description:"Resource name."`
}

type WorkloadGetResourceResult struct {
	Object  map[string]any `json:"object"`
	Cluster string         `json:"cluster"`
}

func WorkloadGetResource() MCPTool[WorkloadGetResourceParams, WorkloadGetResourceResult] {
	return MCPTool[WorkloadGetResourceParams, WorkloadGetResourceResult]{
		Name:        "workload.get_resource",
		Description: "Get a single resource by GVK + name in a given cluster (context).",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[WorkloadGetResourceParams]) (*mcp.CallToolResultFor[WorkloadGetResourceResult], error) {
			req := params.Arguments

			dyn, restCfg, err := kube.DynamicClientForContext(req.Cluster)
			if err != nil {
				return toolErr[WorkloadGetResourceResult](err)
			}
			mapper, err := kube.RESTMapperForConfig(restCfg)
			if err != nil {
				return toolErr[WorkloadGetResourceResult](err)
			}

			gvk := schema.GroupVersionKind{Group: req.Group, Version: req.Version, Kind: req.Kind}
			mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
			if err != nil {
				return toolErr[WorkloadGetResourceResult](err)
			}

			var obj map[string]any
			if req.Namespace != "" {
				u, err := dyn.Resource(mapping.Resource).Namespace(req.Namespace).Get(ctx, req.Name, getOpts())
				if err != nil {
					return toolErr[WorkloadGetResourceResult](err)
				}
				obj = u.Object
			} else {
				u, err := dyn.Resource(mapping.Resource).Get(ctx, req.Name, getOpts())
				if err != nil {
					return toolErr[WorkloadGetResourceResult](err)
				}
				obj = u.Object
			}

			return toolOK(WorkloadGetResourceResult{
				Object:  obj,
				Cluster: req.Cluster,
			}), nil
		},
	}
}

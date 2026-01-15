package tools

import (
	"context"
	"fmt"
	"strings"

	"nfreconfig-mcp-server/internal/kube"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	registerTool(WorkloadListResource())
	registerTool(WorkloadGetResource())
	registerTool(WorkloadDeleteResource())
}

type WorkloadResourceParams struct {
	Context   string `json:"context,omitempty"`   // mgmt kubeconfig context; default = current
	Cluster   string `json:"cluster"`             // CAPI Cluster name (e.g., 5g-edge)
	Kind      string `json:"kind"`                // e.g., NFDeployment, NetworkAttachmentDefinition, NFConfig, Config, Application
	Namespace string `json:"namespace,omitempty"` // list: "" or "*" => all namespaces; get/delete: must be set (namespaced kinds)
	Name      string `json:"name,omitempty"`      // for get/delete
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

// -------------------- defaults / helpers --------------------

func defaultMgmtContext(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit), nil
	}
	_, raw, err := kube.LoadRawConfig()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(raw.CurrentContext) == "" {
		return "", fmt.Errorf("kubeconfig has empty currentContext; provide params.context explicitly")
	}
	return raw.CurrentContext, nil
}

type kindSpec struct {
	GVR        schema.GroupVersionResource
	Namespaced bool
}

var kindMap = map[string]kindSpec{
	// Nephio workload CRDs
	"NFDeployment": {GVR: schema.GroupVersionResource{Group: "workload.nephio.org", Version: "v1alpha1", Resource: "nfdeployments"}, Namespaced: true},
	"NFConfig":     {GVR: schema.GroupVersionResource{Group: "workload.nephio.org", Version: "v1alpha1", Resource: "nfconfigs"}, Namespaced: true},

	// Nephio ref config
	"Config": {GVR: schema.GroupVersionResource{Group: "ref.nephio.org", Version: "v1alpha1", Resource: "configs"}, Namespaced: true},

	// Multus NAD
	"NetworkAttachmentDefinition": {GVR: schema.GroupVersionResource{Group: "k8s.cni.cncf.io", Version: "v1", Resource: "network-attachment-definitions"}, Namespaced: true},

	// ArgoCD Application (if you need to verify/sync on workload clusters)
	"Application": {GVR: schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}, Namespaced: true},
}

func resolveKind(kind string) (kindSpec, error) {
	k := strings.TrimSpace(kind)
	if k == "" {
		return kindSpec{}, fmt.Errorf("missing required field: kind")
	}
	if spec, ok := kindMap[k]; ok {
		return spec, nil
	}
	// helpful error
	allowed := make([]string, 0, len(kindMap))
	for kk := range kindMap {
		allowed = append(allowed, kk)
	}
	return kindSpec{}, fmt.Errorf("unsupported kind %q. allowed: %s", k, strings.Join(allowed, ", "))
}

func requireCluster(cluster string) (string, error) {
	c := strings.TrimSpace(cluster)
	if c == "" {
		return "", fmt.Errorf("missing required field: cluster")
	}
	return c, nil
}

func requireName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" {
		return "", fmt.Errorf("missing required field: name")
	}
	return n, nil
}

func cleanNamespace(ns string) string { return strings.TrimSpace(ns) }

// -------------------- tools --------------------

func WorkloadListResource() MCPTool[WorkloadResourceParams, WorkloadListResult] {
	return MCPTool[WorkloadResourceParams, WorkloadListResult]{
		Name:        "[workload]@list_resource",
		Description: "List resources from a workload cluster by Kind. For namespaced resources: namespace '' or '*' lists across all namespaces.",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[WorkloadResourceParams]) (*mcp.CallToolResultFor[WorkloadListResult], error) {
			cluster, err := requireCluster(params.Arguments.Cluster)
			if err != nil {
				return toolErr[WorkloadListResult](err)
			}

			ks, err := resolveKind(params.Arguments.Kind)
			if err != nil {
				return toolErr[WorkloadListResult](err)
			}

			mgmtCtx, err := defaultMgmtContext(params.Arguments.Context)
			if err != nil {
				return toolErr[WorkloadListResult](err)
			}

			dyn, err := kube.BuildWorkloadDynamicClientByCAPICluster(ctx, mgmtCtx, cluster)
			if err != nil {
				return toolErr[WorkloadListResult](err)
			}

			ns := cleanNamespace(params.Arguments.Namespace)

			var ul *unstructured.UnstructuredList
			if ks.Namespaced {
				// LIST namespaced
				if ns == "" || ns == "*" {
					ul, err = dyn.Resource(ks.GVR).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
				} else {
					ul, err = dyn.Resource(ks.GVR).Namespace(ns).List(ctx, metav1.ListOptions{})
				}
			} else {
				// LIST cluster-scoped (ignore namespace)
				ul, err = dyn.Resource(ks.GVR).List(ctx, metav1.ListOptions{})
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
		Name:        "[workload]@get_resource",
		Description: "Get a resource from a workload cluster by Kind. For namespaced resources, namespace is required.",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[WorkloadResourceParams]) (*mcp.CallToolResultFor[WorkloadGetResult], error) {
			cluster, err := requireCluster(params.Arguments.Cluster)
			if err != nil {
				return toolErr[WorkloadGetResult](err)
			}

			name, err := requireName(params.Arguments.Name)
			if err != nil {
				return toolErr[WorkloadGetResult](err)
			}

			ks, err := resolveKind(params.Arguments.Kind)
			if err != nil {
				return toolErr[WorkloadGetResult](err)
			}

			ns := cleanNamespace(params.Arguments.Namespace)
			if ks.Namespaced {
				if ns == "" || ns == "*" {
					return toolErr[WorkloadGetResult](fmt.Errorf("namespace is required for get_resource (set a concrete namespace, not empty/*)"))
				}
			}

			mgmtCtx, err := defaultMgmtContext(params.Arguments.Context)
			if err != nil {
				return toolErr[WorkloadGetResult](err)
			}

			dyn, err := kube.BuildWorkloadDynamicClientByCAPICluster(ctx, mgmtCtx, cluster)
			if err != nil {
				return toolErr[WorkloadGetResult](err)
			}

			var u *unstructured.Unstructured
			if ks.Namespaced {
				u, err = dyn.Resource(ks.GVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
			} else {
				u, err = dyn.Resource(ks.GVR).Get(ctx, name, metav1.GetOptions{})
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
		Name:        "[workload]@delete_resource",
		Description: "Delete a resource from a workload cluster by Kind. For namespaced resources, namespace is required.",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[WorkloadResourceParams]) (*mcp.CallToolResultFor[WorkloadDeleteResult], error) {
			cluster, err := requireCluster(params.Arguments.Cluster)
			if err != nil {
				return toolErr[WorkloadDeleteResult](err)
			}

			name, err := requireName(params.Arguments.Name)
			if err != nil {
				return toolErr[WorkloadDeleteResult](err)
			}

			ks, err := resolveKind(params.Arguments.Kind)
			if err != nil {
				return toolErr[WorkloadDeleteResult](err)
			}

			ns := cleanNamespace(params.Arguments.Namespace)
			if ks.Namespaced {
				if ns == "" || ns == "*" {
					return toolErr[WorkloadDeleteResult](fmt.Errorf("namespace is required for delete_resource (set a concrete namespace, not empty/*)"))
				}
			}

			mgmtCtx, err := defaultMgmtContext(params.Arguments.Context)
			if err != nil {
				return toolErr[WorkloadDeleteResult](err)
			}

			dyn, err := kube.BuildWorkloadDynamicClientByCAPICluster(ctx, mgmtCtx, cluster)
			if err != nil {
				return toolErr[WorkloadDeleteResult](err)
			}

			if ks.Namespaced {
				err = dyn.Resource(ks.GVR).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{})
			} else {
				err = dyn.Resource(ks.GVR).Delete(ctx, name, metav1.DeleteOptions{})
			}
			if err != nil {
				return toolErr[WorkloadDeleteResult](err)
			}

			return toolOK(WorkloadDeleteResult{Deleted: true}), nil
		},
	}
}

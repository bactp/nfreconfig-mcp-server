package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"nfreconfig-mcp-server/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"

)

// This devtest calls tool handlers directly (no inspector, no MCP transport).
// It proves: kubeconfig access + tool logic works.
func main() {
	ctx := context.Background()

	// 1) clusters.list
	{
		tool := tools.ClustersList()
		res, err := tool.Handler(ctx, nil, &mcp.CallToolParamsFor[tools.ClustersListParams]{Arguments: tools.ClustersListParams{}})
		if err != nil {
			fmt.Fprintf(os.Stderr, "clusters.list error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("=== clusters.list ===")
		printJSON(res.StructuredContent)
	}

	// Pick a cluster context automatically:
	// Use your current context shown in kubectl config current-context.
// 	cluster := os.Getenv("DEVTEST_CLUSTER")
// 	if cluster == "" {
// 		cluster = "kubernetes-admin@kubernetes"
// 	}

// 	// 2) workload.list_resources for Nodes (core/v1/Node, cluster-scoped)
// 	{
// 		tool := tools.WorkloadListResources()
// 		req := tools.WorkloadListResourcesParams{
// 			Cluster: cluster,
// 			Group:   "",
// 			Version: "v1",
// 			Kind:    "Node",
// 			Limit:   10,
// 		}
// 		res, err := tool.Handler(ctx, nil, &mcp.CallToolParamsFor[tools.WorkloadListResourcesParams]{Arguments: req})
// 		if err != nil {
// 			fmt.Fprintf(os.Stderr, "workload.list_resources error: %v\n", err)
// 			os.Exit(1)
// 		}
// 		fmt.Println("=== workload.list_resources (Node) ===")
// 		printJSON(res.StructuredContent)
// 	}

// 	// 3) workload.get_resource for mgmt-control Node (core/v1/Node)
// 	{
// 		tool := tools.WorkloadGetResource()
// 		req := tools.WorkloadGetResourceParams{
// 			Cluster: cluster,
// 			Group:   "",
// 			Version: "v1",
// 			Kind:    "Node",
// 			Name:    "mgmt-control",
// 		}
// 		res, err := tool.Handler(ctx, nil, &mcp.CallToolParamsFor[tools.WorkloadGetResourceParams]{Arguments: req})
// 		if err != nil {
// 			fmt.Fprintf(os.Stderr, "workload.get_resource error: %v\n", err)
// 			os.Exit(1)
// 		}
// 		fmt.Println("=== workload.get_resource (Node/mgmt-control) ===")
// 		printJSON(res.StructuredContent)
// 	}
 }

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}


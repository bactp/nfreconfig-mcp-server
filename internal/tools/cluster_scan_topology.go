package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"nfreconfig-mcp-server/internal/kube"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"
)

func init() { registerTool(ClusterScanTopology()) }

type ClusterScanTopologyParams struct {
	// Optional: filter by cluster name (exact match or prefix)
	ClusterName string `json:"clusterName,omitempty"`

	// Optional: if true, list all available clusters
	ListAll bool `json:"listAll,omitempty"`

	// Optional: also scan for network topology (CIDRs/IPs) in the cluster
	IncludeTopology bool `json:"includeTopology,omitempty"`

	// Optional: namespace filter for topology scan
	Namespace string `json:"namespace,omitempty"`
}

type ClusterTopologyInfo struct {
	// Cluster identity
	Name      string `json:"name"`
	Kind      string `json:"kind"`                // "KubeContext" | "CAPICluster"
	Namespace string `json:"namespace,omitempty"` // for CAPICluster
	Ready     bool   `json:"ready,omitempty"`

	// Connection info
	APIServer        string `json:"apiServer,omitempty"`
	KubeconfigSecret string `json:"kubeconfigSecret,omitempty"`

	// Git repository association
	GitRepoName string `json:"gitRepoName,omitempty"`
	GitURL      string `json:"gitURL,omitempty"`

	// Network topology (if requested)
	NetworkInfo *ClusterNetworkInfo `json:"networkInfo,omitempty"`
}

type ClusterNetworkInfo struct {
	// Pod/Service CIDRs from cluster configuration
	PodCIDRs     []string `json:"podCidrs,omitempty"`
	ServiceCIDRs []string `json:"serviceCidrs,omitempty"`

	// Discovered network interfaces from NADs and other resources
	NetworkInterfaces []NetworkInterface `json:"networkInterfaces,omitempty"`

	// All discovered IPs and CIDRs (flat lists)
	AllCIDRs []string `json:"allCidrs,omitempty"`
	AllIPs   []string `json:"allIps,omitempty"`
}

type ClusterScanTopologyResult struct {
	Query    string                `json:"query,omitempty"`
	Clusters []ClusterTopologyInfo `json:"clusters"`
	Total    int                   `json:"total"`
	Message  string                `json:"message,omitempty"`
}

func ClusterScanTopology() MCPTool[ClusterScanTopologyParams, ClusterScanTopologyResult] {
	return MCPTool[ClusterScanTopologyParams, ClusterScanTopologyResult]{
		Name:        "cluster_scan_topology",
		Description: "Discover clusters with their Git repositories and network topology. Use for Phase 1 discovery: find target clusters (core/edge/regional), get current IP/CIDR allocations, pod/service CIDRs, and associated git URLs. Example: {\"clusterName\":\"regional\", \"includeTopology\":true} returns cluster info with networkInterfaces (name, IPs, CIDRs), podCidrs, serviceCidrs, and gitURL.",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[ClusterScanTopologyParams]) (*mcp.CallToolResultFor[ClusterScanTopologyResult], error) {
			clusterName := strings.TrimSpace(params.Arguments.ClusterName)
			listAll := params.Arguments.ListAll
			includeTopology := params.Arguments.IncludeTopology
			namespace := strings.TrimSpace(params.Arguments.Namespace)

			// Load kubeconfig
			_, raw, err := kube.LoadRawConfig()
			if err != nil {
				return toolErr[ClusterScanTopologyResult](err)
			}

			// Build clients for management cluster
			dyn, err := kube.BuildDynamicClient(raw.CurrentContext)
			if err != nil {
				return toolErr[ClusterScanTopologyResult](fmt.Errorf("build dynamic client: %w", err))
			}
			cs, err := kube.BuildClientset(raw.CurrentContext)
			if err != nil {
				return toolErr[ClusterScanTopologyResult](fmt.Errorf("build clientset: %w", err))
			}

			result := ClusterScanTopologyResult{
				Clusters: []ClusterTopologyInfo{},
			}

			if clusterName != "" {
				result.Query = clusterName
			}

			// 1. Collect clusters from kubeconfig contexts
			ctxNames := make([]string, 0, len(raw.Contexts))
			for name := range raw.Contexts {
				if !listAll && clusterName != "" {
					// Filter by name (exact or prefix match)
					if !strings.Contains(strings.ToLower(name), strings.ToLower(clusterName)) {
						continue
					}
				}
				ctxNames = append(ctxNames, name)
			}
			sort.Strings(ctxNames)

			for _, name := range ctxNames {
				info := ClusterTopologyInfo{
					Name: name,
					Kind: "KubeContext",
				}

				// Try to get API server from kubeconfig
				if ctxCfg, ok := raw.Contexts[name]; ok && ctxCfg != nil {
					if cluster, ok := raw.Clusters[ctxCfg.Cluster]; ok && cluster != nil {
						info.APIServer = cluster.Server
					}
				}

				// Try to find associated git repo (by naming convention)
				gitInfo := findGitRepoForCluster(ctx, dyn, cs.Discovery(), name)
				info.GitRepoName = gitInfo.Name
				info.GitURL = gitInfo.URL

				// Optionally scan topology
				if includeTopology {
					netInfo, err := scanClusterTopology(ctx, name, namespace)
					if err == nil && netInfo != nil {
						info.NetworkInfo = netInfo
					}
				}

				result.Clusters = append(result.Clusters, info)
			}

			// 2. Collect CAPI clusters from management cluster
			capiGVR := schema.GroupVersionResource{
				Group:    "cluster.x-k8s.io",
				Version:  "v1beta1",
				Resource: "clusters",
			}

			ul, err := dyn.Resource(capiGVR).Namespace("").List(ctx, metav1.ListOptions{})
			if err == nil && ul != nil {
				for _, it := range ul.Items {
					name := it.GetName()
					ns := it.GetNamespace()

					if !listAll && clusterName != "" {
						// Filter by name
						if !strings.Contains(strings.ToLower(name), strings.ToLower(clusterName)) {
							continue
						}
					}

					ready := isCAPIClusterReady(&it)
					secretName := name + "-kubeconfig"
					secretRef := ns + "/" + secretName

					info := ClusterTopologyInfo{
						Name:             name,
						Kind:             "CAPICluster",
						Namespace:        ns,
						Ready:            ready,
						KubeconfigSecret: secretRef,
					}

					// Extract API server from kubeconfig secret
					var kubeBytes []byte
					sec, secErr := cs.CoreV1().Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
					if secErr == nil {
						kubeBytes = extractKubeconfigFromSecret(sec)
						if len(kubeBytes) > 0 {
							if apiServer := extractAPIServerFromKubeconfig(kubeBytes); apiServer != "" {
								info.APIServer = apiServer
							}
						}
					}

					// Try to find associated git repo
					gitInfo := findGitRepoForCluster(ctx, dyn, cs.Discovery(), name)
					info.GitRepoName = gitInfo.Name
					info.GitURL = gitInfo.URL

					// Optionally scan topology using the CAPI kubeconfig secret
					if includeTopology {
						if len(kubeBytes) > 0 {
							dynC, csC, err := clientsFromKubeconfigBytes(kubeBytes)
							if err == nil {
								if netInfo, err2 := scanClusterTopologyWithClients(ctx, dynC, csC, namespace); err2 == nil {
									info.NetworkInfo = netInfo
								}
							}
						}
					}

					result.Clusters = append(result.Clusters, info)
				}
			}

			result.Total = len(result.Clusters)
			if result.Total == 0 {
				result.Message = "No cluster is available that matches the criteria."
			}

			return toolOK(result), nil
		},
	}
}

// ---- Helper functions ----

type gitRepoInfo struct {
	Name string
	URL  string
}

func findGitRepoForCluster(ctx context.Context, dyn dynamic.Interface, discovery interface {
	ServerPreferredResources() ([]*metav1.APIResourceList, error)
}, clusterName string) gitRepoInfo {
	// Try to discover Repository GVR
	gvr, namespaced, err := discoverRepositoryGVR(discovery)
	if err != nil {
		return gitRepoInfo{}
	}

	// List repositories
	var ul *unstructured.UnstructuredList
	if namespaced {
		ul, err = dyn.Resource(gvr).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	} else {
		ul, err = dyn.Resource(gvr).List(ctx, metav1.ListOptions{})
	}
	if err != nil || ul == nil {
		return gitRepoInfo{}
	}

	// Look for matching repository by name patterns
	// Common patterns: "5g-{cluster}", "{cluster}-repo", etc.
	clusterLower := strings.ToLower(clusterName)

	for _, repo := range ul.Items {
		repoName := repo.GetName()
		repoNameLower := strings.ToLower(repoName)

		// Check for common naming patterns
		if strings.Contains(repoNameLower, clusterLower) ||
			strings.HasPrefix(repoNameLower, "5g-"+clusterLower) ||
			strings.HasPrefix(repoNameLower, clusterLower) {

			url := extractRepoAddress(&repo)
			if url != "" {
				return gitRepoInfo{
					Name: repoName,
					URL:  url,
				}
			}
		}
	}

	return gitRepoInfo{}
}

// scanClusterTopology connects to a cluster and scans for network topology
func scanClusterTopology(ctx context.Context, clusterContext string, namespace string) (*ClusterNetworkInfo, error) {
	// Build clients for the target cluster using context name
	dyn, err := kube.BuildDynamicClient(clusterContext)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client for %s: %w", clusterContext, err)
	}
	cs, err := kube.BuildClientset(clusterContext)
	if err != nil {
		return nil, fmt.Errorf("build clientset for %s: %w", clusterContext, err)
	}
	return scanClusterTopologyWithClients(ctx, dyn, cs, namespace)
}

func scanClusterTopologyWithClients(ctx context.Context, dyn dynamic.Interface, cs *kubernetes.Clientset, namespace string) (*ClusterNetworkInfo, error) {
	netInfo := &ClusterNetworkInfo{
		NetworkInterfaces: []NetworkInterface{},
		AllCIDRs:          []string{},
		AllIPs:            []string{},
	}

	seenCIDR := make(map[string]bool)
	seenIP := make(map[string]bool)

	// Scan for NetworkAttachmentDefinitions
	nadGVR := schema.GroupVersionResource{
		Group:    "k8s.cni.cncf.io",
		Version:  "v1",
		Resource: "network-attachment-definitions",
	}

	ns := namespace
	if ns == "" {
		ns = metav1.NamespaceAll
	}

	nadList, err := dyn.Resource(nadGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err == nil && nadList != nil {
		for _, nad := range nadList.Items {
			// Extract network topology from NAD
			ifaces := extractNetworkInterfaces(nad.Object)
			netInfo.NetworkInterfaces = append(netInfo.NetworkInterfaces, ifaces...)

			// Also extract flat CIDRs/IPs
			cidrs, ips := extractAllCIDRsAndIPv4Strings(nad.Object)
			for _, c := range cidrs {
				if !seenCIDR[c] {
					netInfo.AllCIDRs = append(netInfo.AllCIDRs, c)
					seenCIDR[c] = true
				}
			}
			for _, ip := range ips {
				if !seenIP[ip] {
					netInfo.AllIPs = append(netInfo.AllIPs, ip)
					seenIP[ip] = true
				}
			}

			// Parse spec.config JSON for additional info
			spec, _, _ := unstructured.NestedMap(nad.Object, "spec")
			if cfg, ok := spec["config"].(string); ok && strings.TrimSpace(cfg) != "" {
				if jm, ok := tryParseJSONConfigString(cfg); ok {
					c2, i2 := extractAllCIDRsAndIPv4Strings(jm)
					for _, c := range c2 {
						if !seenCIDR[c] {
							netInfo.AllCIDRs = append(netInfo.AllCIDRs, c)
							seenCIDR[c] = true
						}
					}
					for _, ip := range i2 {
						if !seenIP[ip] {
							netInfo.AllIPs = append(netInfo.AllIPs, ip)
							seenIP[ip] = true
						}
					}
				}
			}
		}
	}

	// Scan for NFConfig and Config resources
	nfConfigGVR := schema.GroupVersionResource{
		Group:    "workload.nephio.org",
		Version:  "v1alpha1",
		Resource: "nfconfigs",
	}

	nfList, err := dyn.Resource(nfConfigGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err == nil && nfList != nil {
		for _, nf := range nfList.Items {
			ifaces := extractNetworkInterfaces(nf.Object)
			netInfo.NetworkInterfaces = append(netInfo.NetworkInterfaces, ifaces...)

			cidrs, ips := extractAllCIDRsAndIPv4Strings(nf.Object)
			for _, c := range cidrs {
				if !seenCIDR[c] {
					netInfo.AllCIDRs = append(netInfo.AllCIDRs, c)
					seenCIDR[c] = true
				}
			}
			for _, ip := range ips {
				if !seenIP[ip] {
					netInfo.AllIPs = append(netInfo.AllIPs, ip)
					seenIP[ip] = true
				}
			}
		}
	}

	// Cluster-level CIDRs (pod/service) best effort
	podCIDRs, svcCIDRs := getClusterCIDRs(ctx, cs)
	netInfo.PodCIDRs = podCIDRs
	netInfo.ServiceCIDRs = svcCIDRs

	// Sort results
	sort.Strings(netInfo.AllCIDRs)
	sort.Strings(netInfo.AllIPs)

	// Deduplicate network interfaces by name
	ifaceMap := make(map[string]NetworkInterface)
	for _, iface := range netInfo.NetworkInterfaces {
		if existing, ok := ifaceMap[iface.Name]; ok {
			// Merge CIDRs and IPs
			existing.CIDRs = append(existing.CIDRs, iface.CIDRs...)
			existing.IPs = append(existing.IPs, iface.IPs...)
			ifaceMap[iface.Name] = existing
		} else {
			ifaceMap[iface.Name] = iface
		}
	}

	netInfo.NetworkInterfaces = make([]NetworkInterface, 0, len(ifaceMap))
	for _, iface := range ifaceMap {
		sort.Strings(iface.CIDRs)
		sort.Strings(iface.IPs)
		netInfo.NetworkInterfaces = append(netInfo.NetworkInterfaces, iface)
	}
	sort.Slice(netInfo.NetworkInterfaces, func(i, j int) bool {
		return netInfo.NetworkInterfaces[i].Name < netInfo.NetworkInterfaces[j].Name
	})

	return netInfo, nil
}

func extractKubeconfigFromSecret(sec *corev1.Secret) []byte {
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

	// Some secrets might store base64 string in Data (rare)
	if len(kubeBytes) > 0 && looksBase64(kubeBytes) {
		if dec, e := base64.StdEncoding.DecodeString(strings.TrimSpace(string(kubeBytes))); e == nil {
			kubeBytes = dec
		}
	}

	return kubeBytes
}

// clientsFromKubeconfigBytes builds dynamic and typed clients from raw kubeconfig bytes.
func clientsFromKubeconfigBytes(kubeconfig []byte) (dynamic.Interface, *kubernetes.Clientset, error) {
	if len(kubeconfig) == 0 {
		return nil, nil, fmt.Errorf("empty kubeconfig bytes")
	}
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("parse kubeconfig: %w", err)
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("dynamic client: %w", err)
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("clientset: %w", err)
	}
	return dyn, cs, nil
}

// getClusterCIDRs extracts pod CIDRs from Nodes and service CIDRs from kube-proxy config (best effort).
func getClusterCIDRs(ctx context.Context, cs *kubernetes.Clientset) (podCIDRs []string, serviceCIDRs []string) {
	podSeen := map[string]bool{}
	if cs != nil {
		if nl, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
			for i := range nl.Items {
				n := &nl.Items[i]
				if c := strings.TrimSpace(n.Spec.PodCIDR); c != "" && !podSeen[c] {
					podSeen[c] = true
					podCIDRs = append(podCIDRs, c)
				}
				for _, c := range n.Spec.PodCIDRs {
					c = strings.TrimSpace(c)
					if c != "" && !podSeen[c] {
						podSeen[c] = true
						podCIDRs = append(podCIDRs, c)
					}
				}
			}
		}
	}

	svcSeen := map[string]bool{}
	if cs != nil {
		if cm, err := cs.CoreV1().ConfigMaps("kube-system").Get(ctx, "kube-proxy", metav1.GetOptions{}); err == nil && cm != nil {
			for _, key := range []string{"config.conf", "kube-proxy.conf"} {
				if raw, ok := cm.Data[key]; ok && strings.TrimSpace(raw) != "" {
					var m map[string]any
					if yamlErr := yaml.Unmarshal([]byte(raw), &m); yamlErr == nil {
						if c, ok := m["clusterCIDR"].(string); ok {
							c = strings.TrimSpace(c)
							if c != "" && !svcSeen[c] {
								svcSeen[c] = true
								serviceCIDRs = append(serviceCIDRs, c)
							}
						}
						if arr, ok := m["clusterCIDRs"].([]any); ok {
							for _, v := range arr {
								if s, ok := v.(string); ok {
									s = strings.TrimSpace(s)
									if s != "" && !svcSeen[s] {
										svcSeen[s] = true
										serviceCIDRs = append(serviceCIDRs, s)
									}
								}
							}
						}
					}
				}
			}
		}
	}

	sort.Strings(podCIDRs)
	sort.Strings(serviceCIDRs)
	return podCIDRs, serviceCIDRs
}

package kube

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func BuildWorkloadDynamicClientByCAPICluster(ctx context.Context, mgmtContext string, capiClusterName string) (dynamic.Interface, error) {
	_, raw, err := LoadRawConfig()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(mgmtContext) == "" {
		mgmtContext = raw.CurrentContext
	}

	// mgmt clientset + dynamic
	cs, err := BuildClientset(mgmtContext)
	if err != nil {
		return nil, err
	}
	dynMgmt, err := BuildDynamicClient(mgmtContext)
	if err != nil {
		return nil, err
	}

	// find Cluster object by name across namespaces
	capiGVR := schema.GroupVersionResource{Group: "cluster.x-k8s.io", Version: "v1beta1", Resource: "clusters"}
	ul, err := dynMgmt.Resource(capiGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list CAPI clusters: %w", err)
	}

	var ns string
	for _, it := range ul.Items {
		if it.GetName() == capiClusterName {
			ns = it.GetNamespace()
			break
		}
	}
	if ns == "" {
		return nil, fmt.Errorf("CAPI Cluster %q not found", capiClusterName)
	}

	secretName := capiClusterName + "-kubeconfig"
	sec, err := cs.CoreV1().Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get kubeconfig secret %s/%s: %w", ns, secretName, err)
	}

	kubeBytes := sec.Data["value"]
	if len(kubeBytes) == 0 {
		kubeBytes = sec.Data["kubeconfig"]
	}
	if len(kubeBytes) == 0 {
		return nil, fmt.Errorf("kubeconfig secret %s/%s missing data[value|kubeconfig]", ns, secretName)
	}

	rc, err := clientcmd.RESTConfigFromKubeConfig(kubeBytes)
	if err != nil {
		return nil, fmt.Errorf("RESTConfigFromKubeConfig: %w", err)
	}
	return dynamic.NewForConfig(rc)
}

// optional: if you later need typed clientset to workload cluster
func BuildWorkloadClientsetByCAPICluster(ctx context.Context, mgmtContext, capiClusterName string) (*kubernetes.Clientset, error) {
	_, raw, err := LoadRawConfig()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(mgmtContext) == "" {
		mgmtContext = raw.CurrentContext
	}
	cs, err := BuildClientset(mgmtContext)
	if err != nil {
		return nil, err
	}
	dynMgmt, err := BuildDynamicClient(mgmtContext)
	if err != nil {
		return nil, err
	}

	capiGVR := schema.GroupVersionResource{Group: "cluster.x-k8s.io", Version: "v1beta1", Resource: "clusters"}
	ul, err := dynMgmt.Resource(capiGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list CAPI clusters: %w", err)
	}

	var ns string
	for _, it := range ul.Items {
		if it.GetName() == capiClusterName {
			ns = it.GetNamespace()
			break
		}
	}
	if ns == "" {
		return nil, fmt.Errorf("CAPI Cluster %q not found", capiClusterName)
	}

	secretName := capiClusterName + "-kubeconfig"
	sec, err := cs.CoreV1().Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get kubeconfig secret %s/%s: %w", ns, secretName, err)
	}

	kubeBytes := sec.Data["value"]
	if len(kubeBytes) == 0 {
		kubeBytes = sec.Data["kubeconfig"]
	}
	if len(kubeBytes) == 0 {
		return nil, fmt.Errorf("kubeconfig secret %s/%s missing data[value|kubeconfig]", ns, secretName)
	}

	rc, err := clientcmd.RESTConfigFromKubeConfig(kubeBytes)
	if err != nil {
		return nil, fmt.Errorf("RESTConfigFromKubeConfig: %w", err)
	}
	return kubernetes.NewForConfig(rc)
}

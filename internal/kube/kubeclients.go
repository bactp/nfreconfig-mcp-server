package kube

import (
	"fmt"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// BuildRESTConfig returns a Kubernetes REST config.
// Priority: in-cluster config if available; otherwise kubeconfig file.
// If contextName == "", uses kubeconfig current-context or in-cluster config.
// If contextName != "" and not "in-cluster", requires kubeconfig file.
func BuildRESTConfig(contextName string) (*rest.Config, error) {
	// If no context specified or explicitly "in-cluster", try in-cluster first
	if contextName == "" || contextName == "in-cluster" {
		if cfg, err := rest.InClusterConfig(); err == nil {
			return cfg, nil
		}
	}

	// Fall back to local kubeconfig (or required for specific contexts)
	kubeconfig := DefaultKubeconfigPath()
	if kubeconfig == "" {
		return nil, fmt.Errorf("cannot determine kubeconfig path (KUBECONFIG not set and no home dir)")
	}

	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" && contextName != "in-cluster" {
		overrides.CurrentContext = contextName
	}

	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
	return cc.ClientConfig()
}

func BuildClientset(contextName string) (*kubernetes.Clientset, error) {
	cfg, err := BuildRESTConfig(contextName)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

func BuildDynamicClient(contextName string) (dynamic.Interface, error) {
	cfg, err := BuildRESTConfig(contextName)
	if err != nil {
		return nil, err
	}
	return dynamic.NewForConfig(cfg)
}

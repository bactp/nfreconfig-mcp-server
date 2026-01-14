package kube

import (
	"fmt"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// DynamicClientForContext builds a dynamic client for a specific kubeconfig context name.
func DynamicClientForContext(contextName string) (dynamic.Interface, *rest.Config, error) {
	kubeconfig := DefaultKubeconfigPath()
	if kubeconfig == "" {
		return nil, nil, fmt.Errorf("cannot determine kubeconfig path")
	}

	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}

	cfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig},
		overrides,
	)

	restCfg, err := cfg.ClientConfig()
	if err != nil {
		return nil, nil, err
	}

	dc, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, nil, err
	}

	return dc, restCfg, nil
}

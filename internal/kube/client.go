package kube

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/util/homedir"
)

func DefaultKubeconfigPath() string {
	if v := os.Getenv("KUBECONFIG"); v != "" {
		return v
	}
	if home := homedir.HomeDir(); home != "" {
		return filepath.Join(home, ".kube", "config")
	}
	return ""
}

// IsInCluster checks if running inside a Kubernetes pod
func IsInCluster() bool {
	_, err := rest.InClusterConfig()
	return err == nil
}

func LoadRawConfig() (clientcmd.ClientConfig, api.Config, error) {
	// Try in-cluster config first (when running inside a pod)
	if IsInCluster() {
		// Build a synthetic config for in-cluster
		restConfig, err := rest.InClusterConfig()
		if err != nil {
			return nil, api.Config{}, fmt.Errorf("in-cluster config: %w", err)
		}

		// Create a synthetic kubeconfig API object
		rawCfg := api.Config{
			Clusters: map[string]*api.Cluster{
				"in-cluster": {
					Server:                   restConfig.Host,
					CertificateAuthorityData: restConfig.CAData,
				},
			},
			AuthInfos: map[string]*api.AuthInfo{
				"in-cluster": {
					Token: restConfig.BearerToken,
				},
			},
			Contexts: map[string]*api.Context{
				"in-cluster": {
					Cluster:  "in-cluster",
					AuthInfo: "in-cluster",
				},
			},
			CurrentContext: "in-cluster",
		}

		cfg := clientcmd.NewDefaultClientConfig(rawCfg, &clientcmd.ConfigOverrides{})
		return cfg, rawCfg, nil
	}

	// Fall back to kubeconfig file
	kubeconfig := DefaultKubeconfigPath()
	if kubeconfig == "" {
		return nil, api.Config{}, fmt.Errorf("cannot determine kubeconfig path (KUBECONFIG not set and no home dir)")
	}

	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
	cfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})

	rawCfg, err := cfg.RawConfig()
	if err != nil {
		return nil, api.Config{}, err
	}

	return cfg, rawCfg, nil
}

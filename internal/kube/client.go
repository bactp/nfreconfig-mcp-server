package kube

import (
	"fmt"
	"os"
	"path/filepath"

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

func LoadRawConfig() (clientcmd.ClientConfig, api.Config, error) {
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

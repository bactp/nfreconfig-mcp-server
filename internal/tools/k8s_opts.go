package tools

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

func listOpts(limit int64) metav1.ListOptions {
	if limit > 0 {
		return metav1.ListOptions{Limit: limit}
	}
	return metav1.ListOptions{}
}


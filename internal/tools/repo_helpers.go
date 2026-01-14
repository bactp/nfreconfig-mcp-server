package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func cleanPath(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"'")
	return s
}

func readYAMLFile(absPath string) (*unstructured.Unstructured, []byte, error) {
	b, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, b, err
	}
	u := &unstructured.Unstructured{Object: m}
	if u.GetKind() == "" || u.GetAPIVersion() == "" {
		return u, b, nil // still return for generic string patch
	}
	return u, b, nil
}

func writeYAMLFile(absPath string, obj map[string]any) error {
	out, err := yaml.Marshal(obj)
	if err != nil {
		return err
	}
	// best effort: keep trailing newline
	if len(out) == 0 || out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	tmp := absPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, absPath)
}

func absJoin(workdir, rel string) string {
	return filepath.Join(cleanPath(workdir), filepath.FromSlash(strings.TrimSpace(rel)))
}

// Walk arbitrary YAML object tree
func walkAny(v any, fn func(path []string, key string, parent map[string]any, val any)) {
	var rec func(path []string, cur any)
	rec = func(path []string, cur any) {
		switch x := cur.(type) {
		case map[string]any:
			for k, vv := range x {
				fn(path, k, x, vv)
				rec(append(path, k), vv)
			}
		case []any:
			for i, vv := range x {
				rec(append(path, fmt.Sprintf("[%d]", i)), vv)
			}
		}
	}
	rec(nil, v)
}

var cidrRe = regexp.MustCompile(`\b(\d{1,3}\.){3}\d{1,3}/\d{1,2}\b`)
var ipv4Re = regexp.MustCompile(`\b(\d{1,3}\.){3}\d{1,3}\b`)

func extractAllCIDRsAndIPv4Strings(obj map[string]any) (cidrs []string, ips []string) {
	seenC := map[string]struct{}{}
	seenI := map[string]struct{}{}
	walkAny(obj, func(_ []string, _ string, _ map[string]any, val any) {
		s, ok := val.(string)
		if !ok {
			return
		}
		for _, m := range cidrRe.FindAllString(s, -1) {
			if _, ok := seenC[m]; !ok {
				seenC[m] = struct{}{}
				cidrs = append(cidrs, m)
			}
		}
		for _, m := range ipv4Re.FindAllString(s, -1) {
			if _, ok := seenI[m]; !ok {
				seenI[m] = struct{}{}
				ips = append(ips, m)
			}
		}
	})
	return
}

// NAD.spec.config often contains JSON string; parse if possible.
func tryParseJSONConfigString(s string) (map[string]any, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, false
	}
	return m, true
}

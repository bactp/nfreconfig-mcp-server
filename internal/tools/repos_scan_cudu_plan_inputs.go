package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func init() { registerTool(RepoScanManifestsMany()) }

type RepoWorkdir struct {
	Name    string `json:"name"`
	Workdir string `json:"workdir"`
}

type RepoScanManifestsManyParams struct {
	Repos []RepoWorkdir `json:"repos"`           // required
	Kinds []string      `json:"kinds,omitempty"` // default ["NFDeployment","NetworkAttachmentDefinition","NFConfig","Config"]

	MaxFiles int `json:"maxFiles,omitempty"` // default 5000

	// NEW:
	IncludeTopology bool `json:"includeTopology,omitempty"` // default true (recommended)
	IncludeRaw      bool `json:"includeRaw,omitempty"`      // default false (debug/heavy)
}

type NetworkInterface struct {
	Name  string   `json:"name"`            // interface name (e.g., "n2", "n3", "eth0")
	CIDRs []string `json:"cidrs,omitempty"` // CIDRs associated with this interface
	IPs   []string `json:"ips,omitempty"`   // IPs associated with this interface
}

type FoundObject struct {
	Repo       string `json:"repo"`
	File       string `json:"file"` // repo-relative path
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion,omitempty"`
	Name       string `json:"name,omitempty"`
	Namespace  string `json:"namespace,omitempty"`

	// NEW: Structured network topology with interface associations
	NetworkInterfaces []NetworkInterface `json:"networkInterfaces,omitempty"`

	// Legacy: flat lists (still provided for backward compatibility)
	CIDRs []string `json:"cidrs,omitempty"`
	IPs   []string `json:"ips,omitempty"`

	// NEW: for NAD, also parse spec.config JSON string and extract from it
	NADConfigCIDRs []string `json:"nadConfigCidrs,omitempty"`
	NADConfigIPs   []string `json:"nadConfigIps,omitempty"`

	// Optional debug:
	Raw map[string]any `json:"raw,omitempty"`
}

type RepoScanResult struct {
	Repo    string        `json:"repo"`
	Workdir string        `json:"workdir"`
	Found   []FoundObject `json:"found"`
	Errors  []string      `json:"errors,omitempty"`
}

type RepoScanManifestsManyResult struct {
	Results []RepoScanResult `json:"results"`
}

func RepoScanManifestsMany() MCPTool[RepoScanManifestsManyParams, RepoScanManifestsManyResult] {
	return MCPTool[RepoScanManifestsManyParams, RepoScanManifestsManyResult]{
		Name:        "[repo]@scan_manifests",
		Description: "Scan repository workdirs for K8s manifests (NFDeployment, NAD, NFConfig, Config). Returns file paths, object metadata, and network topology with interface-to-IP/CIDR mappings. Use to find which files to patch. Example: {\"repos\":[{\"name\":\"cucp\",\"workdir\":\"/work/cucp\"}], \"kinds\":[\"NFDeployment\",\"NetworkAttachmentDefinition\"], \"includeTopology\":true}.",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[RepoScanManifestsManyParams]) (*mcp.CallToolResultFor[RepoScanManifestsManyResult], error) {
			repos := make([]RepoWorkdir, 0, len(params.Arguments.Repos))
			for _, r := range params.Arguments.Repos {
				r.Name = strings.TrimSpace(r.Name)
				r.Workdir = strings.TrimSpace(r.Workdir)
				r.Workdir = strings.Trim(r.Workdir, "\"'") // harden against embedded quotes
				if r.Name == "" || r.Workdir == "" {
					continue
				}
				repos = append(repos, r)
			}
			if len(repos) == 0 {
				return toolErr[RepoScanManifestsManyResult](fmt.Errorf("missing required field: repos (non-empty array of {name,workdir})"))
			}

			wantKinds := toSet(params.Arguments.Kinds)
			if len(wantKinds) == 0 {
				wantKinds = toSet([]string{"NFDeployment", "NetworkAttachmentDefinition", "NFConfig", "Config"})
			}

			maxFiles := params.Arguments.MaxFiles
			if maxFiles <= 0 {
				maxFiles = 5000
			}

			// Defaults: includeTopology=true unless explicitly false.
			includeTopology := true
			if params.Arguments.IncludeTopology == false {
				includeTopology = false
			}
			includeRaw := params.Arguments.IncludeRaw

			out := RepoScanManifestsManyResult{
				Results: make([]RepoScanResult, 0, len(repos)),
			}

			for _, r := range repos {
				res := RepoScanResult{
					Repo:    r.Name,
					Workdir: r.Workdir,
					Found:   []FoundObject{},
					Errors:  []string{},
				}

				count := 0
				walkErr := filepath.WalkDir(r.Workdir, func(path string, d fs.DirEntry, err error) error {
					if err != nil {
						res.Errors = append(res.Errors, fmt.Sprintf("walk error: %s: %v", path, err))
						return nil
					}
					if d.IsDir() {
						if d.Name() == ".git" {
							return fs.SkipDir
						}
						return nil
					}

					ext := strings.ToLower(filepath.Ext(d.Name()))
					if ext != ".yaml" && ext != ".yml" {
						return nil
					}

					count++
					if count > maxFiles {
						return fs.SkipAll
					}

					rel, _ := filepath.Rel(r.Workdir, path)
					relSlash := filepath.ToSlash(rel)

					b, readErr := os.ReadFile(path)
					if readErr != nil {
						res.Errors = append(res.Errors, fmt.Sprintf("read error: %s: %v", relSlash, readErr))
						return nil
					}

					docs := splitYAMLDocuments(string(b))
					for _, doc := range docs {
						doc = strings.TrimSpace(doc)
						if doc == "" {
							continue
						}

						obj, parseErr := parseYAMLToUnstructured([]byte(doc))
						if parseErr != nil || obj == nil {
							// ignore non-k8s or invalid docs
							continue
						}

						kind := strings.TrimSpace(obj.GetKind())
						if kind == "" {
							continue
						}
						if _, ok := wantKinds[kind]; !ok {
							continue
						}

						fo := FoundObject{
							Repo:       r.Name,
							File:       relSlash,
							Kind:       kind,
							APIVersion: obj.GetAPIVersion(),
							Name:       obj.GetName(),
							Namespace:  obj.GetNamespace(),
						}

						if includeTopology {
							// Extract structured network interfaces with IP/CIDR associations
							fo.NetworkInterfaces = extractNetworkInterfaces(obj.Object)

							// Legacy flat lists for backward compatibility
							cidrs, ips := extractAllCIDRsAndIPv4Strings(obj.Object)
							sort.Strings(cidrs)
							sort.Strings(ips)
							fo.CIDRs = cidrs
							fo.IPs = ips

							// NAD spec.config JSON string extraction
							if kind == "NetworkAttachmentDefinition" {
								spec, _, _ := unstructured.NestedMap(obj.Object, "spec")
								if cfg, ok := spec["config"].(string); ok && strings.TrimSpace(cfg) != "" {
									if jm, ok := tryParseJSONConfigString(cfg); ok {
										c2, i2 := extractAllCIDRsAndIPv4Strings(jm)
										sort.Strings(c2)
										sort.Strings(i2)
										fo.NADConfigCIDRs = c2
										fo.NADConfigIPs = i2
									}
								}
							}
						}

						if includeRaw {
							fo.Raw = obj.Object
						}

						res.Found = append(res.Found, fo)
					}

					return nil
				})

				if walkErr != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("walk failed: %v", walkErr))
				}

				out.Results = append(out.Results, res)
			}

			return toolOK(out), nil
		},
	}
}

// ---- helpers ----

func toSet(xs []string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x == "" {
			continue
		}
		m[x] = struct{}{}
	}
	return m
}

func splitYAMLDocuments(s string) []string {
	parts := []string{}
	cur := []string{}
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "---" {
			parts = append(parts, strings.Join(cur, "\n"))
			cur = []string{}
			continue
		}
		cur = append(cur, line)
	}
	parts = append(parts, strings.Join(cur, "\n"))
	return parts
}

func parseYAMLToUnstructured(b []byte) (*unstructured.Unstructured, error) {
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("empty")
	}
	u := &unstructured.Unstructured{Object: m}
	if u.GetKind() == "" || u.GetAPIVersion() == "" {
		return nil, fmt.Errorf("not a k8s object")
	}
	return u, nil
}

// ---------------- topology extraction ----------------

// extractNetworkInterfaces walks the object tree and extracts network interfaces
// with their associated IPs and CIDRs. It looks for common patterns like:
// - interfaces[].name with associated ipAddress, subnet, gateway fields
// - networks[].name with IP/CIDR fields
// - spec.config.* for NADs
func extractNetworkInterfaces(obj map[string]any) []NetworkInterface {
	interfaceMap := make(map[string]*NetworkInterface)
	seenCIDR := make(map[string]map[string]bool) // iface -> cidr -> bool
	seenIP := make(map[string]map[string]bool)   // iface -> ip -> bool

	// Walk the object tree looking for network interface patterns
	walkAny(obj, func(path []string, key string, parent map[string]any, val any) {
		// Look for interface/network name fields
		ifName := ""
		if key == "name" || key == "interfaceName" || key == "interface" {
			if s, ok := val.(string); ok && s != "" {
				// Check if this is in a network/interface context
				for _, p := range path {
					if strings.Contains(strings.ToLower(p), "interface") ||
						strings.Contains(strings.ToLower(p), "network") ||
						p == "n2" || p == "n3" || p == "n4" || p == "n6" { // common 5G interface names
						ifName = s
						break
					}
				}
				// Also check if parent has network-related keys
				if ifName == "" && parent != nil {
					for pk := range parent {
						pkLower := strings.ToLower(pk)
						if strings.Contains(pkLower, "ip") ||
							strings.Contains(pkLower, "subnet") ||
							strings.Contains(pkLower, "gateway") ||
							strings.Contains(pkLower, "cidr") {
							ifName = s
							break
						}
					}
				}
			}
		}

		// If we found an interface name, look for IPs/CIDRs in the parent context
		if ifName != "" && parent != nil {
			if interfaceMap[ifName] == nil {
				interfaceMap[ifName] = &NetworkInterface{Name: ifName}
				seenCIDR[ifName] = make(map[string]bool)
				seenIP[ifName] = make(map[string]bool)
			}

			// Extract IPs and CIDRs from parent
			for k, v := range parent {
				if s, ok := v.(string); ok {
					kLower := strings.ToLower(k)
					// Look for IP/CIDR related fields
					if strings.Contains(kLower, "ip") ||
						strings.Contains(kLower, "subnet") ||
						strings.Contains(kLower, "gateway") ||
						strings.Contains(kLower, "cidr") ||
						strings.Contains(kLower, "address") {
						// Extract CIDRs
						for _, cidr := range cidrRe.FindAllString(s, -1) {
							if !seenCIDR[ifName][cidr] {
								interfaceMap[ifName].CIDRs = append(interfaceMap[ifName].CIDRs, cidr)
								seenCIDR[ifName][cidr] = true
							}
						}
						// Extract IPs
						for _, ip := range ipv4Re.FindAllString(s, -1) {
							if !seenIP[ifName][ip] {
								interfaceMap[ifName].IPs = append(interfaceMap[ifName].IPs, ip)
								seenIP[ifName][ip] = true
							}
						}
					}
				}
			}
		}

		// Also check for common 5G interface patterns in path (n2, n3, n4, n6)
		for _, p := range path {
			if p == "n2" || p == "n3" || p == "n4" || p == "n6" ||
				strings.HasPrefix(p, "n2_") || strings.HasPrefix(p, "n3_") ||
				strings.HasPrefix(p, "n4_") || strings.HasPrefix(p, "n6_") {
				if interfaceMap[p] == nil {
					interfaceMap[p] = &NetworkInterface{Name: p}
					seenCIDR[p] = make(map[string]bool)
					seenIP[p] = make(map[string]bool)
				}

				// Extract IPs/CIDRs from string values
				if s, ok := val.(string); ok {
					for _, cidr := range cidrRe.FindAllString(s, -1) {
						if !seenCIDR[p][cidr] {
							interfaceMap[p].CIDRs = append(interfaceMap[p].CIDRs, cidr)
							seenCIDR[p][cidr] = true
						}
					}
					for _, ip := range ipv4Re.FindAllString(s, -1) {
						if !seenIP[p][ip] {
							interfaceMap[p].IPs = append(interfaceMap[p].IPs, ip)
							seenIP[p][ip] = true
						}
					}
				}
			}
		}
	})

	// Convert map to sorted slice
	result := make([]NetworkInterface, 0, len(interfaceMap))
	for _, iface := range interfaceMap {
		if len(iface.CIDRs) > 0 || len(iface.IPs) > 0 {
			sort.Strings(iface.CIDRs)
			sort.Strings(iface.IPs)
			result = append(result, *iface)
		}
	}

	// Sort by interface name for consistent output
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result
}

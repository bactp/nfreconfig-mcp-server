package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func init() { registerTool(ManifestPatchCucpIPsMany()) }

type IPInfo struct {
	Address string `json:"address,omitempty"` // CIDR e.g. 192.168.10.88/24
	Gateway string `json:"gateway,omitempty"` // e.g. 192.168.10.1
}

type PatchTarget struct {
	Repo      string `json:"repo"`
	Workdir   string `json:"workdir"`
	File      string `json:"file"`
	Kind      string `json:"kind,omitempty"` // optional, but helps
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type ManifestPatchCucpIPsManyParams struct {
	Targets []PatchTarget     `json:"targets"` // CUCP NFDeployment + CUCP NADs
	NewIPs  map[string]IPInfo `json:"newIps"`  // keys: n2,f1c,e1 (or whatever you use)
	DryRun  bool              `json:"dryRun,omitempty"`
}

type PatchResult struct {
	Repo    string `json:"repo"`
	File    string `json:"file"`
	Changed bool   `json:"changed"`
	Error   string `json:"error,omitempty"`
}

type ManifestPatchCucpIPsManyResult struct {
	Results []PatchResult `json:"results"`
}

func ManifestPatchCucpIPsMany() MCPTool[ManifestPatchCucpIPsManyParams, ManifestPatchCucpIPsManyResult] {
	return MCPTool[ManifestPatchCucpIPsManyParams, ManifestPatchCucpIPsManyResult]{
		Name:        "manifest_patch_cucp_ips",
		Description: "Update CUCP NFDeployment and NAD manifests with new IP allocations per interface. Use in Phase 3 to apply planned IPs to CUCP manifests. Patches address/gateway fields for each interface (n2, n3, n4, n6) including NAD spec.config JSON. Example: {\"targets\":[{\"repo\":\"cucp\",\"workdir\":\"/work/cucp\",\"file\":\"nfdeploy.yaml\",\"kind\":\"NFDeployment\"}], \"newIps\":{\"n2\":{\"address\":\"10.10.1.10/24\",\"gateway\":\"10.10.1.1\"}}}.",
		Handler: func(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[ManifestPatchCucpIPsManyParams]) (*mcp.CallToolResultFor[ManifestPatchCucpIPsManyResult], error) {
			if len(params.Arguments.Targets) == 0 {
				return toolErr[ManifestPatchCucpIPsManyResult](fmt.Errorf("missing required field: targets"))
			}
			if len(params.Arguments.NewIPs) == 0 {
				return toolErr[ManifestPatchCucpIPsManyResult](fmt.Errorf("missing required field: newIps"))
			}

			out := ManifestPatchCucpIPsManyResult{Results: make([]PatchResult, 0, len(params.Arguments.Targets))}

			for _, t := range params.Arguments.Targets {
				repo := strings.TrimSpace(t.Repo)
				workdir := cleanPath(t.Workdir)
				file := filepath.ToSlash(strings.TrimSpace(t.File))
				abs := absJoin(workdir, file)

				r := PatchResult{Repo: repo, File: file}

				u, _, err := readYAMLFile(abs)
				if err != nil {
					r.Error = fmt.Sprintf("read yaml: %v", err)
					out.Results = append(out.Results, r)
					continue
				}

				obj := u.Object
				changed := false
				kind := t.Kind
				if kind == "" {
					kind = u.GetKind()
				}

				// 1) Patch NFDeployment: update any keys named address/gateway under an interface context.
				if kind == "NFDeployment" {
					changed = patchByInterfaceContext(obj, params.Arguments.NewIPs) || changed
				}

				// 2) Patch NAD: spec.config is JSON string; update inside if contains address/gateway-like fields.
				if kind == "NetworkAttachmentDefinition" {
					ch, e := patchNADSpecConfig(obj, params.Arguments.NewIPs)
					if e != nil {
						r.Error = e.Error()
						out.Results = append(out.Results, r)
						continue
					}
					changed = ch || changed
				}

				if changed && !params.Arguments.DryRun {
					if err := writeYAMLFile(abs, obj); err != nil {
						r.Error = fmt.Sprintf("write yaml: %v", err)
						out.Results = append(out.Results, r)
						continue
					}
				}
				r.Changed = changed
				out.Results = append(out.Results, r)
			}

			return toolOK(out), nil
		},
	}
}

// Heuristic: whenever we find map containing "name": <iface> and keys address/gateway nearby.
func patchByInterfaceContext(obj map[string]any, newIPs map[string]IPInfo) bool {
	changed := false
	walkAny(obj, func(_ []string, key string, parent map[string]any, val any) {
		// Look for interface "name"
		if key != "name" {
			return
		}
		iface, ok := val.(string)
		if !ok {
			return
		}
		iface = strings.TrimSpace(iface)
		ip, ok := newIPs[iface]
		if !ok {
			return
		}

		// Try to patch siblings directly: address/gateway or ipv4.address/ipv4.gateway
		if a, ok := parent["address"].(string); ok && ip.Address != "" && a != ip.Address {
			parent["address"] = ip.Address
			changed = true
		}
		if g, ok := parent["gateway"].(string); ok && ip.Gateway != "" && g != ip.Gateway {
			parent["gateway"] = ip.Gateway
			changed = true
		}

		if ipv4, ok := parent["ipv4"].(map[string]any); ok {
			if a, ok := ipv4["address"].(string); ok && ip.Address != "" && a != ip.Address {
				ipv4["address"] = ip.Address
				changed = true
			}
			if g, ok := ipv4["gateway"].(string); ok && ip.Gateway != "" && g != ip.Gateway {
				ipv4["gateway"] = ip.Gateway
				changed = true
			}
			parent["ipv4"] = ipv4
		}
	})
	return changed
}

func patchNADSpecConfig(obj map[string]any, newIPs map[string]IPInfo) (bool, error) {
	spec, found, _ := unstructured.NestedMap(obj, "spec")
	if !found {
		return false, nil
	}
	cfg, ok := spec["config"].(string)
	if !ok || strings.TrimSpace(cfg) == "" {
		return false, nil
	}
	jm, ok := tryParseJSONConfigString(cfg)
	if !ok {
		// Not JSON, do simple string replace for CIDRs/GWs if present
		return patchStringFieldsInMap(spec, newIPs), nil
	}

	changed := false

	// Common patterns: ipam.addresses[].address, gateway, or ips[] etc.
	changed = patchByInterfaceContext(jm, newIPs) || changed

	// Also do string replacement for any CIDR/gateway occurrences in JSON tree
	changed = patchStringFieldsInMap(jm, newIPs) || changed

	if changed {
		b, err := json.Marshal(jm)
		if err != nil {
			return false, err
		}
		spec["config"] = string(b)
		obj["spec"] = spec
	}
	return changed, nil
}

// Brutal but reliable: replace any string equal to old values? Here we just overwrite keys named address/gateway if they look like IP and we can infer iface from nearby name.
func patchStringFieldsInMap(m map[string]any, newIPs map[string]IPInfo) bool {
	changed := false
	// As fallback: if string contains an old CIDR from any iface, replace with new
	// We don't know old here; so we patch only when key is "address"/"gateway" and value looks like IP.
	walkAny(m, func(_ []string, key string, parent map[string]any, val any) {
		s, ok := val.(string)
		if !ok {
			return
		}
		s = strings.TrimSpace(s)
		if key == "address" && cidrRe.MatchString(s) {
			// cannot know which iface; skip unless parent has "name"
			iface, _ := parent["name"].(string)
			iface = strings.TrimSpace(iface)
			if ip, ok := newIPs[iface]; ok && ip.Address != "" && s != ip.Address {
				parent[key] = ip.Address
				changed = true
			}
		}
		if key == "gateway" && ipv4Re.MatchString(s) {
			iface, _ := parent["name"].(string)
			iface = strings.TrimSpace(iface)
			if ip, ok := newIPs[iface]; ok && ip.Gateway != "" && s != ip.Gateway {
				parent[key] = ip.Gateway
				changed = true
			}
		}
	})
	return changed
}

func nowRFC3339Compact() string {
	return time.Now().UTC().Format("20060102T150405Z")
}

# NFReconfig MCP Server

MCP (Model Context Protocol) server implementation for autonomous 5G Network Functions reconfiguration operations in cloud-native Kubernetes environments.

## 📋 Table of Contents

- [Prerequisites](#-prerequisites)
- [Infrastructure Setup](#-infrastructure-setup)
- [Installation](#-installation)
- [Project Structure](#-project-structure)
- [MCP Tools Reference](#-mcp-tools-reference)
- [Agent Configuration](#-agent-configuration)
- [Related Projects](#-related-projects)
- [License](#-license)

---

## 🔧 Prerequisites

### Required Software

- [Go](https://golang.org/doc/install) (1.23 or later)
- [Docker](https://docs.docker.com/get-docker/)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Helm](https://helm.sh/docs/intro/install/) (v3.x)
- [kmcp](https://github.com/kagent-dev/kmcp) - Kagent MCP CLI tool

### Infrastructure Requirements

Before deploying the MCP server, you need a working multi-cluster Kubernetes environment with Nephio and 5G OAI stack.

---

## 🏗️ Infrastructure Setup

### Step 1: Bootstrap Kubernetes Clusters with Nephio

Follow the step-by-step guide to set up your multi-cluster Kubernetes infrastructure with Nephio:

📖 **Guide:** [Nephio Test Infrastructure Setup](https://github.com/vitu-mafeni/nephio-test-infra-aws/blob/master/docs/picture-step-by-step.md)

This will provision:
- Management cluster with Nephio control plane
- Workload clusters (Core, Edge, Regional, Standby)
- GitOps infrastructure (Gitea, ArgoCD)
- Porch package orchestrator

### Step 2: Deploy 5G OAI Stack

Deploy the OpenAirInterface 5G network functions across your workload clusters:

📖 **Guide:** [OAI 5G Deployment](https://github.com/vitu-mafeni/nephio-test-infra-aws/blob/master/docs/oai-5g-deployment.md)

**Cluster Topology:**

| Cluster | Network Functions | Description |
|---------|-------------------|-------------|
| **Core Cluster** | AMF, SMF, NRF, UDM, UDR, AUSF, MySQL | 5G Core control plane functions |
| **Edge Cluster** | UPF, CU-UP, DU | User plane and RAN lower layers |
| **Regional Cluster** | CU-CP | RAN control plane (gNB-CU-CP) |
| **Standby Cluster** | (Reserved) | Target for CU-CP relocation |

**Interface Bindings:**

```
┌─────────────────┐     N2      ┌─────────────────┐
│   Core Cluster  │◄───────────►│ Regional Cluster│
│  (AMF, SMF...)  │             │    (CU-CP)      │
└────────┬────────┘             └───────┬─────────┘
         │ N4                      E1   │   F1-C
         │                              │
         ▼                              ▼
┌─────────────────────────────────────────────────┐
│                  Edge Cluster                    │
│  ┌─────┐    N3    ┌───────┐   F1-U   ┌─────┐   │
│  │ UPF │◄────────►│ CU-UP │◄────────►│ DU  │   │
│  └─────┘          └───────┘          └─────┘   │
└─────────────────────────────────────────────────┘
```

- **N2**: AMF ↔ CU-CP (NGAP signaling)
- **N4**: SMF ↔ UPF (PFCP session management)
- **N3**: UPF ↔ CU-UP (GTP-U user plane)
- **E1**: CU-CP ↔ CU-UP (E1AP)
- **F1-C**: CU-CP ↔ DU (F1AP control plane)
- **F1-U**: CU-UP ↔ DU (F1-U user plane)

### Step 3: Install Kagent

Install Kagent for Kubernetes-native agent runtime:

```bash
# Add Kagent Helm repository
helm repo add kagent https://kagent-dev.github.io/kagent/
helm repo update

# Install Kagent in the management cluster
helm install kagent kagent/kagent \
  --namespace kagent-system \
  --create-namespace
```

Verify installation:
```bash
kubectl get pods -n kagent-system
```

---

## 📦 Installation

### Option 1: Local Development

1. **Clone the repository:**
   ```bash
   git clone https://github.com/bactp/nfreconfig-mcp-server.git
   cd nfreconfig-mcp-server
   ```

2. **Install dependencies:**
   ```bash
   go mod tidy
   ```

3. **Run the MCP server locally:**
   ```bash
   go run cmd/server/main.go
   ```

### Option 2: Docker Build

```bash
# Build using kmcp
kmcp build

# Or build manually
docker build -t nfreconfig-mcp-server:latest .
```

### Option 3: Kubernetes Deployment

1. **Deploy RBAC, Service, and Deployment:**
   ```bash
   kubectl apply -f k8s-deployment/mcp-server-rbac.yaml
   kubectl apply -f k8s-deployment/mcp-server-service.yaml
   kubectl apply -f k8s-deployment/mcp-server-deployment.yaml
   ```

2. **Or deploy as MCPServer custom resource using kmcp:**
   ```bash
   kmcp deploy mcp
   ```

3. **Verify deployment:**
   ```bash
   kubectl get pods -l app=nfreconfig-mcp-server
   kubectl get mcpserver
   ```

---

## 📁 Project Structure

```
nfreconfig-mcp-server/
├── cmd/
│   ├── server/              # MCP server entrypoint
│   │   └── main.go
│   └── devtest/             # Development testing utilities
│       └── main.go
├── internal/
│   ├── kube/                # Kubernetes client utilities
│   │   ├── client.go        # Kubeconfig loading
│   │   ├── dynamic.go       # Dynamic client builder
│   │   ├── kubeclients.go   # Client management
│   │   ├── mapper.go        # Resource mapping
│   │   └── workload_client.go  # Workload cluster client
│   └── tools/               # MCP tool implementations
│       ├── all_tools.go                    # Tool registration
│       ├── cluster_scan_topology.go        # Cluster discovery
│       ├── repos_get_url.go                # Repository URL discovery
│       ├── git_clone_or_open.go            # Git clone operations
│       ├── repos_scan_cudu_plan_inputs.go  # Manifest scanning
│       ├── manifest_patch_cucp_ips_many.go # CUCP IP patching
│       ├── manifest_patch_config_refs_many.go  # Config reference patching
│       ├── git_commit_push_many.go         # Git commit/push
│       ├── argocd_sync_app.go              # ArgoCD sync trigger
│       ├── workload_resources.go           # Workload resource ops
│       └── helpers.go                      # Utility functions
├── docs/
│   ├── agents/              # Agent system prompts
│   │   └── README.md        # Agent configuration guide
│   └── nf-reconfiguration-sequence.mmd  # Sequence diagram
├── k8s-deployment/          # Kubernetes manifests
│   ├── mcp-server-deployment.yaml
│   ├── mcp-server-rbac.yaml
│   └── mcp-server-service.yaml
├── Dockerfile
├── go.mod
├── go.sum
├── kmcp.yaml                # Kagent MCP configuration
└── mcp-server-config.json   # MCP server configuration
```

---

## 🔧 MCP Tools Reference

| Tool | Category | Description |
|------|----------|-------------|
| `cluster_scan_topology` | Cluster Inventory | Discover clusters with Git repos and network topology |
| `workload_list_resource` | Cluster Inventory | List K8s resources on workload clusters |
| `workload_get_resource` | Cluster Inventory | Get specific resource from workload cluster |
| `workload_delete_resource` | Cluster Inventory | Delete resource from workload cluster |
| `repos_get_repos_urls` | Repository | Get Git clone URLs for repositories |
| `git_clone_repos` | Repository | Clone Git repositories to local workdirs |
| `repo_scan_manifests` | Repository | Scan repos for K8s manifests with topology |
| `manifest_patch_cucp_ips` | Manifest Change | Patch CUCP NFDeployment/NAD with new IPs |
| `manifest_patch_config_refs` | Manifest Change | Update DU/CUUP configs with new CUCP refs |
| `git_commit_push` | Git Delivery | Stage, commit, and push repository changes |
| `argocd_sync_app` | Git Delivery | Trigger ArgoCD Application synchronization |

For detailed tool parameters and examples, see [docs/agents/README.md](docs/agents/README.md).

---

## 🤖 Agent Configuration

This MCP server is designed to work with a multi-agent architecture:

| Agent | Role | Tools Used |
|-------|------|------------|
| **NFs Reconfiguration Agent** | Coordination orchestrator | Delegates to skill agents |
| **Cluster Inventory Agent** | Topology discovery | `cluster_scan_topology`, `workload_*` |
| **Repository Agent** | Git repository management | `repos_get_repos_urls`, `git_clone_repos`, `repo_scan_manifests` |
| **Manifest Change Agent** | Configuration patching | `manifest_patch_cucp_ips`, `manifest_patch_config_refs` |
| **Git Delivery Agent** | GitOps synchronization | `git_commit_push`, `argocd_sync_app` |

For agent system prompts and configuration examples, see **[docs/agents/README.md](docs/agents/README.md)**.

---

## 🔗 Related Projects

| Project | Description |
|---------|-------------|
| [nephio-test-infra-aws](https://github.com/vitu-mafeni/nephio-test-infra-aws) | Infrastructure setup guides for Nephio and OAI 5G |
| [oai-cu-du-reconfiguration](https://github.com/vitu-mafeni/oai-cu-du-reconfiguration) | Previous work on CU-DU reconfiguration automation |
| [Kagent](https://github.com/kagent-dev/kagent) | Kubernetes-native agent runtime |
| [kmcp](https://github.com/kagent-dev/kmcp) | Kagent MCP CLI tool |
| [Nephio](https://nephio.org/) | Kubernetes-native network automation |
| [ArgoCD](https://argo-cd.readthedocs.io/) | Declarative GitOps for Kubernetes |
| [Model Context Protocol](https://modelcontextprotocol.io/) | Standardized LLM tool interface |

---

## 📜 License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

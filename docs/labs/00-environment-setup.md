# Lab 00 — Environment Setup

## Objective

Create a kind cluster with the Ignition helm chart, operator, and in-cluster git server. This environment persists across all subsequent phase labs.

## Step 1: Create Kind Cluster

Create a cluster with extra port mappings so we can access the Ignition web UI from the host:

```bash
cat <<'EOF' | kind create cluster --name ignition-sync-lab --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: 30088
        hostPort: 8088
        protocol: TCP
      - containerPort: 30043
        hostPort: 8043
        protocol: TCP
EOF
```

**Verify:**
```bash
kubectl cluster-info --context kind-ignition-sync-lab
kubectl get nodes
```

Expected: One node in `Ready` state.

## Step 2: Create Lab Namespace

```bash
kubectl create namespace lab
```

## Step 3: Deploy Ignition Gateway

Add the official helm repo and install a minimal Ignition gateway:

```bash
helm repo add inductiveautomation https://charts.ia.io
helm repo update
```

Install with test-friendly values:

```bash
helm upgrade --install ignition inductiveautomation/ignition \
  -n lab \
  --set image.tag=8.3.6 \
  --set commissioning.edition=standard \
  --set commissioning.acceptIgnitionEULA=true \
  --set gateway.replicas=1 \
  --set gateway.resourcesEnabled=true \
  --set gateway.resources.requests.cpu=500m \
  --set gateway.resources.requests.memory=1Gi \
  --set gateway.resources.limits.cpu=1 \
  --set gateway.resources.limits.memory=2Gi \
  --set gateway.dataVolumeStorageSize=5Gi \
  --set gateway.persistentVolumeClaimRetentionPolicy=Delete \
  --set service.type=NodePort \
  --set service.nodePorts.http=30088 \
  --set service.nodePorts.https=30043 \
  --set ingress.enabled=false \
  --set certManager.enabled=false
```

**Wait for gateway to start** (takes 60-120s on first pull):

```bash
kubectl rollout status statefulset/ignition -n lab --timeout=300s
```

**Verify gateway is running:**
```bash
kubectl get pods -n lab -l app.kubernetes.io/name=ignition -o wide
```

Expected: `ignition-0` pod in `Running` state with `1/1` containers ready.

**Verify web UI is reachable:**
```bash
curl -s -o /dev/null -w '%{http_code}' http://localhost:8088/StatusPing
```

Expected: `200`. If not accessible via NodePort, use port-forward as fallback:

```bash
kubectl port-forward -n lab svc/ignition 8088:8088 &
```

**Observation:** Open `http://localhost:8088` in a browser. You should see the Ignition Gateway landing page. Complete the initial commissioning wizard if prompted (set admin password, skip trial activation).

## Step 4: Annotate the Ignition Gateway Pod

The operator discovers gateways via annotations. Add them to the Ignition StatefulSet's pod template:

```bash
kubectl patch statefulset ignition -n lab --type=json -p='[
  {"op": "add", "path": "/spec/template/metadata/annotations/ignition-sync.io~1cr-name", "value": "lab-sync"},
  {"op": "add", "path": "/spec/template/metadata/annotations/ignition-sync.io~1gateway-name", "value": "lab-gateway"},
  {"op": "add", "path": "/spec/template/metadata/annotations/ignition-sync.io~1service-path", "value": ""}
]'
```

This triggers a rolling restart of the Ignition pod. Wait for it:

```bash
kubectl rollout status statefulset/ignition -n lab --timeout=300s
```

**Verify annotations are present:**
```bash
kubectl get pod ignition-0 -n lab -o jsonpath='{.metadata.annotations}' | jq .
```

Expected: Should contain `ignition-sync.io/cr-name: "lab-sync"` and `ignition-sync.io/gateway-name: "lab-gateway"`.

## Step 5: Create Gateway API Key Secret

The operator requires a reference to a Secret containing an Ignition API key. For initial testing (before the agent needs to actually call the Ignition API), create a placeholder:

```bash
kubectl create secret generic ignition-api-key -n lab \
  --from-literal=apiKey=placeholder-key-for-testing
kubectl label secret ignition-api-key -n lab app=lab-test
```

> **Note:** In phase 05 (sync agent), this needs to be a real API key created through the Ignition web UI. We'll replace it then.

## Step 6: Build and Load Operator Image

```bash
cd /path/to/ignition-sync-operator
make docker-build IMG=ignition-sync-operator:lab
kind load docker-image ignition-sync-operator:lab --name ignition-sync-lab
```

**Verify image is loaded:**
```bash
docker exec ignition-sync-lab-control-plane crictl images | grep ignition-sync
```

## Step 7: Install CRDs and Deploy Operator

```bash
make install
make deploy IMG=ignition-sync-operator:lab
```

**Wait for controller:**
```bash
kubectl rollout status deployment/ignition-sync-operator-controller-manager \
  -n ignition-sync-operator-system --timeout=120s
```

**Verify:**
```bash
kubectl get pods -n ignition-sync-operator-system
```

Expected: `controller-manager` pod Running with `1/1` Ready.

**Check operator logs for clean startup:**
```bash
kubectl logs -n ignition-sync-operator-system -l control-plane=controller-manager --tail=20
```

Expected: Should see "starting webhook receiver" and no ERROR lines.

## Step 8: Deploy In-Cluster Git Server

The git server provides a deterministic test repository with realistic Ignition project structure:

```bash
kubectl apply -n lab -f test/functional/fixtures/git-server.yaml
```

**Wait for git server:**
```bash
kubectl wait --for=condition=Ready pod/test-git-server -n lab --timeout=120s
```

**Verify git server serves the repo:**
```bash
kubectl run git-test --rm -i --restart=Never -n lab \
  --image=alpine/git:latest -- \
  git ls-remote git://test-git-server.lab.svc.cluster.local/test-repo.git
```

Expected: Should list refs including `refs/tags/v1.0.0`, `refs/tags/v2.0.0`, and `refs/heads/main`.

## Step 9: Verify Complete Environment

Run this checklist:

```bash
echo "=== Environment Checklist ==="

echo -n "Kind cluster: "
kind get clusters | grep -q ignition-sync-lab && echo "OK" || echo "MISSING"

echo -n "Lab namespace: "
kubectl get ns lab >/dev/null 2>&1 && echo "OK" || echo "MISSING"

echo -n "Ignition gateway: "
kubectl get pod ignition-0 -n lab -o jsonpath='{.status.phase}' 2>/dev/null

echo -n "  Annotations: "
kubectl get pod ignition-0 -n lab -o jsonpath='{.metadata.annotations.ignition-sync\.io/cr-name}' 2>/dev/null
echo ""

echo -n "Operator: "
kubectl get pods -n ignition-sync-operator-system -l control-plane=controller-manager \
  -o jsonpath='{.items[0].status.phase}' 2>/dev/null
echo ""

echo -n "Git server: "
kubectl get pod test-git-server -n lab -o jsonpath='{.status.phase}' 2>/dev/null
echo ""

echo -n "API key secret: "
kubectl get secret ignition-api-key -n lab >/dev/null 2>&1 && echo "OK" || echo "MISSING"

echo -n "CRD: "
kubectl get crd ignitionsyncs.sync.ignition.io >/dev/null 2>&1 && echo "OK" || echo "MISSING"
```

All items should show `OK` or `Running`.

## Step 10: Record Baseline State

Save baseline state for comparison during labs:

```bash
kubectl get all -n lab -o wide > /tmp/lab-baseline.txt
kubectl get events -n lab --sort-by=.lastTimestamp > /tmp/lab-events-baseline.txt
echo "Baseline saved to /tmp/lab-baseline.txt"
```

## Environment Teardown (When Done With All Labs)

```bash
helm uninstall ignition -n lab
kubectl delete namespace lab
make undeploy ignore-not-found=true
make uninstall ignore-not-found=true
kind delete cluster --name ignition-sync-lab
```

## Troubleshooting

**Ignition pod stuck in Pending:** Check PVC binding — `kubectl get pvc -n lab`. Kind's default StorageClass is `standard` which should auto-provision. If not, check `kubectl get storageclass`.

**Ignition OOMKilled:** Increase memory limit in helm values. 2Gi should be sufficient for a single gateway with no projects.

**Image pull errors:** Ensure Docker Desktop has internet access. The Ignition image is ~800MB on first pull.

**Operator CrashLoopBackOff:** Check logs with `kubectl logs -n ignition-sync-operator-system -l control-plane=controller-manager --previous`. Common cause: CRD not installed before deploying.

#!/usr/bin/env bash
# End-to-end test for the operator: builds the rpkv and rpkv-operator
# images, spins up a throwaway kind cluster, deploys a dev-container
# Redpanda broker plus the operator against it, applies an RpkvIndex CR,
# and verifies the shard StatefulSet grows its replica count when the
# indexed topic's partition count grows. The cluster is always deleted on
# exit - it exists only for the duration of this script - and the result
# is recorded as a receipt under .claude/receipts/<HEAD sha>/kind-operator-e2e.json.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"
cd "$REPO_ROOT"

KIND="go run sigs.k8s.io/kind@v0.32.0"
CLUSTER=rpkv-operator-e2e
RP_IMAGE="docker.redpanda.com/redpandadata/redpanda:v26.1.15"

cleanup() {
    $KIND delete cluster --name "$CLUSTER" || true
}
trap cleanup EXIT

$KIND create cluster --name "$CLUSTER" --wait 120s

docker build -t rpkv:dev .
docker build -f operator/Dockerfile -t rpkv-operator:dev operator/
$KIND load docker-image rpkv:dev --name "$CLUSTER"
$KIND load docker-image rpkv-operator:dev --name "$CLUSTER"

kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: redpanda
  labels:
    app: redpanda
spec:
  containers:
    - name: redpanda
      image: ${RP_IMAGE}
      args:
        - redpanda
        - start
        - --mode
        - dev-container
        - --smp
        - "1"
        - --kafka-addr
        - PLAINTEXT://0.0.0.0:9092
        - --advertise-kafka-addr
        - PLAINTEXT://redpanda:9092
---
apiVersion: v1
kind: Service
metadata:
  name: redpanda
spec:
  type: ClusterIP
  selector:
    app: redpanda
  ports:
    - port: 9092
      targetPort: 9092
EOF

kubectl wait --for=condition=Ready pod/redpanda --timeout=180s

for i in $(seq 1 30); do
    if kubectl exec redpanda -- rpk topic create kv -p 4; then
        break
    fi
    if [[ "$i" -eq 30 ]]; then
        echo "kind-operator-e2e: rpk topic create kv never succeeded" >&2
        exit 1
    fi
    sleep 2
done

kubectl apply -f operator/config/crd/bases/rpkv.sonirico.dev_rpkvindices.yaml
kubectl apply -f operator/config/rbac/role.yaml
kubectl apply -f operator/config/manager/manager.yaml

kubectl rollout status deployment/rpkv-operator --timeout=300s

kubectl apply -f - <<EOF
apiVersion: rpkv.sonirico.dev/v1alpha1
kind: RpkvIndex
metadata:
  name: kv
spec:
  topic: kv
  brokers:
    - redpanda:9092
  image: rpkv:dev
EOF

initial_replicas=""
initial_ok=false
for i in $(seq 1 60); do
    initial_replicas="$(kubectl get statefulset kv-shard -o jsonpath='{.spec.replicas}' 2>/dev/null || true)"
    if [[ "$initial_replicas" == "4" ]]; then
        initial_ok=true
        break
    fi
    sleep 2
done

kubectl exec redpanda -- rpk topic add-partitions kv --num 2

grown_replicas=""
grown_ok=false
for i in $(seq 1 60); do
    grown_replicas="$(kubectl get statefulset kv-shard -o jsonpath='{.spec.replicas}' 2>/dev/null || true)"
    if [[ "$grown_replicas" == "6" ]]; then
        grown_ok=true
        break
    fi
    sleep 2
done

receipt_dir=".claude/receipts/$(git rev-parse HEAD)"
mkdir -p "$receipt_dir"

if $initial_ok && $grown_ok; then
    ok=true
    exit_code=0
else
    ok=false
    exit_code=1
fi

initial_replicas="${initial_replicas:-0}"
grown_replicas="${grown_replicas:-0}"

cat >"${receipt_dir}/kind-operator-e2e.json" <<JSON
{
  "cluster": "${CLUSTER}",
  "initial_replicas": ${initial_replicas},
  "grown_replicas": ${grown_replicas},
  "ok": ${ok},
  "exit_code": ${exit_code}
}
JSON

exit "$exit_code"

#!/usr/bin/env bash
# End-to-end smoke test for the helm chart: builds the rpkv image, spins up
# a throwaway kind cluster, deploys a dev-container Redpanda broker plus the
# chart against it, produces one record over rpk, and reads it back through
# the router's public HTTP API. The cluster is always deleted on exit - it
# exists only for the duration of this script - and the result is recorded
# as a receipt under .claude/receipts/<HEAD sha>/kind-smoke.json.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"
cd "$REPO_ROOT"

KIND="go run sigs.k8s.io/kind@v0.32.0"
CLUSTER=rpkv-smoke
RP_IMAGE="docker.redpanda.com/redpandadata/redpanda:v26.1.15"

cleanup() {
    $KIND delete cluster --name "$CLUSTER" || true
}
trap cleanup EXIT

$KIND create cluster --name "$CLUSTER" --wait 120s

docker build -t rpkv:dev .
$KIND load docker-image rpkv:dev --name "$CLUSTER"

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
        echo "kind-smoke: rpk topic create kv never succeeded" >&2
        exit 1
    fi
    sleep 2
done

docker run --rm -v "${REPO_ROOT}/deploy/helm/rpkv:/apps" alpine/helm:latest template rpkv /apps --set brokers=redpanda:9092 --set topics=kv | kubectl apply -f -

kubectl rollout status statefulset/rpkv-shard --timeout=300s
kubectl rollout status deployment/rpkv-router --timeout=300s

kubectl exec redpanda -- sh -c "printf 'v1\n' | rpk topic produce kv -k k1"

expected="v1"
got=""
ok=false
for i in $(seq 1 30); do
    if got="$(kubectl run "smoke-curl-${i}" --rm -i --restart=Never --image=curlimages/curl -- -fsS "http://rpkv-router:8080/v1/kv/kv/k1" 2>/dev/null)"; then
        if [[ "$got" == *"$expected"* ]]; then
            ok=true
            break
        fi
    fi
    sleep 2
done

receipt_dir=".claude/receipts/$(git rev-parse HEAD)"
mkdir -p "$receipt_dir"

if $ok; then
    exit_code=0
else
    exit_code=1
fi

cat >"${receipt_dir}/kind-smoke.json" <<JSON
{
  "cluster": "${CLUSTER}",
  "expected": "${expected}",
  "got": $(jq -Rs . <<<"$got"),
  "ok": ${ok},
  "exit_code": ${exit_code}
}
JSON

exit "$exit_code"

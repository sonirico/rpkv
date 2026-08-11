#!/usr/bin/env bash
# Validates the README quickstart against a fresh clone: setup, check,
# bring up the dev-loop broker, produce a record, read it back over the
# public HTTP API, and record every step's exit code as a receipt. This is
# not a hook-wired gate - it is run by hand before a release, against the
# finished branch, from outside any existing checkout.
set -euo pipefail

REF="${1:-main}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"

for tool in docker jq just go curl; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        echo "release-check: required tool '${tool}' is not on PATH" >&2
        exit 1
    fi
done

if [[ -n "$(docker ps -aq --filter name=^rpkv-redpanda$)" ]]; then
    echo "release-check: container rpkv-redpanda already exists - a fresh validation needs it absent (run 'just redpanda-down' to remove)" >&2
    exit 1
fi

CLONE_DIR=""
RPKV_PID=""

teardown() {
    if [[ -n "$RPKV_PID" ]]; then
        kill -- "-${RPKV_PID}" 2>/dev/null || true
    fi
    if [[ -n "$CLONE_DIR" && -d "$CLONE_DIR" ]]; then
        (cd "$CLONE_DIR" && just redpanda-down) 2>/dev/null || true
    fi
    if [[ -n "$CLONE_DIR" ]]; then
        rm -rf "$CLONE_DIR"
    fi
}
trap teardown EXIT

CLONE_DIR="$(mktemp -d)"
git clone git@github.com:sonirico/rpkv.git "$CLONE_DIR"
(cd "$CLONE_DIR" && git checkout "$REF")
SHA="$(cd "$CLONE_DIR" && git rev-parse HEAD)"

setup_exit=-1
check_exit=-1
redpanda_up_exit=-1
topic_create_exit=-1
healthz_exit=-1
produce_exit=-1
curl_exit=-1
http_code=""

hdrs_file="$(mktemp)"
body_file="$(mktemp)"

run_steps() {
    set +e

    (cd "$CLONE_DIR" && just setup)
    setup_exit=$?
    [[ $setup_exit -eq 0 ]] || return

    (cd "$CLONE_DIR" && just check)
    check_exit=$?
    [[ $check_exit -eq 0 ]] || return

    (cd "$CLONE_DIR" && just redpanda-up)
    redpanda_up_exit=$?
    [[ $redpanda_up_exit -eq 0 ]] || return

    docker exec rpkv-redpanda rpk topic create orders
    topic_create_exit=$?
    [[ $topic_create_exit -eq 0 ]] || return

    (cd "$CLONE_DIR" && exec setsid go run ./cmd/rpkv --brokers localhost:19092 --topics orders --data-dir ./rpkv-data --listen :8080) &
    RPKV_PID=$!

    healthz_exit=1
    for _ in $(seq 1 60); do
        if curl -fsS http://localhost:8080/healthz >/dev/null 2>&1; then
            healthz_exit=0
            break
        fi
        sleep 1
    done
    [[ $healthz_exit -eq 0 ]] || return

    printf 'hello-value\n' | docker exec -i rpkv-redpanda rpk topic produce orders --key user-42
    produce_exit=$?
    [[ $produce_exit -eq 0 ]] || return

    for _ in $(seq 1 10); do
        http_code="$(curl -sS -D "$hdrs_file" -o "$body_file" -w '%{http_code}' http://localhost:8080/v1/kv/orders/user-42)"
        curl_exit=$?
        [[ "$http_code" == "404" ]] || break
        sleep 1
    done

    set -e
}
run_steps
set -e

http_200=false
if [[ "$http_code" == "200" ]]; then
    http_200=true
fi

body_match=false
if [[ -f "$body_file" ]] && [[ "$(cat "$body_file")" == "hello-value" ]]; then
    body_match=true
fi

headers_present=false
if [[ -f "$hdrs_file" ]] \
    && grep -qi '^X-Rpkv-Partition:[[:space:]]*0[[:space:]]*$' "$hdrs_file" \
    && grep -qi '^X-Rpkv-Offset:[[:space:]]*0[[:space:]]*$' "$hdrs_file" \
    && grep -qi '^X-Rpkv-Checkpoint:[[:space:]]*0[[:space:]]*$' "$hdrs_file"; then
    headers_present=true
fi

receipt_dir="$REPO_ROOT/.claude/receipts/$SHA"
mkdir -p "$receipt_dir"
receipt_path="${receipt_dir}/release-check.json"

pass=false
if [[ $setup_exit -eq 0 && $check_exit -eq 0 && $redpanda_up_exit -eq 0 \
    && $topic_create_exit -eq 0 && $healthz_exit -eq 0 && $produce_exit -eq 0 \
    && $curl_exit -eq 0 && "$http_200" == "true" && "$body_match" == "true" \
    && "$headers_present" == "true" ]]; then
    pass=true
fi

jq -n \
    --arg ref "$REF" \
    --arg sha "$SHA" \
    --argjson setup_exit "$setup_exit" \
    --argjson check_exit "$check_exit" \
    --argjson redpanda_up_exit "$redpanda_up_exit" \
    --argjson topic_create_exit "$topic_create_exit" \
    --argjson healthz_exit "$healthz_exit" \
    --argjson produce_exit "$produce_exit" \
    --argjson curl_exit "$curl_exit" \
    --arg http_code "$http_code" \
    --argjson http_200 "$http_200" \
    --argjson body_match "$body_match" \
    --argjson headers_present "$headers_present" \
    --argjson pass "$pass" \
    '{
        ref: $ref,
        sha: $sha,
        setup_exit: $setup_exit,
        check_exit: $check_exit,
        redpanda_up_exit: $redpanda_up_exit,
        topic_create_exit: $topic_create_exit,
        healthz_exit: $healthz_exit,
        produce_exit: $produce_exit,
        curl_exit: $curl_exit,
        http_code: $http_code,
        http_200: $http_200,
        body_match: $body_match,
        headers_present: $headers_present,
        pass: $pass
    }' >"$receipt_path"

rm -f "$hdrs_file" "$body_file"

echo "release-check: receipt at ${receipt_path}"
echo "pass=${pass}"

if [[ "$pass" == "true" ]]; then
    exit 0
fi
exit 1

#!/usr/bin/env bash
# vm-pre-delete: runs before the instance is stopped/removed. Its disk still
# exists here. stdin = instance-context JSON; stdout is ignored.
# Must be idempotent: it may run for an already-gone instance (garm retries
# failed creates), and a delete-hook failure never blocks deletion.
set -euo pipefail

ctx="$(cat)"
{
    echo "[vm-pre-delete] hook=${GARM_HOOK} phase=${GARM_HOOK_PHASE} ignore_failure=${GARM_HOOK_IGNORE_FAILURE}"
    echo "[vm-pre-delete] instance=${GARM_INSTANCE_NAME} pool=${GARM_POOL_ID}"
    printf '%s\n' "${ctx}" | jq .
} >&2

# Do host-side work keyed on ${GARM_INSTANCE_NAME} / ${GARM_POOL_ID} here,
# e.g. snapshot the instance disk before it is removed.

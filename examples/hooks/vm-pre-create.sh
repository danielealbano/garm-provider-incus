#!/usr/bin/env bash
# vm-pre-create: mutate the incus InstancesPost before the instance is created.
#   stdin  = api.InstancesPost JSON
#   stdout = modified api.InstancesPost JSON (must stay a complete, valid object)
# Do NOT change the instance name (garm tracks the instance by it).
set -euo pipefail

input="$(cat)"

# Show the input on stderr (surfaced in the provider/garm logs).
{
    echo "[vm-pre-create] hook=${GARM_HOOK} phase=${GARM_HOOK_PHASE}"
    echo "[vm-pre-create] instance=${GARM_INSTANCE_NAME} pool=${GARM_POOL_ID} arch=${GARM_OS_ARCH}"
    printf '%s\n' "${input}" | jq .
} >&2

# Emit the modified InstancesPost: attach a data disk device.
printf '%s' "${input}" | jq \
    '.devices = ((.devices // {}) + {
        "data": {"type": "disk", "pool": "default", "source": "vol-'"${GARM_INSTANCE_NAME}"'", "path": "/data"}
    })'

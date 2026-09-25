#!/usr/bin/env bash
# Round-trips secrets against a real Doppler instance to confirm the wire formats
# this tool assumes. Everything else is verified against server source and mocks;
# this is the only check that exercises the live API.
#
#   DOPPLER_TOKEN=<token> bash scripts/verify-against-local.sh
#
# Defaults to the local dev instance; override with DOPPLER_API. Use a service
# account token: a user-backed token cannot read restricted secrets, which is one
# of the things this verifies.
#
# Creates a throwaway project and deletes it on exit.
set -euo pipefail

API="${DOPPLER_API:-https://api.dopplerlocal.com:3030}"
PROJECT="migrator-verify-$$"

if [ -z "${DOPPLER_TOKEN:-}" ]; then
    echo "Set DOPPLER_TOKEN to a token for $API" >&2
    exit 1
fi

# -k: the local dev instance serves a self-signed certificate.
api() {
    local method="$1" path="$2"
    shift 2
    curl -sk --fail-with-body -X "$method" "$API$path" \
        -H "Authorization: Bearer $DOPPLER_TOKEN" \
        -H "Content-Type: application/json" "$@"
}

cleanup() {
    api POST "/v3/projects/project/delete" -d "{\"project\":\"$PROJECT\"}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

pass() { echo "  ok   $*"; }

echo "==> Verifying wire formats against $API"

api POST "/v3/projects" \
    -d "{\"name\":\"$PROJECT\",\"description\":\"migrator verification\",\"create_default_environments\":false}" >/dev/null
pass "create_default_environments accepted on project create"

api POST "/v3/environments?project=$PROJECT" \
    -d "{\"project\":\"$PROJECT\",\"name\":\"Production\",\"slug\":\"prd\",\"personal_configs\":false}" >/dev/null
pass "personal_configs accepted on environment create"

# The shape this script exists to prove: valueType and visibility set at creation,
# in one call, via change_requests.
api POST "/v3/configs/config/secrets?project=$PROJECT&config=prd" -d '{
  "project": "'"$PROJECT"'", "config": "prd",
  "change_requests": [
    {"name": "PLAIN", "originalName": null, "value": "hello"},
    {"name": "TYPED_JSON", "originalName": null, "value": "{\"a\":1}", "valueType": {"type": "json"}},
    {"name": "SECRETIVE", "originalName": null, "value": "shh", "visibility": "restricted"}
  ]
}' >/dev/null
pass "change_requests accepted with valueType and visibility"

read_back=$(api GET "/v3/configs/config/secrets?project=$PROJECT&config=prd&include_inherited_secrets=false&include_dynamic_secrets=false")

echo "$read_back" | python3 -c '
import json, sys

secrets = json.load(sys.stdin)["secrets"]

vt = secrets["TYPED_JSON"].get("rawValueType")
assert isinstance(vt, dict), f"rawValueType is {type(vt).__name__}, expected an object: {vt!r}"
assert vt.get("type") == "json", f"valueType did not round-trip: {vt!r}"
print("  ok   rawValueType round-tripped as an object, not a string")

vis = secrets["SECRETIVE"].get("rawVisibility")
assert vis == "restricted", f"visibility did not round-trip: {vis!r}"
print("  ok   rawVisibility round-tripped")

print(f"  ok   an untyped secret reports rawValueType={secrets[\"PLAIN\"].get(\"rawValueType\")!r}")
'

echo "==> Wire formats confirmed"

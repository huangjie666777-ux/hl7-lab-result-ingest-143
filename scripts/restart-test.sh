#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p data tmp
go build -o bin/server ./cmd/server
go build -o bin/mllp-send ./cmd/mllp-send

./bin/server -mllp-addr :12576 -http-addr :18081 -db data/lab.db &
SRV=$!; sleep 1
./bin/mllp-send -addr localhost:12576 examples/01-preliminary.hl7 | tail -1
kill $SRV; wait $SRV 2>/dev/null || true

./bin/server -mllp-addr :12576 -http-addr :18081 -db data/lab.db &
SRV=$!; sleep 1
echo "--- after restart, resend same message (dup no-op AA, no new version):"
./bin/mllp-send -addr localhost:12576 examples/01-preliminary.hl7 | tail -1
echo "--- query after restart:"
curl -s "http://localhost:18081/api/sources/LIS%5EHOSP-A/orders/ORD-9001/results" | python3 -m json.tool | grep -E '"(code|value|version)"'
kill $SRV

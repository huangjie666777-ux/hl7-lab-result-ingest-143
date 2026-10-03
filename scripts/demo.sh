#!/usr/bin/env bash
# End-to-end demo: start server, send messages via MLLP, query via HTTP.
set -euo pipefail
cd "$(dirname "$0")/.."

rm -rf data tmp && mkdir -p data tmp
go build -o bin/server ./cmd/server
go build -o bin/mllp-send ./cmd/mllp-send

./bin/server -mllp-addr :12575 -http-addr :18080 -db data/lab.db &
SRV=$!
trap 'kill $SRV 2>/dev/null || true' EXIT
sleep 1

send() { echo "== $1"; ./bin/mllp-send -addr localhost:12575 "$2"; echo; }

send "1. preliminary report (P)" examples/01-preliminary.hl7
send "2. confirm (F)" examples/02-confirm.hl7
send "3. correction (C) on confirmed WBC" examples/03-correct.hl7
send "4. duplicate redelivery of MSG0003 (no-op AA)" examples/03-correct.hl7
send "5. same control ID, different content (AE)" <(sed 's/7.1/7.2/' examples/03-correct.hl7)
send "6. cross-patient order reuse (AE)" examples/04-illegal-cross-patient.hl7
send "7. correction without confirmed result (AE)" examples/05-illegal-correct-without-final.hl7
send "8. unknown escape (AE)" examples/06-illegal-unknown-escape.hl7
send "9. duplicate item in message (AE)" examples/07-illegal-dup-item.hl7
send "10. partial supplement: new item HGB on same order" examples/08-partial-order.hl7

SRC="LIS%5EHOSP-A"
echo "== current results"
curl -s "http://localhost:18080/api/sources/$SRC/orders/ORD-9001/results" | python3 -m json.tool
echo "== history"
curl -s "http://localhost:18080/api/sources/$SRC/orders/ORD-9001/history" | python3 -m json.tool

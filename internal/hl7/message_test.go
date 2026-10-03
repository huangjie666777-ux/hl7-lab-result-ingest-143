package hl7

import "testing"

const validMsg = "MSH|^~\\&|LIS|HOSP-A|ARCHIVE|LAB|20261003090000||ORU^R01|MSG1|P|2.5.1\r" +
	"PID|1||P12345^^^HIS^MR||Zhang^San\r" +
	"OBR|1||ORD9001|CBC^Blood Count^LN\r" +
	"OBX|1|NM|WBC^White Blood Cell^LN|1|6.8|10*9/L|||||P\r" +
	"OBX|2|ST|RPT^Note^L|1|a\\F\\b\\S\\c\\R\\d\\T\\e\\E\\f||||||F\r"

func TestParseValid(t *testing.T) {
	m, err := ParseMessage(validMsg)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Source() != "LIS^HOSP-A" || m.ControlID != "MSG1" || m.PatientID != "P12345" || m.OrderNo != "ORD9001" {
		t.Fatalf("unexpected header: %+v", m)
	}
	if len(m.Items) != 2 {
		t.Fatalf("want 2 items, got %d", len(m.Items))
	}
	if got := m.Items[1].Value; got != "a|b^c~d&e\\f" {
		t.Fatalf("escape decode wrong: %q", got)
	}
	if m.Items[0].Units != "10*9/L" {
		t.Fatalf("units lost: %q", m.Items[0].Units)
	}
}

func TestRejectBadMessages(t *testing.T) {
	cases := map[string]string{
		"bad escape":      "MSH|^~\\&|LIS|HOSP-A|ARCHIVE|LAB|2026||ORU^R01|M2|P|2.5.1\rPID|1||P1\rOBR|1||O1\rOBX|1|ST|A^B^L|1|bad\\X\\escape||||||P\r",
		"bad NM":          "MSH|^~\\&|LIS|HOSP-A|ARCHIVE|LAB|2026||ORU^R01|M3|P|2.5.1\rPID|1||P1\rOBR|1||O1\rOBX|1|NM|A^B^L|1|12x|mg|||||P\r",
		"bad status":      "MSH|^~\\&|LIS|HOSP-A|ARCHIVE|LAB|2026||ORU^R01|M4|P|2.5.1\rPID|1||P1\rOBR|1||O1\rOBX|1|NM|A^B^L|1|1|mg|||||R\r",
		"dup item":        "MSH|^~\\&|LIS|HOSP-A|ARCHIVE|LAB|2026||ORU^R01|M5|P|2.5.1\rPID|1||P1\rOBR|1||O1\rOBX|1|NM|A^B^L|1|1|mg|||||P\rOBX|2|NM|A^B^L|1|2|mg|||||P\r",
		"missing OBR":     "MSH|^~\\&|LIS|HOSP-A|ARCHIVE|LAB|2026||ORU^R01|M6|P|2.5.1\rPID|1||P1\rOBX|1|NM|A^B^L|1|1|mg|||||P\r",
		"no OBX":          "MSH|^~\\&|LIS|HOSP-A|ARCHIVE|LAB|2026||ORU^R01|M7|P|2.5.1\rPID|1||P1\rOBR|1||O1\r",
		"wrong version":   "MSH|^~\\&|LIS|HOSP-A|ARCHIVE|LAB|2026||ORU^R01|M8|P|2.3\rPID|1||P1\rOBR|1||O1\rOBX|1|NM|A^B^L|1|1|mg|||||P\r",
		"unsupported seg": "MSH|^~\\&|LIS|HOSP-A|ARCHIVE|LAB|2026||ORU^R01|M9|P|2.5.1\rPID|1||P1\rOBR|1||O1\rOBX|1|NM|A^B^L|1|1|mg|||||P\rNTE|1|hi\r",
		"bad value type":  "MSH|^~\\&|LIS|HOSP-A|ARCHIVE|LAB|2026||ORU^R01|MA|P|2.5.1\rPID|1||P1\rOBR|1||O1\rOBX|1|CE|A^B^L|1|x|mg|||||P\r",
	}
	for name, raw := range cases {
		if _, err := ParseMessage(raw); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

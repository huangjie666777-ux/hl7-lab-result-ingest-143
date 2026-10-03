package hl7

import "testing"

const baseMSH = "MSH|^~\\&|LIS|HOSP-A|||20261003090000||ORU^R01|MSG0001|P|2.5.1"

func okMsg() string {
	return baseMSH + "\r" +
		"PID|1||P12345^^^HIS^MR||Zhang\r" +
		"OBR|1||ORD-9001\r" +
		"OBX|1|NM|GLU^Glucose^LN||5.6|mmol/L|||||P\r" +
		"OBX|2|ST|RBC-CMT^Comment^L||See\\T\\note||||||F"
}

func TestParseValid(t *testing.T) {
	m, err := Parse(okMsg())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Source() != "LIS^HOSP-A" || m.ControlID != "MSG0001" {
		t.Fatalf("bad source/control: %+v", m)
	}
	if m.PatientID != "P12345" || m.OrderNum != "ORD-9001" {
		t.Fatalf("bad ids: %+v", m)
	}
	if len(m.Items) != 2 {
		t.Fatalf("want 2 items, got %d", len(m.Items))
	}
	if m.Items[0].Value != "5.6" || m.Items[0].Unit != "mmol/L" || m.Items[0].Status != "P" {
		t.Fatalf("bad item0: %+v", m.Items[0])
	}
	if m.Items[1].Value != "See&note" {
		t.Fatalf("escape decode failed: %q", m.Items[1].Value)
	}
}

func TestRejections(t *testing.T) {
	cases := map[string]string{
		"bad encoding":   "MSH|^~\\&%|LIS|HOSP-A|||2026||ORU^R01|M1|P|2.5.1\rPID|1||P1\rOBR|1||O1\rOBX|1|NM|A^a^L||1||||||P",
		"wrong type":     "MSH|^~\\&|LIS|HOSP-A|||2026||ADT^A01|M1|P|2.5.1\rPID|1||P1\rOBR|1||O1\rOBX|1|NM|A^a^L||1||||||P",
		"missing pid":    baseMSH + "\rOBR|1||O1\rOBX|1|NM|A^a^L||1||||||P",
		"two pid":        okMsg() + "\rPID|2||P2",
		"no obx":         baseMSH + "\rPID|1||P1\rOBR|1||O1",
		"bad nm":         baseMSH + "\rPID|1||P1\rOBR|1||O1\rOBX|1|NM|A^a^L||abc||||||P",
		"bad type":       baseMSH + "\rPID|1||P1\rOBR|1||O1\rOBX|1|CE|A^a^L||x||||||P",
		"bad status":     baseMSH + "\rPID|1||P1\rOBR|1||O1\rOBX|1|NM|A^a^L||1||||||R",
		"unknown escape": baseMSH + "\rPID|1||P1\rOBR|1||O1\rOBX|1|ST|A^a^L||x\\H\\y||||||P",
		"dup item":       baseMSH + "\rPID|1||P1\rOBR|1||O1\rOBX|1|NM|A^a^L||1||||||P\rOBX|2|NM|A^b^L||2||||||P",
		"missing value":  baseMSH + "\rPID|1||P1\rOBR|1||O1\rOBX|1|NM|A^a^L||||||||P",
	}
	for name, raw := range cases {
		if _, err := Parse(raw); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

func TestTooManyOBX(t *testing.T) {
	raw := baseMSH + "\rPID|1||P1\rOBR|1||O1"
	for i := 1; i <= 21; i++ {
		raw += "\rOBX|" + itoa(i) + "|NM|A" + itoa(i) + "^x^L||1||||||P"
	}
	if _, err := Parse(raw); err == nil {
		t.Fatal("expected rejection for 21 OBX")
	}
}

func TestDecodeEscapes(t *testing.T) {
	got, err := DecodeEscapes("a\\F\\b\\S\\c\\R\\d\\T\\e\\E\\f")
	if err != nil {
		t.Fatal(err)
	}
	if got != "a|b^c~d&e\\f" {
		t.Fatalf("got %q", got)
	}
	if _, err := DecodeEscapes("x\\X0D\\"); err == nil {
		t.Fatal("expected unknown escape rejection")
	}
	if _, err := DecodeEscapes("x\\F"); err == nil {
		t.Fatal("expected unterminated escape rejection")
	}
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

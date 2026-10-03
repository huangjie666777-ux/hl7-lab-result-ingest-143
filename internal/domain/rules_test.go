package domain

import (
	"testing"

	"example.com/hl7-lab-result-ingest/internal/hl7"
)

func item(status, value string) hl7.Item {
	return hl7.Item{Code: "WBC", System: "LN", ValueType: "NM", Value: value, Units: "u", Status: status}
}

func TestTransitions(t *testing.T) {
	cases := []struct {
		name    string
		cur     Current
		in      hl7.Item
		wantErr bool
	}{
		{"new P ok", Current{}, item("P", "1"), false},
		{"new F ok", Current{}, item("F", "1"), false},
		{"new C rejected", Current{}, item("C", "1"), true},
		{"P updated by P", Current{Exists: true, Status: "P", Value: "1", Units: "u"}, item("P", "2"), false},
		{"P upgraded to F", Current{Exists: true, Status: "P", Value: "1", Units: "u"}, item("F", "1"), false},
		{"P corrected rejected", Current{Exists: true, Status: "P", Value: "1", Units: "u"}, item("C", "2"), true},
		{"F corrected by C", Current{Exists: true, Status: "F", Value: "1", Units: "u"}, item("C", "2"), false},
		{"F resent same value ok", Current{Exists: true, Status: "F", Value: "1", Units: "u"}, item("F", "1"), false},
		{"F changed by F rejected", Current{Exists: true, Status: "F", Value: "1", Units: "u"}, item("F", "2"), true},
		{"F downgraded rejected", Current{Exists: true, Status: "F", Value: "1", Units: "u"}, item("P", "1"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Transition(tc.cur, tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

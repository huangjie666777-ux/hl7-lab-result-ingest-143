package store

import (
	"context"
	"errors"
	"testing"

	"example.com/hl7-lab-result-ingest/internal/hl7"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func msg(ctrl, order, patient, status, value string) *hl7.Message {
	return &hl7.Message{
		Raw:        "raw-" + ctrl + "-" + value,
		SendingApp: "LIS", SendingFac: "HOSP",
		ControlID: ctrl, PatientID: patient, OrderNum: order,
		Items: []hl7.Item{{
			ValueType: "NM", Code: "GLU", CodeSystem: "LN",
			Value: value, Unit: "mmol/L", Status: status,
		}},
	}
}

func mustApply(t *testing.T, st *Store, m *hl7.Message) bool {
	t.Helper()
	dup, err := st.Apply(context.Background(), m)
	if err != nil {
		t.Fatalf("apply %s: %v", m.ControlID, err)
	}
	return dup
}

func TestLifecycle(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	mustApply(t, st, msg("M1", "O1", "P1", "P", "5.0")) // preliminary
	mustApply(t, st, msg("M2", "O1", "P1", "P", "5.1")) // P updates P
	mustApply(t, st, msg("M3", "O1", "P1", "F", "5.2")) // upgrade to F

	// F cannot be changed by P or F.
	if _, err := st.Apply(ctx, msg("M4", "O1", "P1", "F", "9.9")); !errors.Is(err, ErrConflict) {
		t.Fatalf("F->F change should be rejected, got %v", err)
	}
	if _, err := st.Apply(ctx, msg("M5", "O1", "P1", "P", "9.9")); !errors.Is(err, ErrConflict) {
		t.Fatalf("F->P should be rejected, got %v", err)
	}
	// C corrects a confirmed result.
	mustApply(t, st, msg("M6", "O1", "P1", "C", "5.3"))
	// Corrected result can only be changed by C again.
	if _, err := st.Apply(ctx, msg("M7", "O1", "P1", "F", "5.4")); !errors.Is(err, ErrConflict) {
		t.Fatalf("C->F change should be rejected, got %v", err)
	}

	cur, err := st.Current(ctx, "LIS^HOSP", "O1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cur.Results) != 1 || cur.Results[0].Value != "5.3" || cur.Results[0].Version != 4 {
		t.Fatalf("bad current: %+v", cur.Results)
	}
	hist, err := st.History(ctx, "LIS^HOSP", "O1")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist.Results) != 4 {
		t.Fatalf("want 4 history rows, got %d", len(hist.Results))
	}
}

func TestCorrectionRequiresConfirmed(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	if _, err := st.Apply(ctx, msg("M1", "O1", "P1", "C", "1")); !errors.Is(err, ErrConflict) {
		t.Fatalf("C on new item should be rejected, got %v", err)
	}
	mustApply(t, st, msg("M2", "O1", "P1", "P", "1"))
	if _, err := st.Apply(ctx, msg("M3", "O1", "P1", "C", "2")); !errors.Is(err, ErrConflict) {
		t.Fatalf("C on preliminary should be rejected, got %v", err)
	}
}

func TestDedup(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	m := msg("M1", "O1", "P1", "P", "5.0")
	if dup := mustApply(t, st, m); dup {
		t.Fatal("first apply should not be duplicate")
	}
	if dup := mustApply(t, st, m); !dup {
		t.Fatal("identical redelivery should be a no-op duplicate")
	}
	other := msg("M1", "O1", "P1", "P", "6.0") // same control ID, different content
	if _, err := st.Apply(ctx, other); !errors.Is(err, ErrConflict) {
		t.Fatalf("same ID different content should be rejected, got %v", err)
	}
	hist, _ := st.History(ctx, "LIS^HOSP", "O1")
	if len(hist.Results) != 1 {
		t.Fatalf("duplicate must not add versions, got %d", len(hist.Results))
	}
}

func TestPatientBinding(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	mustApply(t, st, msg("M1", "O1", "P1", "P", "5.0"))
	if _, err := st.Apply(ctx, msg("M2", "O1", "P2", "P", "5.0")); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-patient order reuse should be rejected, got %v", err)
	}
	// Same order, same patient: fine.
	mustApply(t, st, msg("M3", "O1", "P1", "P", "5.1"))
}

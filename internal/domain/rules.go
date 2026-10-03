// Package domain holds the result status transition rules.
package domain

import (
	"errors"
	"fmt"

	"example.com/hl7-lab-result-ingest/internal/hl7"
)

// Current is the currently stored state of one result item.
type Current struct {
	Exists bool
	Status string // effective status: P or F
	Value  string
	Units  string
}

// Transition validates an incoming OBX status against the current state and
// returns the effective status to store (P or F).
//
// Rules:
//   - new item: P or F only; C requires an existing confirmed result
//   - current P: may be updated by P or upgraded to F
//   - current F (including corrected results): only C may change the value;
//     F with an identical value is a no-op, anything else is rejected
func Transition(cur Current, in hl7.Item) (string, error) {
	if !cur.Exists {
		switch in.Status {
		case hl7.StatusPreliminary:
			return hl7.StatusPreliminary, nil
		case hl7.StatusFinal:
			return hl7.StatusFinal, nil
		default:
			return "", fmt.Errorf("item %s^%s: correction without confirmed result", in.Code, in.System)
		}
	}
	switch cur.Status {
	case hl7.StatusPreliminary:
		switch in.Status {
		case hl7.StatusPreliminary:
			return hl7.StatusPreliminary, nil
		case hl7.StatusFinal:
			return hl7.StatusFinal, nil
		default:
			return "", fmt.Errorf("item %s^%s: correction requires confirmed result", in.Code, in.System)
		}
	case hl7.StatusFinal:
		switch in.Status {
		case hl7.StatusCorrected:
			return hl7.StatusFinal, nil
		case hl7.StatusFinal:
			if in.Value == cur.Value && in.Units == cur.Units {
				return hl7.StatusFinal, nil
			}
			return "", fmt.Errorf("item %s^%s: final result can only be changed by correction", in.Code, in.System)
		default:
			return "", fmt.Errorf("item %s^%s: cannot downgrade final result to preliminary", in.Code, in.System)
		}
	}
	return "", errors.New("unknown stored status")
}

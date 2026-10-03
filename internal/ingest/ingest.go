// Package ingest wires MLLP frames to parsing, validation and storage,
// producing HL7 ACKs.
package ingest

import (
	"context"
	"errors"
	"log"

	"example.com/hl7-lab-result-ingest/internal/hl7"
	"example.com/hl7-lab-result-ingest/internal/store"
)

type Service struct {
	st *store.Store
}

func New(st *store.Store) *Service { return &Service{st: st} }

// HandleFrame processes one raw HL7 message and returns the ACK message.
// It always returns an ACK; failures produce AE.
func (s *Service) HandleFrame(ctx context.Context, raw string) string {
	msg, err := hl7.Parse(raw)
	if err != nil {
		log.Printf("ingest: parse failed: %v", err)
		return hl7.BuildACK("AE", hl7.ExtractControlID(raw), err.Error())
	}
	dup, err := s.st.Apply(ctx, msg)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			log.Printf("ingest: rejected %s: %v", msg.ControlID, err)
			return hl7.BuildACK("AE", msg.ControlID, err.Error())
		}
		log.Printf("ingest: store failed for %s: %v", msg.ControlID, err)
		return hl7.BuildACK("AE", msg.ControlID, "internal error")
	}
	if dup {
		log.Printf("ingest: duplicate redelivery of %s acknowledged without changes", msg.ControlID)
	}
	return hl7.BuildACK("AA", msg.ControlID, "")
}

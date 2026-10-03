package hl7

import (
	"fmt"
	"strings"
	"time"
)

// BuildACK builds an ACK message for the given original message.
// code is "AA" (accepted) or "AE" (error). origControlID is echoed in MSA-2.
func BuildACK(code, origControlID, errText string) string {
	if origControlID == "" {
		origControlID = "UNKNOWN"
	}
	ts := time.Now().Format("20060102150405")
	ctrl := fmt.Sprintf("ACK%d", time.Now().UnixNano())
	msh := strings.Join([]string{
		"MSH", "^~\\&", "LABARCHIVE", "LAB", "", "", ts, "", "ACK^R01", ctrl, "P", "2.5.1",
	}, "|")
	msa := "MSA|" + code + "|" + origControlID
	if errText != "" {
		msa += "|" + errText
	}
	return msh + "\r" + msa
}

// ExtractControlID best-effort extracts MSH-10 from a raw message for ACKs
// when full parsing failed.
func ExtractControlID(raw string) string {
	first, _, _ := strings.Cut(raw, "\r")
	if !strings.HasPrefix(first, "MSH|") {
		return ""
	}
	f := strings.Split(first, "|")
	if len(f) > 9 {
		return f[9]
	}
	return ""
}

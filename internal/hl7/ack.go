package hl7

import (
	"fmt"
	"strings"
	"time"
)

// ACK codes written to MSA-1.
const (
	AckAA = "AA"
	AckAE = "AE"
)

// escapeField re-encodes HL7 reserved characters for outbound fields.
func escapeField(s string) string {
	r := strings.NewReplacer(
		"\\", "\\E\\",
		"|", "\\F\\",
		"^", "\\S\\",
		"~", "\\R\\",
		"&", "\\T\\",
	)
	return r.Replace(s)
}

// BuildACK renders an ACK^R01 message correlated to the inbound message.
func BuildACK(code, controlID, errText string, in *Message) string {
	app, fac := "", ""
	if in != nil {
		app, fac = in.SourceApp, in.SourceFacility
	}
	now := time.Now().UTC()
	ts := now.Format("20060102150405")
	id := fmt.Sprintf("ACK%d", now.UnixNano())
	msh := strings.Join([]string{
		"MSH", "^~\\&", "ARCHIVE", "LAB",
		escapeField(app), escapeField(fac),
		ts, "", "ACK^R01", id, "P", "2.5.1",
	}, "|")
	msa := "MSA|" + code + "|" + escapeField(controlID)
	if errText != "" {
		msa += "|" + escapeField(errText)
	}
	return msh + "\r" + msa + "\r"
}

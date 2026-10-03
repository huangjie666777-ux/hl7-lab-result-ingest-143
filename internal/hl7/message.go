package hl7

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	FieldSep    = '|'
	CompSep     = '^'
	RepSep      = '~'
	EscChar     = '\\'
	SubCompSep  = '&'
	MaxOBXCount = 20
)

var nmPattern = regexp.MustCompile(`^[+-]?(\d+(\.\d*)?|\.\d+)$`)

// Message is a parsed ORU^R01 message with the fields this service archives.
type Message struct {
	Raw        string
	SendingApp string // MSH-3
	SendingFac string // MSH-4
	ControlID  string // MSH-10
	PatientID  string // PID-3.1
	OrderNum   string // OBR-3
	Items      []Item
}

// Item is one OBX result.
type Item struct {
	SetID      string // OBX-1
	ValueType  string // OBX-2
	Code       string // OBX-3.1
	CodeSystem string // OBX-3.3
	SubID      string // OBX-4
	Value      string // OBX-5 (decoded for ST, decimal string for NM)
	Unit       string // OBX-6.1
	Status     string // OBX-11
}

// Source identifies the sending system: MSH-3 ^ MSH-4.
func (m *Message) Source() string {
	return m.SendingApp + "^" + m.SendingFac
}

// Key is the item identity: code + coding system + sub ID (no display name).
func (it Item) Key() string {
	return it.Code + "^" + it.CodeSystem + "^" + it.SubID
}

// Parse validates and parses a raw HL7 message (segments separated by CR).
func Parse(raw string) (*Message, error) {
	raw = strings.Trim(raw, "\r\n")
	if raw == "" {
		return nil, errors.New("empty message")
	}
	segs := strings.Split(raw, "\r")
	if len(segs) < 3 {
		return nil, errors.New("message must contain at least MSH, PID, OBR")
	}
	if !strings.HasPrefix(segs[0], "MSH") {
		return nil, errors.New("first segment must be MSH")
	}
	msh := strings.Split(segs[0], string(FieldSep))
	if len(msh) < 12 {
		return nil, errors.New("MSH segment too short")
	}
	if msh[1] != "^~\\&" {
		return nil, fmt.Errorf("unsupported encoding characters %q, only ^~\\& is supported", msh[1])
	}
	msgType := strings.Split(msh[8], string(CompSep))
	if len(msgType) < 2 || msgType[0] != "ORU" || msgType[1] != "R01" {
		return nil, fmt.Errorf("unsupported message type %q, only ORU^R01 is accepted", msh[8])
	}
	msg := &Message{
		Raw:        raw,
		SendingApp: comp(msh[2], 0),
		SendingFac: comp(msh[3], 0),
		ControlID:  msh[9],
	}
	if msg.SendingApp == "" || msg.SendingFac == "" {
		return nil, errors.New("MSH-3 and MSH-4 are required")
	}
	if msg.ControlID == "" {
		return nil, errors.New("MSH-10 message control ID is required")
	}

	var pidSeen, obrSeen bool
	for _, seg := range segs[1:] {
		switch {
		case strings.HasPrefix(seg, "PID"):
			if pidSeen {
				return nil, errors.New("exactly one PID segment is required")
			}
			pidSeen = true
			f := strings.Split(seg, string(FieldSep))
			if len(f) < 4 || comp(f[3], 0) == "" {
				return nil, errors.New("PID-3 patient ID is required")
			}
			msg.PatientID = comp(f[3], 0)
		case strings.HasPrefix(seg, "OBR"):
			if obrSeen {
				return nil, errors.New("exactly one OBR segment is required")
			}
			obrSeen = true
			f := strings.Split(seg, string(FieldSep))
			if len(f) < 4 || f[3] == "" {
				return nil, errors.New("OBR-3 filler order number is required")
			}
			msg.OrderNum = f[3]
		case strings.HasPrefix(seg, "OBX"):
			it, err := parseOBX(seg)
			if err != nil {
				return nil, err
			}
			msg.Items = append(msg.Items, *it)
		}
	}
	if !pidSeen || !obrSeen {
		return nil, errors.New("PID and OBR segments are required")
	}
	if len(msg.Items) == 0 {
		return nil, errors.New("at least one OBX segment is required")
	}
	if len(msg.Items) > MaxOBXCount {
		return nil, fmt.Errorf("at most %d OBX segments are allowed", MaxOBXCount)
	}
	seen := map[string]bool{}
	for _, it := range msg.Items {
		if seen[it.Key()] {
			return nil, fmt.Errorf("duplicate item %q in message", it.Key())
		}
		seen[it.Key()] = true
	}
	return msg, nil
}

func parseOBX(seg string) (*Item, error) {
	f := strings.Split(seg, string(FieldSep))
	if len(f) < 12 {
		return nil, fmt.Errorf("OBX segment too short: %q", seg)
	}
	it := &Item{SetID: f[1], ValueType: f[2]}
	if it.ValueType != "NM" && it.ValueType != "ST" {
		return nil, fmt.Errorf("OBX-2 value type %q not supported, only NM and ST", it.ValueType)
	}
	obx3 := strings.Split(f[3], string(CompSep))
	if len(obx3) < 3 || obx3[0] == "" || obx3[2] == "" {
		return nil, fmt.Errorf("OBX-3 must carry code and coding system: %q", f[3])
	}
	it.Code = obx3[0]
	it.CodeSystem = obx3[2]
	it.SubID = f[4]
	if f[5] == "" {
		return nil, fmt.Errorf("OBX-5 value is required for item %q", it.Code)
	}
	switch it.ValueType {
	case "NM":
		if !nmPattern.MatchString(f[5]) {
			return nil, fmt.Errorf("OBX-5 invalid NM value %q", f[5])
		}
		it.Value = f[5]
	case "ST":
		v, err := DecodeEscapes(f[5])
		if err != nil {
			return nil, fmt.Errorf("OBX-5: %w", err)
		}
		it.Value = v
	}
	it.Unit = comp(f[6], 0)
	switch f[11] {
	case "P", "F", "C":
		it.Status = f[11]
	default:
		return nil, fmt.Errorf("OBX-11 status %q not supported, only P, F, C", f[11])
	}
	return it, nil
}

func comp(field string, idx int) string {
	parts := strings.Split(field, string(CompSep))
	if idx >= len(parts) {
		return ""
	}
	return parts[idx]
}

// DecodeEscapes decodes HL7 ST escape sequences (F, S, R, T, E).
// Any unknown escape sequence is rejected.
func DecodeEscapes(s string) (string, error) {
	if !strings.ContainsRune(s, EscChar) {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != EscChar {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i+1:], EscChar)
		if end < 0 {
			return "", fmt.Errorf("unterminated escape sequence in %q", s)
		}
		switch code := s[i+1 : i+1+end]; code {
		case "F":
			b.WriteByte(FieldSep)
		case "S":
			b.WriteByte(CompSep)
		case "R":
			b.WriteByte(RepSep)
		case "T":
			b.WriteByte(SubCompSep)
		case "E":
			b.WriteByte(EscChar)
		default:
			return "", fmt.Errorf("unknown escape sequence \\%s\\", code)
		}
		i += end + 2
	}
	return b.String(), nil
}

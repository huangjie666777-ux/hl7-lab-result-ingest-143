package hl7

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Value types supported by the archive.
const (
	ValueTypeNM = "NM"
	ValueTypeST = "ST"
)

// OBX-11 result statuses accepted by the archive.
const (
	StatusPreliminary = "P"
	StatusFinal       = "F"
	StatusCorrected   = "C"
)

// Item is one parsed OBX result.
type Item struct {
	Code      string // OBX-3.1
	System    string // OBX-3.3
	SubID     string // OBX-4
	ValueType string
	Value     string // NM: decimal string; ST: escape-decoded text
	Units     string // OBX-6
	Status    string // OBX-11
}

// Message is a fully parsed and validated ORU^R01.
type Message struct {
	SourceApp      string // MSH-3
	SourceFacility string // MSH-4
	ControlID      string // MSH-10
	PatientID      string // PID-3.1
	OrderNo        string // OBR-3
	Items          []Item
}

// Source returns the stable source key built from MSH-3/MSH-4.
func (m *Message) Source() string { return m.SourceApp + "^" + m.SourceFacility }

var nmPattern = regexp.MustCompile(`^[+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)$`)

// ParseMessage parses a single CR-segmented HL7 message with fixed
// delimiters |^~\&. It enforces exactly one PID, one OBR and 1..20 OBX.
func ParseMessage(raw string) (*Message, error) {
	if strings.Contains(raw, "\x0b") || strings.ContainsRune(raw, 0x1c) {
		return nil, errors.New("message contains MLLP framing bytes")
	}
	segments := strings.Split(raw, "\r")
	msg := &Message{}
	var seenPID, seenOBR bool
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		fields := strings.Split(seg, "|")
		switch fields[0] {
		case "MSH":
			if msg.ControlID != "" {
				return nil, errors.New("duplicate MSH segment")
			}
			if err := parseMSH(fields, msg); err != nil {
				return nil, err
			}
		case "PID":
			if seenPID {
				return nil, errors.New("duplicate PID segment")
			}
			seenPID = true
			pid3 := field(fields, 3)
			msg.PatientID = component(pid3, 1)
			if msg.PatientID == "" {
				return nil, errors.New("missing PID-3 patient id")
			}
		case "OBR":
			if seenOBR {
				return nil, errors.New("duplicate OBR segment")
			}
			seenOBR = true
			msg.OrderNo = field(fields, 3)
			if msg.OrderNo == "" {
				return nil, errors.New("missing OBR-3 order number")
			}
		case "OBX":
			item, err := parseOBX(fields)
			if err != nil {
				return nil, err
			}
			msg.Items = append(msg.Items, item)
		default:
			return nil, fmt.Errorf("unsupported segment %q", fields[0])
		}
	}
	if msg.ControlID == "" {
		return nil, errors.New("missing MSH segment")
	}
	if !seenPID {
		return nil, errors.New("missing PID segment")
	}
	if !seenOBR {
		return nil, errors.New("missing OBR segment")
	}
	if len(msg.Items) == 0 || len(msg.Items) > 20 {
		return nil, fmt.Errorf("OBX count %d out of range 1..20", len(msg.Items))
	}
	seen := make(map[string]bool, len(msg.Items))
	for _, it := range msg.Items {
		key := it.Code + "\x00" + it.System + "\x00" + it.SubID
		if seen[key] {
			return nil, fmt.Errorf("duplicate item %s^%s sub %s in message", it.Code, it.System, it.SubID)
		}
		seen[key] = true
	}
	return msg, nil
}

func parseMSH(fields []string, msg *Message) error {
	if len(fields) < 12 {
		return errors.New("MSH segment too short")
	}
	if fields[1] != "^~\\&" {
		return fmt.Errorf("unsupported encoding characters %q", fields[1])
	}
	msg.SourceApp = fields[2]
	msg.SourceFacility = fields[3]
	if msg.SourceApp == "" || msg.SourceFacility == "" {
		return errors.New("missing MSH-3/MSH-4 source")
	}
	if mt := fields[8]; mt != "ORU^R01" {
		return fmt.Errorf("unsupported message type %q", mt)
	}
	msg.ControlID = fields[9]
	if msg.ControlID == "" {
		return errors.New("missing MSH-10 control id")
	}
	if v := fields[11]; v != "2.5.1" {
		return fmt.Errorf("unsupported HL7 version %q", v)
	}
	return nil
}

func parseOBX(fields []string) (Item, error) {
	var it Item
	if len(fields) < 12 {
		return it, errors.New("OBX segment too short")
	}
	it.ValueType = fields[2]
	if it.ValueType != ValueTypeNM && it.ValueType != ValueTypeST {
		return it, fmt.Errorf("unsupported OBX-2 value type %q", it.ValueType)
	}
	obx3 := strings.Split(fields[3], "^")
	if len(obx3) < 3 || obx3[0] == "" || obx3[2] == "" {
		return it, errors.New("OBX-3 requires code and coding system")
	}
	it.Code, it.System = obx3[0], obx3[2]
	it.SubID = fields[4]
	rawValue := fields[5]
	if rawValue == "" {
		return it, errors.New("missing OBX-5 value")
	}
	switch it.ValueType {
	case ValueTypeNM:
		if !nmPattern.MatchString(rawValue) {
			return it, fmt.Errorf("invalid NM value %q", rawValue)
		}
		it.Value = rawValue
	case ValueTypeST:
		decoded, err := DecodeEscapes(rawValue)
		if err != nil {
			return it, err
		}
		it.Value = decoded
	}
	it.Units = component(fields[6], 1)
	it.Status = fields[11]
	switch it.Status {
	case StatusPreliminary, StatusFinal, StatusCorrected:
	default:
		return it, fmt.Errorf("unsupported OBX-11 status %q", it.Status)
	}
	return it, nil
}

// DecodeEscapes decodes HL7 ST escape sequences \F\ \S\ \R\ \T\ \E\.
// Any other escape sequence is rejected.
func DecodeEscapes(s string) (string, error) {
	if !strings.ContainsRune(s, '\\') {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+2 >= len(s) || s[i+2] != '\\' {
			return "", fmt.Errorf("invalid escape sequence at offset %d", i)
		}
		switch s[i+1] {
		case 'F':
			b.WriteByte('|')
		case 'S':
			b.WriteByte('^')
		case 'R':
			b.WriteByte('~')
		case 'T':
			b.WriteByte('&')
		case 'E':
			b.WriteByte('\\')
		default:
			return "", fmt.Errorf("unknown escape sequence \\%c\\", s[i+1])
		}
		i += 2
	}
	return b.String(), nil
}

func field(fields []string, n int) string {
	if n < len(fields) {
		return fields[n]
	}
	return ""
}

func component(f string, n int) string {
	parts := strings.Split(f, "^")
	if n-1 < len(parts) {
		return parts[n-1]
	}
	return ""
}

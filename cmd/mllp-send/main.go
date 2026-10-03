// mllp-send sends an HL7 message file over MLLP and prints the ACK.
// Usage: mllp-send -addr localhost:2575 message.hl7
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", "localhost:2575", "MLLP server address")
	flag.Parse()
	if flag.NArg() != 1 {
		log.Fatal("usage: mllp-send -addr host:port <message-file>")
	}
	data, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	// Normalize line endings to CR segment separators.
	msg := strings.ReplaceAll(string(data), "\r\n", "\n")
	msg = strings.ReplaceAll(msg, "\n", "\r")
	msg = strings.TrimRight(msg, "\r")

	conn, err := net.DialTimeout("tcp", *addr, 5*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	frame := append([]byte{0x0B}, []byte(msg)...)
	frame = append(frame, 0x1C, 0x0D)
	if _, err := conn.Write(frame); err != nil {
		log.Fatal(err)
	}

	// Read exactly one MLLP frame: VT <payload> FS CR.
	r := bufio.NewReader(conn)
	b, err := r.ReadByte()
	if err != nil {
		log.Fatal(err)
	}
	if b != 0x0B {
		log.Fatalf("expected VT, got %#x", b)
	}
	var sb strings.Builder
	prev := byte(0)
	for {
		b, err := r.ReadByte()
		if err != nil {
			log.Fatal(err)
		}
		if prev == 0x1C && b == 0x0D {
			break
		}
		if prev != 0 {
			sb.WriteByte(prev)
		}
		prev = b
	}
	fmt.Println(strings.ReplaceAll(sb.String(), "\r", "\n"))
}

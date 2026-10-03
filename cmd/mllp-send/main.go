// Command mllp-send is a demo MLLP client: it sends one HL7 message read
// from a file (or stdin) and prints the ACK.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:2575", "MLLP server address")
	flag.Parse()

	var data []byte
	var err error
	if flag.NArg() > 0 {
		data, err = os.ReadFile(flag.Arg(0))
	} else {
		data, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "read message:", err)
		os.Exit(1)
	}
	data = bytes.ReplaceAll(data, []byte("\n"), []byte("\r"))

	conn, err := net.DialTimeout("tcp", *addr, 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		os.Exit(1)
	}
	defer conn.Close()

	frame := append([]byte{0x0b}, data...)
	frame = append(frame, 0x1c, 0x0d)
	if _, err := conn.Write(frame); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read ack:", err)
		os.Exit(1)
	}
	ack := bytes.Trim(buf[:n], "\x0b\x1c\r")
	fmt.Println(string(bytes.ReplaceAll(ack, []byte("\r"), []byte("\n"))))
}

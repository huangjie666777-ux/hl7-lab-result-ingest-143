// Package mllp implements an MLLP/TCP listener with HL7 framing.
package mllp

import (
	"bufio"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const (
	vtByte = 0x0B
	fsByte = 0x1C
	crByte = 0x0D

	// MaxFrameSize limits a single HL7 frame to 1 MiB.
	MaxFrameSize = 1 << 20
)

// Handler processes one decoded HL7 message and returns the ACK payload.
type Handler func(raw string) (ack string)

// Server is an MLLP/TCP server with connection limits and read deadlines.
type Server struct {
	Addr         string
	Handler      Handler
	MaxConns     int
	ReadTimeout  time.Duration // idle read deadline per frame
	WriteTimeout time.Duration

	ln     net.Listener
	wg     sync.WaitGroup
	sem    chan struct{}
	closed chan struct{}
	once   sync.Once
}

// ListenAndServe starts accepting connections until Close is called.
func (s *Server) ListenAndServe() error {
	if s.MaxConns <= 0 {
		s.MaxConns = 32
	}
	if s.ReadTimeout <= 0 {
		s.ReadTimeout = 30 * time.Second
	}
	if s.WriteTimeout <= 0 {
		s.WriteTimeout = 10 * time.Second
	}
	s.sem = make(chan struct{}, s.MaxConns)
	s.closed = make(chan struct{})
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	s.ln = ln
	log.Printf("mllp: listening on %s (max %d conns)", ln.Addr(), s.MaxConns)
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.closed:
				s.wg.Wait()
				return nil
			default:
				return err
			}
		}
		select {
		case s.sem <- struct{}{}:
		default:
			log.Printf("mllp: connection limit reached, rejecting %s", conn.RemoteAddr())
			conn.Close()
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { <-s.sem }()
			s.serveConn(conn)
		}()
	}
}

// Close stops accepting and waits for in-flight connections to finish.
func (s *Server) Close() error {
	s.once.Do(func() {
		close(s.closed)
		if s.ln != nil {
			s.ln.Close()
		}
	})
	return nil
}

func (s *Server) serveConn(conn net.Conn) {
	defer conn.Close()
	log.Printf("mllp: connection from %s", conn.RemoteAddr())
	r := bufio.NewReaderSize(conn, 64*1024)
	for {
		conn.SetReadDeadline(time.Now().Add(s.ReadTimeout))
		frame, err := readFrame(r)
		if err != nil {
			if !errors.Is(err, io.EOF) && !isTimeout(err) {
				log.Printf("mllp: %s: %v", conn.RemoteAddr(), err)
			}
			return
		}
		ack := s.Handler(frame)
		conn.SetWriteDeadline(time.Now().Add(s.WriteTimeout))
		if _, err := conn.Write([]byte{vtByte}); err != nil {
			return
		}
		if _, err := conn.Write([]byte(ack)); err != nil {
			return
		}
		if _, err := conn.Write([]byte{fsByte, crByte}); err != nil {
			return
		}
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// readFrame reads one MLLP frame: VT <payload> FS CR. It tolerates the
// payload arriving in multiple TCP segments and multiple frames per segment.
func readFrame(r *bufio.Reader) (string, error) {
	// Wait for start-of-block.
	for {
		b, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		if b == vtByte {
			break
		}
	}
	buf := make([]byte, 0, 4096)
	for {
		b, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		if b == fsByte {
			// Expect trailing CR.
			end, err := r.ReadByte()
			if err != nil {
				return "", err
			}
			if end != crByte {
				return "", errors.New("mllp: FS not followed by CR")
			}
			return string(buf), nil
		}
		buf = append(buf, b)
		if len(buf) > MaxFrameSize {
			return "", errors.New("mllp: frame exceeds 1 MiB limit")
		}
	}
}

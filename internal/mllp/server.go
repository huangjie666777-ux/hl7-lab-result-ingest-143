// Package mllp implements the MLLP/TCP listener: framing, connection
// limits, read deadlines and ACK delivery.
package mllp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"example.com/hl7-lab-result-ingest/internal/hl7"
	"example.com/hl7-lab-result-ingest/internal/store"
)

const (
	vt byte = 0x0b
	fs byte = 0x1c
	cr byte = 0x0d

	// MaxFrame is the maximum accepted message size (1 MiB).
	MaxFrame = 1 << 20
)

// Server receives HL7 messages over MLLP.
type Server struct {
	addr         string
	store        *store.Store
	maxConns     int
	readTimeout  time.Duration
	writeTimeout time.Duration

	ln     net.Listener
	sem    chan struct{}
	wg     sync.WaitGroup
	closed chan struct{}
	once   sync.Once
}

// New creates a Server. readTimeout bounds each read; maxConns caps
// simultaneous connections.
func New(addr string, st *store.Store, maxConns int, readTimeout time.Duration) *Server {
	return &Server{
		addr:         addr,
		store:        st,
		maxConns:     maxConns,
		readTimeout:  readTimeout,
		writeTimeout: 10 * time.Second,
		sem:          make(chan struct{}, maxConns),
		closed:       make(chan struct{}),
	}
}

// ListenAndServe accepts connections until Close is called.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.ln = ln
	log.Printf("mllp: listening on %s", ln.Addr())
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.closed:
				s.wg.Wait()
				return nil
			default:
			}
			return err
		}
		select {
		case s.sem <- struct{}{}:
		default:
			log.Printf("mllp: connection limit reached, dropping %s", conn.RemoteAddr())
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

// Close stops accepting and waits for in-flight connections.
func (s *Server) Close() error {
	s.once.Do(func() { close(s.closed) })
	if s.ln != nil {
		return s.ln.Close()
	}
	return nil
}

func (s *Server) serveConn(conn net.Conn) {
	defer conn.Close()
	var buf []byte
	tmp := make([]byte, 64*1024)
	for {
		if err := conn.SetReadDeadline(time.Now().Add(s.readTimeout)); err != nil {
			return
		}
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if len(buf) > MaxFrame+2 {
				log.Printf("mllp: frame from %s exceeds 1MiB, closing", conn.RemoteAddr())
				return
			}
			var rest []byte
			frames, rest := extractFrames(buf)
			buf = rest
			for _, frame := range frames {
				s.handleFrame(conn, frame)
			}
		}
		if err != nil {
			return // timeout, EOF or reset: recycle the connection
		}
	}
}

// extractFrames splits complete MLLP frames (VT ... FS CR) off buf and
// returns the remaining partial data. Supports half frames and multiple
// frames per connection.
func extractFrames(buf []byte) (frames [][]byte, rest []byte) {
	for {
		start := bytes.IndexByte(buf, vt)
		if start < 0 {
			return frames, nil
		}
		end := bytes.Index(buf[start+1:], []byte{fs, cr})
		if end < 0 {
			return frames, buf[start:]
		}
		frame := buf[start+1 : start+1+end]
		if len(frame) > 0 {
			frames = append(frames, frame)
		}
		buf = buf[start+1+end+2:]
	}
}

func (s *Server) handleFrame(conn net.Conn, frame []byte) {
	raw := string(frame)
	msg, err := hl7.ParseMessage(raw)
	var code, controlID, errText string
	if err != nil {
		code, errText = hl7.AckAE, err.Error()
	} else {
		controlID = msg.ControlID
		err = s.store.Ingest(context.Background(), raw, msg)
		switch {
		case err == nil, errors.Is(err, store.ErrDuplicate):
			code = hl7.AckAA
		default:
			code, errText = hl7.AckAE, err.Error()
		}
	}
	ack := hl7.BuildACK(code, controlID, errText, msg)
	out := append([]byte{vt}, []byte(ack)...)
	out = append(out, fs, cr)
	conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
	if _, err := conn.Write(out); err != nil {
		log.Printf("mllp: write ack to %s: %v", conn.RemoteAddr(), err)
	}
	log.Printf("mllp: %s -> %s (%s)", controlID, code, firstLine(errText))
}

func firstLine(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return fmt.Sprintf("%s", s)
}

// Package store implements the SQLite repository: dedup, transactional
// ingest of results and history, and query APIs.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"example.com/hl7-lab-result-ingest/internal/domain"
	"example.com/hl7-lab-result-ingest/internal/hl7"
)

// ErrDuplicateContent is returned when a control id is re-sent with a
// different payload.
var ErrDuplicateContent = errors.New("control id already used with different content")

// ErrDuplicate is returned when the exact same message is re-sent; the
// caller should ACK AA without writing anything.
var ErrDuplicate = errors.New("duplicate message")

const schema = `
PRAGMA journal_mode=WAL;
CREATE TABLE IF NOT EXISTS messages (
  id         INTEGER PRIMARY KEY,
  source     TEXT NOT NULL,
  control_id TEXT NOT NULL,
  sha256     TEXT NOT NULL,
  raw        TEXT NOT NULL,
  received_at TEXT NOT NULL,
  UNIQUE(source, control_id)
);
CREATE TABLE IF NOT EXISTS orders (
  id         INTEGER PRIMARY KEY,
  source     TEXT NOT NULL,
  order_no   TEXT NOT NULL,
  patient_id TEXT NOT NULL,
  UNIQUE(source, order_no)
);
CREATE TABLE IF NOT EXISTS results (
  id          INTEGER PRIMARY KEY,
  order_id    INTEGER NOT NULL REFERENCES orders(id),
  item_code   TEXT NOT NULL,
  item_system TEXT NOT NULL,
  sub_id      TEXT NOT NULL,
  value_type  TEXT NOT NULL,
  value       TEXT NOT NULL,
  units       TEXT NOT NULL,
  status      TEXT NOT NULL,
  corrected   INTEGER NOT NULL DEFAULT 0,
  updated_at  TEXT NOT NULL,
  UNIQUE(order_id, item_code, item_system, sub_id)
);
CREATE TABLE IF NOT EXISTS history (
  id         INTEGER PRIMARY KEY,
  result_id  INTEGER NOT NULL REFERENCES results(id),
  message_id INTEGER NOT NULL REFERENCES messages(id),
  status     TEXT NOT NULL,
  value      TEXT NOT NULL,
  units      TEXT NOT NULL,
  recorded_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_history_result ON history(result_id, id);
`

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (and migrates) the database at path.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_foreign_keys=on", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite is single-writer; serialize access
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// Ingest validates and stores a parsed message atomically: dedup record,
// current results and history are committed together or not at all.
func (s *Store) Ingest(ctx context.Context, raw string, msg *hl7.Message) error {
	sum := sha256.Sum256([]byte(raw))
	digest := hex.EncodeToString(sum[:])
	source := msg.Source()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Dedup by (source, control id).
	var existingHash string
	err = tx.QueryRowContext(ctx,
		`SELECT sha256 FROM messages WHERE source=? AND control_id=?`,
		source, msg.ControlID).Scan(&existingHash)
	if err == nil {
		if existingHash == digest {
			return ErrDuplicate
		}
		return ErrDuplicateContent
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	// Bind order to patient; reject patient mix-ups.
	var orderID int64
	var boundPatient string
	err = tx.QueryRowContext(ctx,
		`SELECT id, patient_id FROM orders WHERE source=? AND order_no=?`,
		source, msg.OrderNo).Scan(&orderID, &boundPatient)
	if errors.Is(err, sql.ErrNoRows) {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO orders(source, order_no, patient_id) VALUES(?,?,?)`,
			source, msg.OrderNo, msg.PatientID)
		if err != nil {
			return err
		}
		orderID, err = res.LastInsertId()
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if boundPatient != msg.PatientID {
		return fmt.Errorf("order %s already bound to patient %s", msg.OrderNo, boundPatient)
	}

	// Record the message first so history can reference it.
	msgRes, err := tx.ExecContext(ctx,
		`INSERT INTO messages(source, control_id, sha256, raw, received_at) VALUES(?,?,?,?,?)`,
		source, msg.ControlID, digest, raw, nowUTC())
	if err != nil {
		return err
	}
	messageID, err := msgRes.LastInsertId()
	if err != nil {
		return err
	}

	for _, item := range msg.Items {
		if err := applyItem(ctx, tx, orderID, messageID, item); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func applyItem(ctx context.Context, tx *sql.Tx, orderID, messageID int64, item hl7.Item) error {
	cur := domain.Current{}
	var resultID int64
	err := tx.QueryRowContext(ctx,
		`SELECT id, status, value, units FROM results
		 WHERE order_id=? AND item_code=? AND item_system=? AND sub_id=?`,
		orderID, item.Code, item.System, item.SubID).
		Scan(&resultID, &cur.Status, &cur.Value, &cur.Units)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		cur.Exists = false
	case err != nil:
		return err
	default:
		cur.Exists = true
	}

	newStatus, err := domain.Transition(cur, item)
	if err != nil {
		return err
	}

	corrected := 0
	if item.Status == hl7.StatusCorrected {
		corrected = 1
	}
	if !cur.Exists {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO results(order_id, item_code, item_system, sub_id, value_type, value, units, status, corrected, updated_at)
			 VALUES(?,?,?,?,?,?,?,?,?,?)`,
			orderID, item.Code, item.System, item.SubID, item.ValueType,
			item.Value, item.Units, newStatus, corrected, nowUTC())
		if err != nil {
			return err
		}
		resultID, err = res.LastInsertId()
		if err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx,
			`UPDATE results SET value_type=?, value=?, units=?, status=?, corrected=?, updated_at=? WHERE id=?`,
			item.ValueType, item.Value, item.Units, newStatus, corrected, nowUTC(), resultID); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO history(result_id, message_id, status, value, units, recorded_at) VALUES(?,?,?,?,?,?)`,
		resultID, messageID, item.Status, item.Value, item.Units, nowUTC())
	return err
}

// Result is the current state of one item as exposed by the query API.
type Result struct {
	ItemCode   string `json:"item_code"`
	ItemSystem string `json:"item_system"`
	SubID      string `json:"sub_id"`
	ValueType  string `json:"value_type"`
	Value      string `json:"value"`
	Units      string `json:"units"`
	Status     string `json:"status"`
	Corrected  bool   `json:"corrected"`
	UpdatedAt  string `json:"updated_at"`
}

// HistoryEntry is one recorded version of a result.
type HistoryEntry struct {
	ItemCode   string `json:"item_code"`
	ItemSystem string `json:"item_system"`
	SubID      string `json:"sub_id"`
	Status     string `json:"status"`
	Value      string `json:"value"`
	Units      string `json:"units"`
	ControlID  string `json:"control_id"`
	RecordedAt string `json:"recorded_at"`
}

// ErrNotFound is returned when the source/order pair is unknown.
var ErrNotFound = errors.New("order not found")

func (s *Store) findOrder(ctx context.Context, source, orderNo string) (int64, string, error) {
	var id int64
	var patient string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, patient_id FROM orders WHERE source=? AND order_no=?`,
		source, orderNo).Scan(&id, &patient)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrNotFound
	}
	return id, patient, err
}

// CurrentResults returns the patient id and current results for an order.
func (s *Store) CurrentResults(ctx context.Context, source, orderNo string) (string, []Result, error) {
	orderID, patient, err := s.findOrder(ctx, source, orderNo)
	if err != nil {
		return "", nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT item_code, item_system, sub_id, value_type, value, units, status, corrected, updated_at
		 FROM results WHERE order_id=? ORDER BY item_code, item_system, sub_id`, orderID)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	var out []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.ItemCode, &r.ItemSystem, &r.SubID, &r.ValueType,
			&r.Value, &r.Units, &r.Status, &r.Corrected, &r.UpdatedAt); err != nil {
			return "", nil, err
		}
		out = append(out, r)
	}
	return patient, out, rows.Err()
}

// History returns all recorded versions for an order, oldest first.
func (s *Store) History(ctx context.Context, source, orderNo string) (string, []HistoryEntry, error) {
	orderID, patient, err := s.findOrder(ctx, source, orderNo)
	if err != nil {
		return "", nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.item_code, r.item_system, r.sub_id, h.status, h.value, h.units, m.control_id, h.recorded_at
		 FROM history h
		 JOIN results r ON r.id = h.result_id
		 JOIN messages m ON m.id = h.message_id
		 WHERE r.order_id=? ORDER BY h.id`, orderID)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	var out []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		if err := rows.Scan(&e.ItemCode, &e.ItemSystem, &e.SubID, &e.Status,
			&e.Value, &e.Units, &e.ControlID, &e.RecordedAt); err != nil {
			return "", nil, err
		}
		out = append(out, e)
	}
	return patient, out, rows.Err()
}

// Package store persists results, history and dedup records in SQLite.
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

	"example.com/hl7-lab-result-ingest/internal/hl7"
)

// ErrConflict marks message-level rejections (AE) such as duplicate control
// IDs with different content or patient/order mismatches.
var ErrConflict = errors.New("message rejected")

type Store struct {
	db *sql.DB
}

// Result is one archived observation value.
type Result struct {
	Code       string    `json:"code"`
	CodeSystem string    `json:"code_system"`
	SubID      string    `json:"sub_id"`
	ValueType  string    `json:"value_type"`
	Value      string    `json:"value"`
	Unit       string    `json:"unit"`
	Status     string    `json:"status"`
	Version    int       `json:"version"`
	ControlID  string    `json:"control_id"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// OrderView is the query result for one order.
type OrderView struct {
	Source    string   `json:"source"`
	OrderNum  string   `json:"order_num"`
	PatientID string   `json:"patient_id"`
	Results   []Result `json:"results"`
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // serialize writers; SQLite is single-writer anyway
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS orders (
  source     TEXT NOT NULL,
  order_num  TEXT NOT NULL,
  patient_id TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (source, order_num)
);
CREATE TABLE IF NOT EXISTS messages (
  source      TEXT NOT NULL,
  control_id  TEXT NOT NULL,
  raw_hash    TEXT NOT NULL,
  raw_message TEXT NOT NULL,
  received_at TEXT NOT NULL,
  PRIMARY KEY (source, control_id)
);
CREATE TABLE IF NOT EXISTS current_results (
  source      TEXT NOT NULL,
  order_num   TEXT NOT NULL,
  item_key    TEXT NOT NULL,
  code        TEXT NOT NULL,
  code_system TEXT NOT NULL,
  sub_id      TEXT NOT NULL,
  value_type  TEXT NOT NULL,
  value       TEXT NOT NULL,
  unit        TEXT NOT NULL,
  status      TEXT NOT NULL,
  version     INTEGER NOT NULL,
  control_id  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  PRIMARY KEY (source, order_num, item_key)
);
CREATE TABLE IF NOT EXISTS result_history (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  source      TEXT NOT NULL,
  order_num   TEXT NOT NULL,
  item_key    TEXT NOT NULL,
  code        TEXT NOT NULL,
  code_system TEXT NOT NULL,
  sub_id      TEXT NOT NULL,
  value_type  TEXT NOT NULL,
  value       TEXT NOT NULL,
  unit        TEXT NOT NULL,
  status      TEXT NOT NULL,
  version     INTEGER NOT NULL,
  control_id  TEXT NOT NULL,
  created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_history_order ON result_history (source, order_num, item_key, version);
`)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

// Apply stores a parsed message atomically. It returns alreadyProcessed=true
// when the same (source, control ID) with identical content was stored before.
// A returned error wrapping ErrConflict means the message must be NAKed (AE)
// and nothing was written.
func (s *Store) Apply(ctx context.Context, msg *hl7.Message) (alreadyProcessed bool, err error) {
	sum := sha256.Sum256([]byte(msg.Raw))
	hash := hex.EncodeToString(sum[:])
	now := time.Now().UTC().Format(time.RFC3339Nano)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	// Dedup by (source, MSH-10).
	var prevHash string
	err = tx.QueryRowContext(ctx,
		`SELECT raw_hash FROM messages WHERE source=? AND control_id=?`,
		msg.Source(), msg.ControlID).Scan(&prevHash)
	switch {
	case err == nil && prevHash == hash:
		return true, tx.Commit() // identical redelivery: no new version
	case err == nil:
		return false, fmt.Errorf("%w: control ID %s already used with different content", ErrConflict, msg.ControlID)
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}

	// Bind order to patient; reject cross-patient reuse of the order number.
	var patient string
	err = tx.QueryRowContext(ctx,
		`SELECT patient_id FROM orders WHERE source=? AND order_num=?`,
		msg.Source(), msg.OrderNum).Scan(&patient)
	switch {
	case err == nil && patient != msg.PatientID:
		return false, fmt.Errorf("%w: order %s already bound to patient %s", ErrConflict, msg.OrderNum, patient)
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO orders (source, order_num, patient_id, created_at) VALUES (?,?,?,?)`,
			msg.Source(), msg.OrderNum, msg.PatientID, now); err != nil {
			return false, err
		}
	case err != nil:
		return false, err
	}

	for _, it := range msg.Items {
		if err := applyItem(ctx, tx, msg, it, now); err != nil {
			return false, err
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO messages (source, control_id, raw_hash, raw_message, received_at) VALUES (?,?,?,?,?)`,
		msg.Source(), msg.ControlID, hash, msg.Raw, now); err != nil {
		return false, err
	}
	return false, tx.Commit()
}

func applyItem(ctx context.Context, tx *sql.Tx, msg *hl7.Message, it hl7.Item, now string) error {
	var cur struct {
		value, unit, status string
		version             int
	}
	err := tx.QueryRowContext(ctx,
		`SELECT value, unit, status, version FROM current_results WHERE source=? AND order_num=? AND item_key=?`,
		msg.Source(), msg.OrderNum, it.Key()).
		Scan(&cur.value, &cur.unit, &cur.status, &cur.version)
	notFound := errors.Is(err, sql.ErrNoRows)
	if err != nil && !notFound {
		return err
	}

	switch {
	case notFound:
		if it.Status == "C" {
			return fmt.Errorf("%w: correction for %s without a confirmed result", ErrConflict, it.Code)
		}
		cur.version = 0
	default:
		locked := cur.status == "F" || cur.status == "C"
		if locked && it.Status != "C" {
			if it.Value == cur.value && it.Unit == cur.unit {
				return nil // identical re-report of a final result: no-op
			}
			return fmt.Errorf("%w: item %s is final, only C may change its value", ErrConflict, it.Code)
		}
		if !locked && it.Status == "C" {
			return fmt.Errorf("%w: correction for %s requires a confirmed result", ErrConflict, it.Code)
		}
	}

	version := cur.version + 1
	if _, err := tx.ExecContext(ctx, `
INSERT INTO current_results (source, order_num, item_key, code, code_system, sub_id, value_type, value, unit, status, version, control_id, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT (source, order_num, item_key) DO UPDATE SET
  value_type=excluded.value_type, value=excluded.value, unit=excluded.unit,
  status=excluded.status, version=excluded.version, control_id=excluded.control_id, updated_at=excluded.updated_at`,
		msg.Source(), msg.OrderNum, it.Key(), it.Code, it.CodeSystem, it.SubID,
		it.ValueType, it.Value, it.Unit, it.Status, version, msg.ControlID, now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO result_history (source, order_num, item_key, code, code_system, sub_id, value_type, value, unit, status, version, control_id, created_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		msg.Source(), msg.OrderNum, it.Key(), it.Code, it.CodeSystem, it.SubID,
		it.ValueType, it.Value, it.Unit, it.Status, version, msg.ControlID, now)
	return err
}

// Current returns the current results for one source + order.
func (s *Store) Current(ctx context.Context, source, order string) (*OrderView, error) {
	return s.query(ctx, source, order, false)
}

// History returns all versions for one source + order.
func (s *Store) History(ctx context.Context, source, order string) (*OrderView, error) {
	return s.query(ctx, source, order, true)
}

func (s *Store) query(ctx context.Context, source, order string, history bool) (*OrderView, error) {
	view := &OrderView{Source: source, OrderNum: order, Results: []Result{}}
	err := s.db.QueryRowContext(ctx,
		`SELECT patient_id FROM orders WHERE source=? AND order_num=?`, source, order).
		Scan(&view.PatientID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rows *sql.Rows
	if history {
		rows, err = s.db.QueryContext(ctx, `
SELECT code, code_system, sub_id, value_type, value, unit, status, version, control_id, created_at
FROM result_history WHERE source=? AND order_num=? ORDER BY item_key, version`, source, order)
	} else {
		rows, err = s.db.QueryContext(ctx, `
SELECT code, code_system, sub_id, value_type, value, unit, status, version, control_id, updated_at
FROM current_results WHERE source=? AND order_num=? ORDER BY item_key`, source, order)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Result
		var ts string
		if err := rows.Scan(&r.Code, &r.CodeSystem, &r.SubID, &r.ValueType, &r.Value,
			&r.Unit, &r.Status, &r.Version, &r.ControlID, &ts); err != nil {
			return nil, err
		}
		r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, ts)
		view.Results = append(view.Results, r)
	}
	return view, rows.Err()
}

package main

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// State is a run's progress, rewritten atomically after every order.
type State struct {
	RunID       string    `json:"run_id"`
	Store       string    `json:"store"`
	ProfileHash string    `json:"profile_hash"`
	GenNow      time.Time `json:"gen_now"`
	Total       int       `json:"total"`
	Done        int       `json:"done"`
	Created     int       `json:"created"`
	Failed      int       `json:"failed"`
	// Inflight is the index of an order whose create was started but not yet
	// recorded. Resume looks it up by tag before creating anything.
	Inflight  *int      `json:"inflight,omitempty"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OrderRecord is one line of orders.jsonl.
type OrderRecord struct {
	Index       int       `json:"index"`
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Outcome     string    `json:"outcome"`
	ProcessedAt time.Time `json:"processed_at"`
}

// FailureRecord is one line of failures.jsonl.
type FailureRecord struct {
	Index   int       `json:"index"`
	Error   string    `json:"error"`
	Time    time.Time `json:"time"`
	OrderID string    `json:"order_id,omitempty"`
}

// RunDir is <state-dir>/<run-id>.
type RunDir string

func (d RunDir) path(name string) string { return filepath.Join(string(d), name) }

var runIDPattern = regexp.MustCompile(`^\d{8}-[0-9a-f]{6}$`)

// newRunID returns YYYYMMDD-<6 hex>.
func newRunID(now time.Time) string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return now.UTC().Format("20060102") + "-" + hex.EncodeToString(b)
}

func profileHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

// writeAtomic writes b to path via a temp file and rename, so a crash never
// leaves a half-written file.
func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(b, '\n'))
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func appendJSONL(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (d RunDir) SaveState(st *State) error { return writeJSONAtomic(d.path("state.json"), st) }

func (d RunDir) LoadState() (*State, error) {
	var st State
	if err := readJSON(d.path("state.json"), &st); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no run at %s (check -resume and -state-dir)", string(d))
		}
		return nil, err
	}
	return &st, nil
}

func (d RunDir) AppendOrder(r OrderRecord) error     { return appendJSONL(d.path("orders.jsonl"), r) }
func (d RunDir) AppendFailure(r FailureRecord) error { return appendJSONL(d.path("failures.jsonl"), r) }

// HasOrder reports whether orders.jsonl already records index.
func (d RunDir) HasOrder(index int) bool {
	f, err := os.Open(d.path("orders.jsonl"))
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r OrderRecord
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.Index == index {
			return true
		}
	}
	return false
}

package flock

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func mustScan(t *testing.T, f *Flock) []Row {
	t.Helper()
	rows, err := f.Scan()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func seed(t *testing.T) *Flock {
	t.Helper()
	a, err := New("peer-a")
	if err != nil {
		t.Fatal(err)
	}
	puts := []struct {
		key   []any
		value any
	}{
		{[]any{"e", "session-s1"}, true},
		{[]any{"m", "session-s1"}, map[string]any{"id": "s1", "title": "Fix the tests", "status": map[string]any{"type": "idle"}, "n": 3.5}},
		{[]any{"m", "session-s1", "latestUserMsgId"}, "turn-1"},
		{[]any{"e", "machine-m1"}, true},
		{[]any{"m", "machine-m1"}, map[string]any{"name": "mac", "localProjects": map[string]any{"p1": map[string]any{"name": "lody"}}}},
		{[]any{"m", "session-s2"}, map[string]any{"title": "中文标题"}},
	}
	for _, p := range puts {
		if err := a.Put(p.key, p.value); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Delete([]any{"m", "session-s2"}); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestScanOrderAndValues(t *testing.T) {
	a := seed(t)
	rows := mustScan(t, a)
	var keys []string
	for _, r := range rows {
		b, _ := json.Marshal(r.Key)
		keys = append(keys, string(b))
	}
	want := []string{
		`["e","machine-m1"]`, `["e","session-s1"]`,
		`["m","machine-m1"]`, `["m","session-s1"]`, `["m","session-s1","latestUserMsgId"]`, `["m","session-s2"]`,
	}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	if rows[5].Live {
		t.Fatalf("deleted row is live: %+v", rows[5])
	}
	meta := rows[3].Value.(map[string]any)
	if meta["title"] != "Fix the tests" || meta["n"] != 3.5 || meta["status"].(map[string]any)["type"] != "idle" {
		t.Fatalf("meta = %#v", meta)
	}
	v, ok, err := a.Get([]any{"m", "session-s1", "latestUserMsgId"})
	if err != nil || !ok || v != "turn-1" {
		t.Fatalf("Get = %v %v %v", v, ok, err)
	}
}

func TestFileRoundTrip(t *testing.T) {
	a := seed(t)
	file, err := a.ExportFile()
	if err != nil {
		t.Fatal(err)
	}
	b, err := FromFile("peer-b", file)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mustScan(t, a), mustScan(t, b)) {
		t.Fatal("scan differs after file round trip")
	}
	c, _ := New("peer-c")
	if err := c.ImportFile(file); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mustScan(t, a), mustScan(t, c)) {
		t.Fatal("scan differs after ImportFile")
	}
}

func TestJSONDelta(t *testing.T) {
	a := seed(t)
	full, err := a.ExportJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := full["entries"].(map[string]any)
	rec := entries[`["m","session-s1"]`].(map[string]any)
	if c, _ := rec["c"].(string); !strings.HasSuffix(c, ",peer-a") || strings.Count(c, ",") != 2 {
		t.Fatalf("clock = %q", rec["c"])
	}
	if _, ok := entries[`["m","session-s2"]`].(map[string]any)["d"]; ok {
		t.Fatal("tombstone carries data")
	}

	// Through JSON text, as bundles travel over a stream.
	raw, _ := json.Marshal(full)
	var bundle map[string]any
	json.Unmarshal(raw, &bundle)
	b, _ := New("peer-b")
	if err := b.ImportJSON(bundle); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mustScan(t, a), mustScan(t, b)) {
		t.Fatal("scan differs after ImportJSON")
	}

	before, err := b.Version()
	if err != nil || len(before) == 0 {
		t.Fatalf("version = %v %v", before, err)
	}
	if err := b.Put([]any{"m", "session-s1", "title"}, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if err := b.Put([]any{"m", "session-s1", "latestUserMsgId"}, nil); err != nil {
		t.Fatal(err)
	}
	delta, err := b.ExportJSON(before)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(delta["entries"].(map[string]any)); got != 2 {
		t.Fatalf("delta has %d entries: %v", got, delta)
	}
	raw, _ = json.Marshal(delta)
	bundle = nil
	json.Unmarshal(raw, &bundle)
	if err := a.ImportJSON(bundle); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := a.Get([]any{"m", "session-s1", "title"}); v != "Renamed" {
		t.Fatalf("title = %v", v)
	}
	// A null put clears the key, as the runtime's set(key, undefined) does.
	if v, ok, _ := a.Get([]any{"m", "session-s1", "latestUserMsgId"}); ok {
		t.Fatalf("latestUserMsgId = %v, want cleared", v)
	}
	rows := mustScan(t, a)
	for _, r := range rows {
		if len(r.Key) == 3 && r.Key[2] == "latestUserMsgId" && r.Live {
			t.Fatalf("cleared row is live: %+v", r)
		}
	}
}

func TestBadFile(t *testing.T) {
	if _, err := FromFile("p", []byte("not a flock file")); err == nil {
		t.Fatal("expected an error")
	}
	// The engine must still work afterwards.
	if _, err := New("p"); err != nil {
		t.Fatal(err)
	}
}

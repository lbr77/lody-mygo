package collab

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestTextEncodings(t *testing.T) {
	var zipped bytes.Buffer
	w := gzip.NewWriter(&zipped)
	w.Write([]byte("package main\n"))
	w.Close()
	cases := []Text{
		{Encoding: "utf8-plain", Text: "package main\n"},
		{Encoding: "utf8-gzip-base64", Data: base64.StdEncoding.EncodeToString(zipped.Bytes())},
		{Encoding: "base64", Data: base64.StdEncoding.EncodeToString([]byte("package main\n"))},
	}
	for _, c := range cases {
		b, err := c.Bytes()
		if err != nil || string(b) != "package main\n" {
			t.Errorf("%s: %q, %v", c.Encoding, b, err)
		}
	}
}

func TestChanges(t *testing.T) {
	raw := `{"status":"ok","base":"abc123","truncated":false,"entries":[
		{"status":"ok","path":"b.go","oldSnapshot":{"kind":"missing"},"newSnapshot":{"kind":"text","text":{"encoding":"plain","text":"x\n","rawBytes":2}},"add":1,"del":0},
		{"status":"deferred","path":"a.go","add":3,"del":1}]}`
	var c Changes
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	e := c.Entries[0]
	if e.Kind() != "added" || *e.Add != 1 {
		t.Fatalf("entry = %+v", e)
	}
	old, ok := e.Old.String()
	if !ok || old != "" {
		t.Fatalf("old = %q %v", old, ok)
	}
	if s, ok := e.New.String(); !ok || s != "x\n" {
		t.Fatalf("new = %q %v", s, ok)
	}
	if _, ok := (&Snapshot{Kind: "binary"}).String(); ok {
		t.Fatal("binary has text")
	}
}

func TestSortEntries(t *testing.T) {
	e := []DirEntry{{"zeta.go", "file"}, {"Alpha", "directory"}, {"beta.go", "file"}, {"cmd", "directory"}}
	SortEntries(e)
	got := ""
	for _, x := range e {
		got += x.Name + " "
	}
	if got != "Alpha cmd beta.go zeta.go " {
		t.Fatal(got)
	}
}

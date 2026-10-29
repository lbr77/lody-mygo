// Package flock is a Go binding of flock-rs, the CRDT key-value store behind
// Lody's catalog streams, running its WebAssembly build in wazero.
//
// Values are JSON-like Go values: nil, bool, float64, string, []any and
// map[string]any.
package flock

import (
	"errors"
	"runtime"
)

// Flock is one replica. Its methods are safe from any goroutine.
type Flock struct {
	e   *engine
	ptr uint32
}

// Row is one entry of a scan. Value is set only when Live is true: rows of
// deleted keys are tombstones.
type Row struct {
	Key   []any
	Value any
	Live  bool
}

// Version is a version vector as flock-rs encodes it: peer id to
// [physicalTime, logicalCounter].
type Version map[string]any

// ErrBroken is returned by replicas whose module instance trapped.
var ErrBroken = errors.New("flock: replica lost after a trap; rebuild it")

func wrap(e *engine, ptr uint32) *Flock {
	f := &Flock{e: e, ptr: ptr}
	runtime.SetFinalizer(f, (*Flock).Free)
	return f
}

// New returns an empty replica writing as peerID.
func New(peerID string) (*Flock, error) {
	e, err := getEngine()
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	words, err := e.invoke("rawflock_new", func(retptr uint32) []uint64 {
		p, n := e.passString(peerID)
		return []uint64{uint64(retptr), uint64(p), uint64(n)}
	})
	if err != nil {
		return nil, err
	}
	if words[2] != 0 {
		return nil, thrownError(e.take(words[1]))
	}
	return wrap(e, words[0]), nil
}

// FromFile loads a replica from a binary export (exportFile).
func FromFile(peerID string, data []byte) (*Flock, error) {
	e, err := getEngine()
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	load := func(name string) ([4]uint32, error) {
		return e.invoke(name, func(retptr uint32) []uint64 {
			p, n := e.passString(peerID)
			b, bn := e.passBytes(data)
			return []uint64{uint64(retptr), uint64(p), uint64(n), uint64(b), uint64(bn)}
		})
	}
	words, err := load("rawflock_fromFile")
	if err != nil {
		return nil, err
	}
	if words[2] != 0 {
		e.drop(words[1])
		// flock-wasm falls back to recovery for files it can't load strictly.
		if words, err = load("rawflock_recoverFromFile"); err != nil {
			return nil, err
		}
		if words[2] != 0 {
			return nil, thrownError(e.take(words[1]))
		}
		obj, _ := e.take(words[0]).(*jsObject)
		raw, _ := obj.get("flock").(*jsRawFlock)
		if raw == nil {
			return nil, errors.New("flock: recovery returned no replica")
		}
		return wrap(e, raw.ptr), nil
	}
	return wrap(e, words[0]), nil
}

// Free releases the replica. It is called by the finalizer too.
func (f *Flock) Free() {
	if f.ptr == 0 {
		return
	}
	f.e.mu.Lock()
	defer f.e.mu.Unlock()
	if !f.e.broken {
		f.e.call(f.e.fn("__wbg_rawflock_free"), uint64(f.ptr), 0)
	}
	f.ptr = 0
	runtime.SetFinalizer(f, nil)
}

// do runs an export taking the replica and heap arguments, returning a value
// in word 0 when hasValue is set.
func (f *Flock) do(name string, hasValue bool, args ...any) (any, error) {
	f.e.mu.Lock()
	defer f.e.mu.Unlock()
	if f.e.broken || f.ptr == 0 {
		return nil, ErrBroken
	}
	words, err := f.e.invoke(name, func(retptr uint32) []uint64 {
		params := []uint64{uint64(retptr), uint64(f.ptr)}
		for _, a := range args {
			params = append(params, uint64(f.e.add(a)))
		}
		return params
	})
	if err != nil {
		return nil, err
	}
	if hasValue {
		return f.e.result(words, 0, 1, 2)
	}
	return f.e.result(words, -1, 0, 1)
}

// ImportFile merges a binary export into the replica.
func (f *Flock) ImportFile(data []byte) error {
	f.e.mu.Lock()
	defer f.e.mu.Unlock()
	if f.e.broken || f.ptr == 0 {
		return ErrBroken
	}
	run := func(name string) error {
		words, err := f.e.invoke(name, func(retptr uint32) []uint64 {
			b, n := f.e.passBytes(data)
			return []uint64{uint64(retptr), uint64(f.ptr), uint64(b), uint64(n)}
		})
		if err != nil {
			return err
		}
		_, err = f.e.result(words, -1, 0, 1)
		return err
	}
	if err := run("rawflock_importFile"); err != nil {
		var t *thrown
		if !errors.As(err, &t) {
			return err
		}
		return run("rawflock_recoverImportFile")
	}
	return nil
}

// ImportJSON merges an export bundle ({version, entries}) into the replica.
func (f *Flock) ImportJSON(bundle map[string]any) error {
	_, err := f.do("rawflock_importJson", false, toJS(bundle))
	return err
}

// ExportJSON returns the entries newer than from, or every entry when from
// is empty.
func (f *Flock) ExportJSON(from Version) (map[string]any, error) {
	var vv any = undefined
	if len(from) > 0 {
		vv = toJS(map[string]any(from))
	}
	v, err := f.do("rawflock_exportJson", true, vv, undefined, undefined)
	if err != nil {
		return nil, err
	}
	bundle, _ := fromJS(v).(map[string]any)
	return bundle, nil
}

// Version returns the replica's version vector, the baseline for ExportJSON.
func (f *Flock) Version() (Version, error) {
	v, err := f.do("rawflock_version", true)
	if err != nil {
		return nil, err
	}
	m, _ := fromJS(v).(map[string]any)
	return Version(m), nil
}

// Put writes value at key with the current time. A nil value clears the
// key, as flock-wasm's set(key, undefined) does.
func (f *Flock) Put(key []any, value any) error {
	_, err := f.do("rawflock_put", false, toJS(key), toJS(value), undefined)
	return err
}

// Delete writes a tombstone at key.
func (f *Flock) Delete(key []any) error {
	_, err := f.do("rawflock_delete", false, toJS(key), undefined)
	return err
}

// Get returns the value at key and whether it is set.
func (f *Flock) Get(key []any) (any, bool, error) {
	v, err := f.do("rawflock_get", true, toJS(key))
	if err != nil {
		return nil, false, err
	}
	if v == undefined {
		return nil, false, nil
	}
	return fromJS(v), true, nil
}

// Scan returns every row in key order, tombstones included.
func (f *Flock) Scan() ([]Row, error) {
	v, err := f.do("rawflock_scanValues", true, undefined, undefined, undefined)
	if err != nil {
		return nil, err
	}
	arr, _ := v.(*jsArray)
	if arr == nil {
		return nil, nil
	}
	rows := make([]Row, 0, len(arr.items))
	for _, item := range arr.items {
		obj, ok := item.(*jsObject)
		if !ok {
			continue
		}
		key, _ := fromJS(obj.get("key")).([]any)
		value := obj.get("value")
		rows = append(rows, Row{Key: key, Value: fromJS(value), Live: value != undefined})
	}
	return rows, nil
}

// ExportFile returns the replica in the binary format FromFile loads.
func (f *Flock) ExportFile() ([]byte, error) {
	f.e.mu.Lock()
	defer f.e.mu.Unlock()
	if f.e.broken || f.ptr == 0 {
		return nil, ErrBroken
	}
	words, err := f.e.invoke("rawflock_exportFile", func(retptr uint32) []uint64 {
		return []uint64{uint64(retptr), uint64(f.ptr)}
	})
	if err != nil {
		return nil, err
	}
	if words[3] != 0 {
		return nil, thrownError(f.e.take(words[2]))
	}
	data := f.e.readBytes(words[0], words[1])
	f.e.call(f.e.free, uint64(words[0]), uint64(words[1]), 1)
	return data, nil
}

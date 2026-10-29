package flock

import (
	"fmt"
	"math"
	"sort"
	"strconv"
)

// The wasm module was built by wasm-bindgen for a JavaScript host. These types
// stand in for the JavaScript values it creates and reads through its imports.

type jsUndefined struct{}

var undefined = jsUndefined{}

type jsObject struct {
	keys []string
	vals map[string]any
}

func newObject() *jsObject { return &jsObject{vals: map[string]any{}} }

func (o *jsObject) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *jsObject) get(k string) any {
	if v, ok := o.vals[k]; ok {
		return v
	}
	return undefined
}

type jsArray struct{ items []any }

func (a *jsArray) set(i int, v any) {
	for len(a.items) <= i {
		a.items = append(a.items, undefined)
	}
	a.items[i] = v
}

type jsMap struct {
	keys []any
	vals []any
}

func (m *jsMap) set(k, v any) {
	for i, key := range m.keys {
		if strictEqual(key, k) {
			m.vals[i] = v
			return
		}
	}
	m.keys = append(m.keys, k)
	m.vals = append(m.vals, v)
}

// jsBytes is a Uint8Array that owns its bytes.
type jsBytes struct{ data []byte }

// jsMemoryView is a Uint8Array over the module's memory.
type jsMemoryView struct{ offset, length uint32 }

type jsMemory struct{}
type jsArrayBuffer struct{}

type jsError struct{ message string }

func (e *jsError) Error() string { return e.message }

type jsBigInt struct{ v int64 }

type jsSymbol struct{ name string }

var symbolIterator = &jsSymbol{name: "Symbol.iterator"}

type jsFunc func(this any, args ...any) any

type jsIterator struct {
	items []any
	i     int
}

type jsRawFlock struct{ ptr uint32 }

type jsClosure struct{ a, b uint32 }

func typeOf(v any) string {
	switch v.(type) {
	case jsUndefined:
		return "undefined"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case *jsBigInt:
		return "bigint"
	case *jsSymbol:
		return "symbol"
	case jsFunc:
		return "function"
	}
	return "object"
}

func strictEqual(a, b any) bool {
	switch x := a.(type) {
	case nil, jsUndefined, bool, float64, string:
		return a == b
	case *jsBigInt:
		y, ok := b.(*jsBigInt)
		return ok && x.v == y.v
	}
	// Reference types compare by identity.
	return fmt.Sprintf("%p", a) == fmt.Sprintf("%p", b)
}

func looseEqual(a, b any) bool {
	if (a == nil || a == undefined) && (b == nil || b == undefined) {
		return true
	}
	return strictEqual(a, b)
}

func jsString(v any) string {
	switch x := v.(type) {
	case jsUndefined:
		return "undefined"
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return formatNumber(x)
	case string:
		return x
	case *jsError:
		return "Error: " + x.message
	case *jsBigInt:
		return strconv.FormatInt(x.v, 10)
	case *jsArray:
		s := ""
		for i, item := range x.items {
			if i > 0 {
				s += ","
			}
			if item != nil && item != undefined {
				s += jsString(item)
			}
		}
		return s
	}
	return "[object Object]"
}

func formatNumber(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// propertyKey converts a JavaScript property key to the string used by
// objects and arrays.
func propertyKey(k any) string {
	if s, ok := k.(string); ok {
		return s
	}
	return jsString(k)
}

// toJS converts a JSON-like Go value to its JavaScript counterpart.
func toJS(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case jsUndefined, bool, float64, string:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint32:
		return float64(x)
	case []byte:
		return &jsBytes{data: append([]byte(nil), x...)}
	case []any:
		a := &jsArray{items: make([]any, len(x))}
		for i, item := range x {
			a.items[i] = toJS(item)
		}
		return a
	case []string:
		a := &jsArray{items: make([]any, len(x))}
		for i, item := range x {
			a.items[i] = item
		}
		return a
	case map[string]any:
		o := newObject()
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			o.set(k, toJS(x[k]))
		}
		return o
	}
	panic(fmt.Sprintf("flock: cannot pass %T to wasm", v))
}

// fromJS converts a JavaScript value to JSON-like Go values. Object
// properties that are undefined are left out, as JSON.stringify does.
func fromJS(v any) any {
	switch x := v.(type) {
	case jsUndefined:
		return nil
	case nil, bool, float64, string:
		return x
	case *jsBigInt:
		return float64(x.v)
	case *jsArray:
		out := make([]any, len(x.items))
		for i, item := range x.items {
			out[i] = fromJS(item)
		}
		return out
	case *jsObject:
		out := make(map[string]any, len(x.keys))
		for _, k := range x.keys {
			if x.vals[k] == undefined {
				continue
			}
			out[k] = fromJS(x.vals[k])
		}
		return out
	case *jsMap:
		out := make(map[string]any, len(x.keys))
		for i, k := range x.keys {
			out[propertyKey(k)] = fromJS(x.vals[i])
		}
		return out
	case *jsBytes:
		return append([]byte(nil), x.data...)
	}
	return nil
}

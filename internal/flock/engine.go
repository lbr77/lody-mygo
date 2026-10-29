package flock

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// flock.wasm is flock_wasm_bg.wasm from @loro-dev/flock-wasm 0.4.3 (the
// bundler build), the only published build of flock-rs. It runs in wazero, a
// WebAssembly runtime written in Go, behind the imports wasm-bindgen expects
// from JavaScript.
//
//go:embed wasm/flock.wasm
var wasmBytes []byte

const heapReserved = 132 // wasm-bindgen keeps 128 slots plus undefined, null, true, false

type freeSlot int

// engine is one instance of the module. Calls into it are serialized.
type engine struct {
	mu   sync.Mutex
	ctx  context.Context
	mod  api.Module
	heap []any
	next int

	addToStack, malloc, realloc, free, exnStore api.Function
	fns                                         map[string]api.Function

	// broken is set after a trap: the module may hold half-updated state.
	broken bool
}

var (
	shared     *engine
	sharedErr  error
	sharedOnce sync.Once
	sharedMu   sync.Mutex
)

// getEngine returns the shared instance, replacing it after a trap. Replicas
// made in the old instance keep failing and are rebuilt by their owners.
func getEngine() (*engine, error) {
	sharedOnce.Do(func() { shared, sharedErr = newEngine(context.Background()) })
	if sharedErr == nil && shared.isBroken() {
		e, err := newEngine(context.Background())
		if err != nil {
			return nil, err
		}
		sharedMu.Lock()
		shared = e
		sharedMu.Unlock()
	}
	sharedMu.Lock()
	defer sharedMu.Unlock()
	return shared, sharedErr
}

func (e *engine) isBroken() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.broken
}

func newEngine(ctx context.Context) (*engine, error) {
	config := wazero.NewRuntimeConfig()
	if dir, err := os.UserCacheDir(); err == nil {
		if cache, err := wazero.NewCompilationCacheWithDir(filepath.Join(dir, "lody-mygo", "wazero")); err == nil {
			config = config.WithCompilationCache(cache)
		}
	}
	rt := wazero.NewRuntimeWithConfig(ctx, config)
	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("flock: compile: %w", err)
	}
	e := &engine{ctx: ctx, fns: map[string]api.Function{}}
	e.heap = make([]any, 128, 256)
	for i := range e.heap {
		e.heap[i] = undefined
	}
	e.heap = append(e.heap, undefined, nil, true, false)
	e.next = len(e.heap)

	host := map[string]wazero.HostModuleBuilder{}
	for _, def := range compiled.ImportedFunctions() {
		module, name, _ := def.Import()
		impl, ok := e.imports()[name]
		if !ok {
			return nil, fmt.Errorf("flock: no host function for %s", name)
		}
		b := host[module]
		if b == nil {
			b = rt.NewHostModuleBuilder(module)
			host[module] = b
		}
		b.NewFunctionBuilder().WithGoModuleFunction(impl, def.ParamTypes(), def.ResultTypes()).Export(name)
	}
	for _, b := range host {
		if _, err := b.Instantiate(ctx); err != nil {
			return nil, fmt.Errorf("flock: host module: %w", err)
		}
	}
	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName("flock").WithStartFunctions())
	if err != nil {
		return nil, fmt.Errorf("flock: instantiate: %w", err)
	}
	e.mod = mod
	e.addToStack = mod.ExportedFunction("__wbindgen_add_to_stack_pointer")
	e.malloc = mod.ExportedFunction("__wbindgen_malloc")
	e.realloc = mod.ExportedFunction("__wbindgen_realloc")
	e.free = mod.ExportedFunction("__wbindgen_free")
	e.exnStore = mod.ExportedFunction("__wbindgen_exn_store")
	if start := mod.ExportedFunction("__wbindgen_start"); start != nil {
		if _, err := start.Call(ctx); err != nil {
			return nil, fmt.Errorf("flock: start: %w", err)
		}
	}
	return e, nil
}

// jsThrow is panicked by imports that throw, which unwinds the wasm call.
type jsThrow struct{ value any }

func (t jsThrow) Error() string { return jsString(t.value) }

func (e *engine) fn(name string) api.Function {
	f, ok := e.fns[name]
	if !ok {
		f = e.mod.ExportedFunction(name)
		e.fns[name] = f
	}
	return f
}

func (e *engine) add(v any) uint32 {
	if e.next == len(e.heap) {
		e.heap = append(e.heap, freeSlot(len(e.heap)+1))
	}
	idx := e.next
	next, _ := e.heap[idx].(freeSlot)
	e.next = int(next)
	e.heap[idx] = v
	return uint32(idx)
}

func (e *engine) get(idx uint32) any { return e.heap[idx] }

func (e *engine) drop(idx uint32) {
	if idx < heapReserved {
		return
	}
	e.heap[idx] = freeSlot(e.next)
	e.next = int(idx)
}

func (e *engine) take(idx uint32) any {
	v := e.get(idx)
	e.drop(idx)
	return v
}

func (e *engine) mem() api.Memory { return e.mod.Memory() }

func (e *engine) readString(ptr, n uint32) string {
	b, _ := e.mem().Read(ptr, n)
	return string(b)
}

func (e *engine) readBytes(ptr, n uint32) []byte {
	b, _ := e.mem().Read(ptr, n)
	return append([]byte(nil), b...)
}

func (e *engine) call(f api.Function, params ...uint64) []uint64 {
	out, err := f.Call(e.ctx, params...)
	if err != nil {
		panic(err)
	}
	return out
}

func (e *engine) passBytes(b []byte) (uint32, uint32) {
	ptr := uint32(e.call(e.malloc, uint64(len(b)), 1)[0])
	e.mem().Write(ptr, b)
	return ptr, uint32(len(b))
}

func (e *engine) passString(s string) (uint32, uint32) {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	return e.passBytes([]byte(s))
}

func (e *engine) i32(ptr uint32) uint32 {
	v, _ := e.mem().ReadUint32Le(ptr)
	return v
}

func (e *engine) writeI32(ptr, v uint32) { e.mem().WriteUint32Le(ptr, v) }

// invoke runs an export that returns through a 16-byte area on the shadow
// stack, as wasm-bindgen's generated JavaScript does, and returns the four
// words written there. Errors from traps and thrown exceptions are returned.
func (e *engine) invoke(name string, args func(retptr uint32) []uint64) (words [4]uint32, err error) {
	retptr := uint32(e.call(e.addToStack, api.EncodeI32(-16))[0])
	defer func() {
		if r := recover(); r != nil {
			err = recovered(r)
		}
		e.call(e.addToStack, api.EncodeI32(16))
	}()
	params := args(retptr)
	if _, callErr := e.fn(name).Call(e.ctx, params...); callErr != nil {
		err = unwrapThrow(callErr)
		var t *trap
		if errors.As(err, &t) {
			e.broken = true
		}
		return words, err
	}
	for i := range words {
		words[i] = e.i32(retptr + uint32(4*i))
	}
	return words, nil
}

func recovered(r any) error {
	if err, ok := r.(error); ok {
		return unwrapThrow(err)
	}
	return fmt.Errorf("flock: %v", r)
}

func unwrapThrow(err error) error {
	var t *thrown
	if errors.As(err, &t) {
		return t
	}
	var j jsThrow
	if errors.As(err, &j) {
		return thrownError(j.value)
	}
	return &trap{err: err}
}

// trap is a failure inside the module, after which its state can't be trusted.
type trap struct{ err error }

func (t *trap) Error() string { return "flock: " + t.err.Error() }
func (t *trap) Unwrap() error { return t.err }

type thrown struct{ msg string }

func (t *thrown) Error() string { return "flock: " + t.msg }

func thrownError(v any) error {
	if e, ok := v.(*jsError); ok {
		return &thrown{msg: e.message}
	}
	return &thrown{msg: jsString(v)}
}

// result decodes the common {value, error, isError} return layout.
func (e *engine) result(words [4]uint32, valueWord, errWord, flagWord int) (any, error) {
	if words[flagWord] != 0 {
		return nil, thrownError(e.take(words[errWord]))
	}
	if valueWord < 0 {
		return nil, nil
	}
	return e.take(words[valueWord]), nil
}

func (e *engine) imports() map[string]api.GoModuleFunc {
	u32 := func(v uint64) uint32 { return uint32(v) }
	b2u := func(b bool) uint64 {
		if b {
			return 1
		}
		return 0
	}
	writeString := func(retptr uint32, s string) {
		ptr, n := e.passString(s)
		e.writeI32(retptr+4, n)
		e.writeI32(retptr, ptr)
	}
	// handle runs fn and, when it throws, stores the exception in the module
	// the way wasm-bindgen's handleError does.
	handle := func(fn func(stack []uint64)) api.GoModuleFunc {
		return func(ctx context.Context, m api.Module, stack []uint64) {
			defer func() {
				if r := recover(); r != nil {
					var value any = &jsError{message: fmt.Sprint(r)}
					if t, ok := r.(jsThrow); ok {
						value = t.value
					}
					e.call(e.exnStore, uint64(e.add(value)))
				}
			}()
			fn(stack)
		}
	}
	simple := func(fn func(stack []uint64)) api.GoModuleFunc {
		return func(ctx context.Context, m api.Module, stack []uint64) { fn(stack) }
	}
	return map[string]api.GoModuleFunc{
		"__wbg_String_8f0eb39a4a4c2f66": simple(func(s []uint64) {
			writeString(u32(s[0]), jsString(e.get(u32(s[1]))))
		}),
		"__wbg_buffer_609cc3eee51ed158": simple(func(s []uint64) {
			s[0] = uint64(e.add(jsArrayBuffer{}))
		}),
		"__wbg_call_672a4d21634d4a24": handle(func(s []uint64) {
			f := e.get(u32(s[0])).(jsFunc)
			s[0] = uint64(e.add(f(e.get(u32(s[1])))))
		}),
		"__wbg_call_7cccdd69e0791ae2": handle(func(s []uint64) {
			f := e.get(u32(s[0])).(jsFunc)
			s[0] = uint64(e.add(f(e.get(u32(s[1])), e.get(u32(s[2])))))
		}),
		"__wbg_done_769e5ede4b31c67b": simple(func(s []uint64) {
			done, _ := getProperty(e.get(u32(s[0])), "done").(bool)
			s[0] = b2u(done)
		}),
		"__wbg_entries_3265d4158b33e5dc": simple(func(s []uint64) {
			s[0] = uint64(e.add(objectEntries(e.get(u32(s[0])))))
		}),
		"__wbg_error_7534b8e9a36f1ab4": simple(func(s []uint64) {
			msg := e.readString(u32(s[0]), u32(s[1]))
			e.call(e.free, s[0], s[1], 1)
			fmt.Fprintln(os.Stderr, "flock:", msg)
		}),
		"__wbg_get_67b2ba62fc30de12": handle(func(s []uint64) {
			s[0] = uint64(e.add(getProperty(e.get(u32(s[0])), e.get(u32(s[1])))))
		}),
		"__wbg_get_b9b93047fe3cf45b": simple(func(s []uint64) {
			s[0] = uint64(e.add(getProperty(e.get(u32(s[0])), float64(u32(s[1])))))
		}),
		"__wbg_getwithrefkey_1dc361bd10053bfe": simple(func(s []uint64) {
			s[0] = uint64(e.add(getProperty(e.get(u32(s[0])), e.get(u32(s[1])))))
		}),
		"__wbg_instanceof_ArrayBuffer_e14585432e3737fc": simple(func(s []uint64) {
			_, ok := e.get(u32(s[0])).(jsArrayBuffer)
			s[0] = b2u(ok)
		}),
		"__wbg_instanceof_Map_f3469ce2244d2430": simple(func(s []uint64) {
			_, ok := e.get(u32(s[0])).(*jsMap)
			s[0] = b2u(ok)
		}),
		"__wbg_instanceof_Uint8Array_17156bcf118086a9": simple(func(s []uint64) {
			switch e.get(u32(s[0])).(type) {
			case *jsBytes, *jsMemoryView:
				s[0] = 1
			default:
				s[0] = 0
			}
		}),
		"__wbg_isArray_a1eab7e0d067391b": simple(func(s []uint64) {
			_, ok := e.get(u32(s[0])).(*jsArray)
			s[0] = b2u(ok)
		}),
		"__wbg_isSafeInteger_343e2beeeece1bb0": simple(func(s []uint64) {
			f, ok := e.get(u32(s[0])).(float64)
			s[0] = b2u(ok && f == math.Trunc(f) && math.Abs(f) <= 1<<53-1)
		}),
		"__wbg_iterator_9a24c88df860dc65": simple(func(s []uint64) {
			s[0] = uint64(e.add(symbolIterator))
		}),
		"__wbg_length_a446193dc22c12f8": simple(func(s []uint64) {
			s[0] = uint64(lengthOf(e.get(u32(s[0]))))
		}),
		"__wbg_length_e2d2a49132c1b256": simple(func(s []uint64) {
			s[0] = uint64(lengthOf(e.get(u32(s[0]))))
		}),
		"__wbg_new_405e22f390576ce2": simple(func(s []uint64) { s[0] = uint64(e.add(newObject())) }),
		"__wbg_new_5e0be73521bc8c17": simple(func(s []uint64) { s[0] = uint64(e.add(&jsMap{})) }),
		"__wbg_new_78feb108b6472713": simple(func(s []uint64) { s[0] = uint64(e.add(&jsArray{})) }),
		"__wbg_new_8a6f238a6ece86ea": simple(func(s []uint64) { s[0] = uint64(e.add(&jsError{})) }),
		"__wbg_new_a12002a7f91c75be": simple(func(s []uint64) {
			// new Uint8Array(x): a view of the whole memory, or a copy.
			switch x := e.get(u32(s[0])).(type) {
			case jsArrayBuffer:
				s[0] = uint64(e.add(&jsMemoryView{offset: 0, length: e.mem().Size()}))
			case *jsMemoryView:
				s[0] = uint64(e.add(&jsBytes{data: e.readBytes(x.offset, x.length)}))
			case *jsBytes:
				s[0] = uint64(e.add(&jsBytes{data: append([]byte(nil), x.data...)}))
			default:
				s[0] = uint64(e.add(&jsBytes{}))
			}
		}),
		"__wbg_new_c68d7209be747379": simple(func(s []uint64) {
			s[0] = uint64(e.add(&jsError{message: e.readString(u32(s[0]), u32(s[1]))}))
		}),
		"__wbg_newwithbyteoffsetandlength_d97e637ebe145a9a": simple(func(s []uint64) {
			s[0] = uint64(e.add(&jsMemoryView{offset: u32(s[1]), length: u32(s[2])}))
		}),
		"__wbg_next_25feadfc0913fea9": simple(func(s []uint64) {
			s[0] = uint64(e.add(getProperty(e.get(u32(s[0])), "next")))
		}),
		"__wbg_next_6574e1a8a62d1055": handle(func(s []uint64) {
			s[0] = uint64(e.add(iteratorNext(e.get(u32(s[0])))))
		}),
		"__wbg_now_807e54c39636c349": simple(func(s []uint64) {
			s[0] = api.EncodeF64(float64(time.Now().UnixMilli()))
		}),
		"__wbg_rawflock_new": simple(func(s []uint64) {
			s[0] = uint64(e.add(&jsRawFlock{ptr: u32(s[0])}))
		}),
		"__wbg_resolve_4851785c9c5f573d": simple(func(s []uint64) {
			s[0] = uint64(e.add(e.get(u32(s[0]))))
		}),
		"__wbg_set_37837023f3d740e8": simple(func(s []uint64) {
			arr := e.get(u32(s[0])).(*jsArray)
			arr.set(int(u32(s[1])), e.take(u32(s[2])))
		}),
		"__wbg_set_3f1d0b984ed272ed": simple(func(s []uint64) {
			setProperty(e.get(u32(s[0])), e.take(u32(s[1])), e.take(u32(s[2])))
		}),
		"__wbg_set_65595bdd868b3009": simple(func(s []uint64) {
			// target.set(source, offset): copies bytes into the memory.
			target := e.get(u32(s[0]))
			src := e.get(u32(s[1]))
			offset := u32(s[2])
			var data []byte
			switch x := src.(type) {
			case *jsBytes:
				data = x.data
			case *jsMemoryView:
				data = e.readBytes(x.offset, x.length)
			}
			switch t := target.(type) {
			case *jsMemoryView:
				e.mem().Write(t.offset+offset, data)
			case *jsBytes:
				copy(t.data[offset:], data)
			}
		}),
		"__wbg_set_8fc6bf8a5b1071d1": simple(func(s []uint64) {
			m := e.get(u32(s[0])).(*jsMap)
			m.set(e.get(u32(s[1])), e.get(u32(s[2])))
			s[0] = uint64(e.add(m))
		}),
		"__wbg_set_bb8cecf6a62b9f46": handle(func(s []uint64) {
			setProperty(e.get(u32(s[0])), e.get(u32(s[1])), e.get(u32(s[2])))
			s[0] = 1
		}),
		"__wbg_stack_0ed75d68575b0f3c": simple(func(s []uint64) {
			writeString(u32(s[0]), "")
		}),
		"__wbg_then_44b73946d2fb3e7d": simple(func(s []uint64) {
			s[0] = uint64(e.add(e.get(u32(s[0]))))
		}),
		"__wbg_value_cd1ffa7b1ab794f1": simple(func(s []uint64) {
			s[0] = uint64(e.add(getProperty(e.get(u32(s[0])), "value")))
		}),
		"__wbg_warn_cdcc993122790095": simple(func(s []uint64) {
			fmt.Fprintln(os.Stderr, "flock:", jsString(e.get(u32(s[0]))), jsString(e.get(u32(s[1]))))
		}),
		"__wbindgen_as_number": simple(func(s []uint64) {
			s[0] = api.EncodeF64(toNumber(e.get(u32(s[0]))))
		}),
		"__wbindgen_bigint_from_i64": simple(func(s []uint64) {
			s[0] = uint64(e.add(&jsBigInt{v: int64(s[0])}))
		}),
		"__wbindgen_bigint_from_u64": simple(func(s []uint64) {
			s[0] = uint64(e.add(&jsBigInt{v: int64(s[0])}))
		}),
		"__wbindgen_bigint_get_as_i64": simple(func(s []uint64) {
			retptr := u32(s[0])
			b, ok := e.get(u32(s[1])).(*jsBigInt)
			var v int64
			if ok {
				v = b.v
			}
			e.mem().WriteUint64Le(retptr+8, uint64(v))
			e.writeI32(retptr, uint32(b2u(ok)))
		}),
		"__wbindgen_boolean_get": simple(func(s []uint64) {
			switch b := e.get(u32(s[0])).(type) {
			case bool:
				s[0] = b2u(b)
			default:
				s[0] = 2
			}
		}),
		"__wbindgen_cb_drop": simple(func(s []uint64) {
			e.take(u32(s[0]))
			s[0] = 1
		}),
		"__wbindgen_closure_wrapper271": simple(func(s []uint64) {
			s[0] = uint64(e.add(&jsClosure{a: u32(s[0]), b: u32(s[1])}))
		}),
		"__wbindgen_debug_string": simple(func(s []uint64) {
			writeString(u32(s[0]), jsString(e.get(u32(s[1]))))
		}),
		"__wbindgen_error_new": simple(func(s []uint64) {
			s[0] = uint64(e.add(&jsError{message: e.readString(u32(s[0]), u32(s[1]))}))
		}),
		"__wbindgen_in": simple(func(s []uint64) {
			s[0] = b2u(hasProperty(e.get(u32(s[1])), e.get(u32(s[0]))))
		}),
		"__wbindgen_is_bigint":   simple(func(s []uint64) { s[0] = b2u(typeOf(e.get(u32(s[0]))) == "bigint") }),
		"__wbindgen_is_function": simple(func(s []uint64) { s[0] = b2u(typeOf(e.get(u32(s[0]))) == "function") }),
		"__wbindgen_is_null":     simple(func(s []uint64) { s[0] = b2u(e.get(u32(s[0])) == nil) }),
		"__wbindgen_is_object": simple(func(s []uint64) {
			v := e.get(u32(s[0]))
			s[0] = b2u(v != nil && typeOf(v) == "object")
		}),
		"__wbindgen_is_string":    simple(func(s []uint64) { s[0] = b2u(typeOf(e.get(u32(s[0]))) == "string") }),
		"__wbindgen_is_undefined": simple(func(s []uint64) { s[0] = b2u(e.get(u32(s[0])) == undefined) }),
		"__wbindgen_jsval_eq": simple(func(s []uint64) {
			s[0] = b2u(strictEqual(e.get(u32(s[0])), e.get(u32(s[1]))))
		}),
		"__wbindgen_jsval_loose_eq": simple(func(s []uint64) {
			s[0] = b2u(looseEqual(e.get(u32(s[0])), e.get(u32(s[1]))))
		}),
		"__wbindgen_memory": simple(func(s []uint64) { s[0] = uint64(e.add(jsMemory{})) }),
		"__wbindgen_number_get": simple(func(s []uint64) {
			retptr := u32(s[0])
			f, ok := e.get(u32(s[1])).(float64)
			e.mem().WriteUint64Le(retptr+8, math.Float64bits(f))
			e.writeI32(retptr, uint32(b2u(ok)))
		}),
		"__wbindgen_number_new": simple(func(s []uint64) {
			s[0] = uint64(e.add(api.DecodeF64(s[0])))
		}),
		"__wbindgen_object_clone_ref": simple(func(s []uint64) {
			s[0] = uint64(e.add(e.get(u32(s[0]))))
		}),
		"__wbindgen_object_drop_ref": simple(func(s []uint64) { e.drop(u32(s[0])) }),
		"__wbindgen_rethrow": simple(func(s []uint64) {
			panic(&thrown{msg: jsString(e.take(u32(s[0])))})
		}),
		"__wbindgen_string_get": simple(func(s []uint64) {
			retptr := u32(s[0])
			str, ok := e.get(u32(s[1])).(string)
			if !ok {
				e.writeI32(retptr+4, 0)
				e.writeI32(retptr, 0)
				return
			}
			writeString(retptr, str)
		}),
		"__wbindgen_string_new": simple(func(s []uint64) {
			s[0] = uint64(e.add(e.readString(u32(s[0]), u32(s[1]))))
		}),
		"__wbindgen_throw": simple(func(s []uint64) {
			panic(&thrown{msg: e.readString(u32(s[0]), u32(s[1]))})
		}),
	}
}

func lengthOf(v any) uint32 {
	switch x := v.(type) {
	case *jsArray:
		return uint32(len(x.items))
	case *jsBytes:
		return uint32(len(x.data))
	case *jsMemoryView:
		return x.length
	case string:
		return uint32(len(utf16Units(x)))
	}
	return 0
}

func utf16Units(s string) []rune { return []rune(s) }

func toNumber(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case bool:
		if x {
			return 1
		}
		return 0
	case nil:
		return 0
	case *jsBigInt:
		return float64(x.v)
	}
	return math.NaN()
}

func objectEntries(v any) *jsArray {
	out := &jsArray{}
	switch x := v.(type) {
	case *jsObject:
		for _, k := range x.keys {
			out.items = append(out.items, &jsArray{items: []any{k, x.vals[k]}})
		}
	case *jsArray:
		for i, item := range x.items {
			out.items = append(out.items, &jsArray{items: []any{formatNumber(float64(i)), item}})
		}
	}
	return out
}

func getProperty(target any, key any) any {
	if key == symbolIterator {
		return iteratorFunc(target)
	}
	k := propertyKey(key)
	switch x := target.(type) {
	case *jsObject:
		return x.get(k)
	case *jsArray:
		if k == "length" {
			return float64(len(x.items))
		}
		if f, ok := key.(float64); ok && int(f) >= 0 && int(f) < len(x.items) {
			return x.items[int(f)]
		}
	case *jsBytes:
		if k == "length" {
			return float64(len(x.data))
		}
		if f, ok := key.(float64); ok && int(f) >= 0 && int(f) < len(x.data) {
			return float64(x.data[int(f)])
		}
	case *jsIterator:
		if k == "next" {
			return jsFunc(func(this any, args ...any) any { return iteratorNext(this) })
		}
	case *jsError:
		if k == "message" {
			return x.message
		}
	case *jsRawFlock:
		if k == "__wbg_ptr" {
			return float64(x.ptr)
		}
	case jsUndefined, nil:
		panic(jsThrow{value: &jsError{message: "cannot read properties of " + jsString(target)}})
	}
	return undefined
}

func hasProperty(target any, key any) bool {
	k := propertyKey(key)
	switch x := target.(type) {
	case *jsObject:
		_, ok := x.vals[k]
		return ok
	case *jsArray:
		if k == "length" {
			return true
		}
		if f, ok := key.(float64); ok {
			return int(f) >= 0 && int(f) < len(x.items)
		}
	}
	return false
}

func setProperty(target any, key any, value any) {
	switch x := target.(type) {
	case *jsObject:
		x.set(propertyKey(key), value)
	case *jsArray:
		if f, ok := key.(float64); ok {
			x.set(int(f), value)
		}
	}
}

func iteratorFunc(target any) any {
	var items []any
	switch x := target.(type) {
	case *jsArray:
		items = x.items
	case *jsMap:
		for i, k := range x.keys {
			items = append(items, &jsArray{items: []any{k, x.vals[i]}})
		}
	case *jsBytes:
		for _, b := range x.data {
			items = append(items, float64(b))
		}
	case string:
		for _, r := range x {
			items = append(items, string(r))
		}
	default:
		return undefined
	}
	return jsFunc(func(this any, args ...any) any { return &jsIterator{items: items} })
}

func iteratorNext(v any) any {
	it, ok := v.(*jsIterator)
	result := newObject()
	if !ok || it.i >= len(it.items) {
		result.set("done", true)
		result.set("value", undefined)
		return result
	}
	result.set("done", false)
	result.set("value", it.items[it.i])
	it.i++
	return result
}

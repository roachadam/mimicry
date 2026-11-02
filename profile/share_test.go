package profile

import (
	"reflect"
	"runtime"
	"slices"
	"testing"
)

// fingerprint, equal and copySpec panic on a kind they cannot handle, and
// fingerprint never ends on a cyclic value: a field of such a type added to
// a profile would crash With for every caller whose profile sets it, which
// the built-in profiles, leaving many pointers nil, may never reach. Every
// type a profile holds must be one they handle, its maps keyed by strings
// or integers, and no type may contain itself.
func TestFingerprintCoversProfile(t *testing.T) {
	var check func(reflect.Type, []reflect.Type)

	check = func(typ reflect.Type, path []reflect.Type) {
		if slices.Contains(path, typ) {
			t.Errorf("%v contains itself", typ)

			return
		}

		path = append(path, typ)

		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			check(typ.Elem(), path)
		case reflect.Map:
			switch typ.Key().Kind() {
			case reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			default:
				t.Errorf("%v is keyed by a %v", typ, typ.Key().Kind())
			}

			check(typ.Elem(), path)

		case reflect.Struct:
			for f := range typ.Fields() {
				check(f.Type, path)
			}

		case reflect.Interface, reflect.Chan, reflect.Func, reflect.UnsafePointer, reflect.Complex64, reflect.Complex128:
			t.Errorf("a profile holds a %v (%v)", typ.Kind(), path)
		default:
		}
	}

	check(reflect.TypeFor[Spec](), nil)
}

// A shared copy's entries go with it once nothing holds it, so that a
// process editing many profiles does not keep every one.
func TestSharedCopiesGo(t *testing.T) {
	p, err := Parse(readValid(t))
	if err != nil {
		t.Fatal(err)
	}

	edited := p.With(func(s *Spec) {
		s.Headers[0].Value = "changed"
	})
	s := edited.Spec()
	key := fingerprint(s)

	if load(&byContent, key) != edited.c || load(&lastByName, s.Name) != edited.c {
		t.Fatal("an edited profile's copy is not shared")
	}

	for range 100 {
		runtime.GC()
		runtime.Gosched()

		_, byKey := byContent.Load(key)
		_, byName := lastByName.Load(s.Name)

		if !byKey && !byName {
			return
		}
	}

	t.Error("a shared copy's entries outlived it")
}

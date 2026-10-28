package profiles

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/roachadam/mimicry/profile"
)

// Every accessor returns a fresh copy of one profile decoded once per name:
// each copy must be the embedded file exactly, and a caller changing
// everything in its copy must not reach the next caller's.
func TestCopiesShareNothing(t *testing.T) {
	for _, name := range Names() {
		b, err := data.ReadFile("data/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}

		file, err := profile.Parse(b)
		if err != nil {
			t.Fatal(err)
		}

		if p := load(name); !reflect.DeepEqual(p, file) {
			t.Errorf("%s: the copy differs from the embedded file", name)
		}

		want := encode(t, load(name))

		poison(reflect.ValueOf(load(name)).Elem())

		if got := encode(t, load(name)); !bytes.Equal(got, want) {
			t.Errorf("%s: changing one copy changed the next", name)
		}
	}
}

func encode(t *testing.T, p *profile.Profile) []byte {
	t.Helper()

	b, err := profile.Encode(p)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

// poison changes every value reachable from v.
func poison(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			poison(v.Elem())
		}

	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			poison(v.Index(i))
		}

	case reflect.Map:
		for it := v.MapRange(); it.Next(); {
			e := reflect.New(v.Type().Elem()).Elem()
			e.Set(it.Value())
			poison(e)
			v.SetMapIndex(it.Key(), e)
		}

	case reflect.Struct:
		for _, f := range v.Fields() {
			poison(f)
		}

	case reflect.String:
		v.SetString(v.String() + "x")
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(v.Int() + 1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(v.Uint() + 1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(v.Float() + 1)
	default:
		panic("poison: unexpected " + v.Kind().String())
	}
}

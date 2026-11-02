package profiles

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/roachadam/mimicry/profile"
)

// Every accessor returns one profile, parsed once per name, which must be
// the embedded file exactly; each Spec of it is a copy, and a caller
// changing everything in one must reach neither the profile nor the next
// copy.
func TestSpecsShareNothing(t *testing.T) {
	for _, name := range Names() {
		b, err := data.ReadFile("data/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}

		file, err := profile.Decode(b)
		if err != nil {
			t.Fatal(err)
		}

		p := load(name)
		if p != load(name) {
			t.Errorf("%s: the accessor built its profile again", name)
		}

		if !reflect.DeepEqual(p.Spec(), file) {
			t.Errorf("%s: the profile differs from the embedded file", name)
		}

		want := encode(t, p.Spec())

		poison(reflect.ValueOf(p.Spec()).Elem())

		if got := encode(t, p.Spec()); !bytes.Equal(got, want) {
			t.Errorf("%s: changing one copy changed the profile", name)
		}
	}
}

func encode(t *testing.T, p *profile.Spec) []byte {
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

package aipipeline

import (
	"reflect"
	"testing"
)

func TestStreamParser_ExtractsArrayElements(t *testing.T) {
	p := newArrayParser("items")
	var got [][]byte
	p.onElement = func(b []byte) { got = append(got, append([]byte(nil), b...)) }

	input := `{"items":[{"q":"one","a":"A"},{"q":"two","a":"B"}]}`
	for i := 0; i < len(input); i++ {
		p.feed([]byte{input[i]})
	}

	want := [][]byte{
		[]byte(`{"q":"one","a":"A"}`),
		[]byte(`{"q":"two","a":"B"}`),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got = %s, want %s", got, want)
	}
}

func TestStreamParser_IgnoresNonArrayFields(t *testing.T) {
	p := newArrayParser("items")
	var got [][]byte
	p.onElement = func(b []byte) { got = append(got, append([]byte(nil), b...)) }
	p.feed([]byte(`{"verdict":"ok","items":[{"q":"x"}]}`))
	want := [][]byte{[]byte(`{"q":"x"}`)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got = %s, want %s", got, want)
	}
}

func TestStreamParser_HandlesNestedObjects(t *testing.T) {
	p := newArrayParser("items")
	var got [][]byte
	p.onElement = func(b []byte) { got = append(got, append([]byte(nil), b...)) }
	p.feed([]byte(`{"items":[{"q":"x","meta":{"k":"v"}},{"q":"y"}]}`))
	want := [][]byte{
		[]byte(`{"q":"x","meta":{"k":"v"}}`),
		[]byte(`{"q":"y"}`),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got = %s, want %s", got, want)
	}
}

func TestStreamParser_IgnoresWhitespaceAndCommas(t *testing.T) {
	p := newArrayParser("items")
	var got [][]byte
	p.onElement = func(b []byte) { got = append(got, append([]byte(nil), b...)) }
	p.feed([]byte(`{ "items" : [ {"q":"a"} , {"q":"b"} ] }`))
	if len(got) != 2 {
		t.Fatalf("count = %d, want 2; got = %s", len(got), got)
	}
}

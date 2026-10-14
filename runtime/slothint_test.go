package runtime

import "testing"

// SetHint writes the same as Set, with a hint that is right, wrong, or for a key that is not there yet.
func TestSetHint(t *testing.T) {
	keys := []Value{StringValue("a"), StringValue("a_rather_long_key"), StringValue("c"), StringValue("another_long_key_name")}
	tbl := NewTable()
	var hints [4]uint32
	for round := 0; round < 3; round++ {
		for i, k := range keys {
			tbl.SetHint(k, IntValue(int64(round*10+i)), &hints[i])
		}
		for i, k := range keys {
			if got := tbl.Get(k); got != IntValue(int64(round*10+i)) {
				t.Fatalf("round %d key %d: got %v", round, i, got)
			}
		}
	}
	// a hint for the slot of another key, and one out of range
	var wrong uint32 = 3
	tbl.SetHint(keys[0], IntValue(100), &wrong)
	var far uint32 = 1000
	tbl.SetHint(keys[1], IntValue(101), &far)
	if tbl.Get(keys[0]) != IntValue(100) || tbl.Get(keys[1]) != IntValue(101) || tbl.Get(keys[2]) != IntValue(22) {
		t.Fatal("a wrong hint changed other keys")
	}
	// nil removes, and the key can be set again through the hint
	tbl.SetHint(keys[2], NilValue, &hints[2])
	if !tbl.Get(keys[2]).IsNil() {
		t.Fatal("not removed")
	}
	tbl.SetHint(keys[2], IntValue(7), &hints[2])
	if tbl.Get(keys[2]) != IntValue(7) {
		t.Fatal("not set again")
	}
	// a metatable's cached metamethods follow a write through the hint
	meta := NewTable()
	var mh uint32
	meta.SetHint(StringValue("__index"), IntValue(1), &mh)
	r := New(nil)
	defer r.Close(nil)
	obj := NewTable()
	obj.SetMetatable(meta)
	th := r.MainThread()
	if th.metaGetS(TableValue(obj), "__index") != IntValue(1) {
		t.Fatal("first value")
	}
	meta.SetHint(StringValue("__index"), IntValue(2), &mh)
	if th.metaGetS(TableValue(obj), "__index") != IntValue(2) {
		t.Fatal("the metamethod cache is stale after SetHint")
	}
	// an integer key goes the plain way
	tbl.SetHint(IntValue(5), StringValue("five"), &hints[0])
	if tbl.Get(IntValue(5)) != StringValue("five") {
		t.Fatal("integer key")
	}
}

// Equals on long strings: the same string, an equal one in other memory, one that differs in the last byte, and one of another length.
func TestEqualsLongStrings(t *testing.T) {
	a := "consumption2_rated_scaled"
	b := string(append([]byte(nil), a...)) // equal, other memory
	c := a[:len(a)-1] + "x"
	d := a + "_"
	va, vb, vc, vd := StringValue(a), StringValue(b), StringValue(c), StringValue(d)
	if !va.Equals(va) || !va.Equals(vb) || !vb.Equals(va) {
		t.Error("equal strings are not equal")
	}
	if va.Equals(vc) || va.Equals(vd) || vd.Equals(va) {
		t.Error("different strings are equal")
	}
	if va.Equals(StringValue("short")) || va.Equals(IntValue(1)) {
		t.Error("a long string equals something else")
	}
}

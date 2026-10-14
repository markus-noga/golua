package runtime

import (
	"sync/atomic"
	"unsafe"

	"github.com/arnodel/golua/runtime/internal/luagc"
)

// Table implements a Lua table.
type Table struct {
	// This is where the implementation details are.
	*mixedTable

	meta *Table
}

// NewTable returns a new Table.
func NewTable() *Table {
	return &Table{mixedTable: &mixedTable{}}
}

// NewTableFromSlice creates a table whose array part references the given slice.
// The slice is NOT copied - modifications to the table affect the underlying slice.
func NewTableFromSlice(values []Value) *Table {
	return &Table{
		mixedTable: &mixedTable{
			array: &array{
				values: values,
				len:    uintptr(len(values)),
			},
		},
	}
}

// NewTableWithCapacity creates a table with preallocated capacity.
// This is used for table.create (Lua 5.5) to avoid repeated reallocations.
// nseq: capacity hint for array part (sequence elements)
// nrec: capacity hint for hash part (record/key-value pairs)
func NewTableWithCapacity(nseq, nrec int) *Table {
	return &Table{
		mixedTable: newMixedTableWithCapacity(nseq, nrec),
	}
}

// Metatable returns the table's metatable.
func (t *Table) Metatable() *Table {
	return t.meta
}

// SetMetatable sets the table's metatable.
func (t *Table) SetMetatable(m *Table) {
	t.meta = m
}

var _ luagc.Value = (*Table)(nil)

func (t *Table) Key() luagc.Key {
	return unsafe.Pointer(t.mixedTable)
}

func (t *Table) Clone() luagc.Value {
	clone := new(Table)
	*clone = *t
	return clone
}

// Get returns t[k].
func (t *Table) Get(k Value) Value {
	return t.get(k)
}

// getHint is Get for a key at a place in the code that keeps a hint for it (see hashTable.findHint).
func (t *Table) getHint(k Value, hint *atomic.Uint32) Value {
	if _, ok := k.iface.(string); ok { // not an integer, so only in the hash part
		return t.mixedTable.hashTable.findHint(k, hint)
	}
	return t.get(k)
}

// Set implements t[k] = v (doesn't check if k is nil). It returns the number
// of bytes the table allocated to store v: zero when v fits in storage the
// table already has, the size of the new storage when the table had to grow.
func (t *Table) Set(k, v Value) uint64 {
	if v.IsNil() {
		t.mixedTable.remove(k)
		return 0
	}
	before := t.mixedTable.storage()
	t.mixedTable.insert(k, v)
	return t.mixedTable.storage().allocatedSince(before)
}

// TableHeaderSize is the memory a new empty table takes.
const TableHeaderSize = uint64(unsafe.Sizeof(Table{}) + unsafe.Sizeof(mixedTable{}))

// SetHint is Set for a string key at a place that keeps a hint for it: the index of the slot where the key was found last time (see
// hashTable.findHint), which is checked before it is used, so a wrong hint only costs the lookup.  A table that is written again and
// again with the same keys, such as the context a host builds for every call of a rule, spends most of Set on finding the key; with the
// hint it is a compare and a store.  The hint belongs to the caller and must not be shared between goroutines.
func (t *Table) SetHint(k, v Value, hint *uint32) uint64 {
	if v.IsNil() {
		return t.Set(k, v)
	}
	if _, ok := k.iface.(string); !ok {
		return t.Set(k, v)
	}
	if ht := t.mixedTable.hashTable; ht != nil {
		if i := int(*hint); i < len(ht.slots) {
			if it := &ht.slots[i]; it.key.Equals(k) {
				if ht.mc != nil {
					ht.mc.valid = 0
				}
				it.value = v
				return 0
			}
		}
	}
	n := t.Set(k, v)
	if ht := t.mixedTable.hashTable; ht != nil {
		if it, i := findSlot(ht.slots, (1<<ht.base)-1, k); it != nil {
			*hint = uint32(i)
		}
	}
	return n
}

// Reset implements t[k] = v only if t[k] was already non-nil.
func (t *Table) Reset(k, v Value) (wasSet bool) {
	if v.IsNil() {
		return t.mixedTable.remove(k)
	}
	return t.mixedTable.reset(k, v)
}

// Len returns a length for t (see lua docs for details).
func (t *Table) Len() int64 {
	return int64(t.mixedTable.len())
}

// Next returns the key-value pair that comes after k in the table t.
//   - If k is NilValue, the first key-value pair in the table t is returned.
//   - If k is the last key in the table t, a pair of NilValues is returned.
//   - If the table t is empty, the returned key-value pair is always a pair of NilValues, regardless of k.
//   - In all cases, ok is true if and only if k is either NilValue or a key present in the table t.
func (t *Table) Next(k Value) (next Value, val Value, ok bool) {
	return t.mixedTable.next(k)
}

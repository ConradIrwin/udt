package udt

import (
	"fmt"
	"io"
	"math"
	"slices"
	"unicode/utf8"
)

const delete = rune(math.MaxInt32)

type Document struct {
	value []byte
	meta  []byte

	skipList   []SkipListNode
	maxVersion uint32
	actorIDs   map[string]bool
	Edits      map[ID]*EditTreeNode
	head       *EditTreeNode
}

type ID struct {
	actorID string
	version uint32
}

type EditTreeNode struct {
	char rune
	id   ID
	next []*EditTreeNode
}

type SkipListNode struct {
	countBefore uint8
	node        *EditTreeNode
}

type Edit struct {
	char rune
	id   ID
	prev ID
}

func New() *Document {
	head := &EditTreeNode{
		char: 0,
		id:   ID{"", 0},
		next: nil,
	}

	return &Document{
		value:    []byte{},
		Edits:    map[ID]*EditTreeNode{head.id: head},
		head:     head,
		actorIDs: make(map[string]bool),
	}
}

func Load(bytes []byte) (*Document, error) {
	value, meta, ok := readLengthPrefixed(bytes)
	if !ok {
		return nil, fmt.Errorf("udt: missing length field")
	}
	d := &Document{
		value: value,
		meta:  meta,

		maxVersion: 0,
		actorIDs:   nil,
		Edits:      nil,
		head:       nil,
	}

	if !utf8.Valid(d.value) {
		return nil, fmt.Errorf("udt: contains invalid utf8")
	}
	return d, nil
}

func (d *Document) String() string {
	if d.Edits == nil {
		return string(d.value)
	}
	ret := ""
	d.head.charsInOrder(func(et *EditTreeNode) bool {
		ret += string(et.char)
		return true
	})
	return ret
}

func (d *Document) PrepareToEdit() error {
	actorIDs, rest, ok := readLengthPrefixed(d.meta)
	if !ok {
		return fmt.Errorf("couldn't read until end of actorIDs")
	}
	backing, rest, ok := readLengthPrefixed(rest)
	if !ok {
		return fmt.Errorf("couldn't read until end of backing")
	}
	predActors, rest, ok := readLengthPrefixed(rest)
	if !ok {
		return fmt.Errorf("couldn't read")
	}
	predVersion, rest, ok := readLengthPrefixed(rest)
	if !ok {
		return fmt.Errorf("couldn't read")
	}
	idActors, rest, ok := readLengthPrefixed(rest)
	if !ok {
		return fmt.Errorf("couldn't read")
	}
	idVersion, rest, ok := readLengthPrefixed(rest)
	if !ok {
		return fmt.Errorf("couldn't read")
	}
	fmt.Println("value", len(d.value))
	fmt.Println("ids", len(actorIDs))
	fmt.Println("backing", len(backing))
	fmt.Println("pred a", len(predActors))
	fmt.Println("pred v", len(predVersion))
	fmt.Println("id a", len(idActors))
	fmt.Println("id v", len(idVersion))

	if d.Edits != nil {
		return nil
	}
	return nil
}

func (d *Document) Insert(actorID string, index uint32, char rune) Edit {
	if d.Edits == nil {
		panic("d.PrepareToEdit() must be called before d.Insert()")
	}

	id := ID{
		actorID: actorID,
		version: d.maxVersion + 1,
	}

	Edit := Edit{
		char: char,
		id:   id,
		prev: d.predForIndex(index).id,
	}
	d.ApplyAt(Edit, index)
	return Edit
}

func (d *Document) Remove(actorID string, index uint32) Edit {
	if d.Edits == nil {
		panic("d.PrepareToEdit() must be called before d.Remove()")
	}
	return d.Insert(actorID, index+1, delete)
}

func (d *Document) ApplyAt(Edit Edit, index uint32) {
	if Edit.id.version > d.maxVersion {
		d.maxVersion = Edit.id.version
	}
	if Edit.id.version > 0 {
		d.actorIDs[Edit.id.actorID] = true
	}

	prev := d.Edits[Edit.prev]
	idx := len(prev.next)

	for i, conflict := range prev.next {
		// deletes go first
		if conflict.char == delete && Edit.char != delete {
			continue
		}
		if Edit.char == delete && conflict.char != delete {
			idx = i
			break
		}

		// otherwise, sort by version (newer first)
		if conflict.id.version > Edit.id.version {
			continue
		}
		if conflict.id.version < Edit.id.version {
			idx = i
			break
		}

		// actions by the same actor as the previous character go first
		if conflict.id.actorID == prev.id.actorID &&
			Edit.id.actorID != prev.id.actorID {
			continue
		}
		if conflict.id.actorID != prev.id.actorID &&
			Edit.id.actorID == prev.id.actorID {
			idx = i
			break
		}

		// otherwise, tie-break by actor id
		if conflict.id.actorID > Edit.id.actorID {
			continue
		}
		if conflict.id.actorID < Edit.id.actorID {
			idx = i
			break
		}
		panic("udt: duplicate Edit applied")
	}
	newNode := &EditTreeNode{
		char: Edit.char,
		id:   Edit.id,
		next: nil,
	}
	var delta int
	if Edit.char == delete {
		if idx == 0 {
			delta = 0 - utf8.RuneLen(prev.char)
		}
	} else {
		delta = utf8.RuneLen(Edit.char)
	}
	d.updateSkipList(index, delta)
	d.Edits[Edit.id] = newNode
	prev.next = slices.Insert(prev.next, idx, newNode)
}

func (d *Document) WriteTo(w io.Writer) (int64, error) {
	total := int64(0)
	write := func(b []byte) error {
		more, err := w.Write(b)
		total += int64(more)
		return err
	}
	writeLenPrefixed := func(b []byte) error {
		more, err := w.Write(uleb(uint32(len(b))))
		total += int64(more)
		if err != nil {
			return err
		}
		more, err = w.Write(b)
		total += int64(more)
		return err
	}

	if err := writeLenPrefixed([]byte(d.String())); err != nil {
		return total, err
	}

	if d.head == nil {
		err := write(d.meta)
		return total, err
	}

	scratch := make([]byte, 0, 16*len(d.actorIDs))
	actorIDs := make([]string, 0, len(d.actorIDs))
	for actorID := range d.actorIDs {
		actorIDs = append(actorIDs, actorID)
	}
	slices.Sort(actorIDs)
	lookup := map[string]uint32{}
	for i, actorID := range actorIDs {
		scratch = append(scratch, []byte(actorID)...)
		lookup[actorID] = uint32(i + 1)
	}
	if err := writeLenPrefixed(scratch); err != nil {
		return total, err
	}

	backing := NewBackingEncoder()
	pred := NewIDEncoder(lookup)
	id := NewIDEncoder(lookup)

	d.head.EditsInOrder(func(et *EditTreeNode, p *EditTreeNode) bool {
		backing.Append(et)
		pred.Append(p.id, '◊')
		id.Append(et.id, et.char)
		return true
	})
	more, err := backing.WriteTo(w)
	total += more
	if err != nil {
		return total, err
	}
	more, err = pred.WriteTo(w)
	total += more
	if err != nil {
		return total, err
	}
	more, err = id.WriteTo(w)
	total += more
	return total, err
}

// TODO: updating skip list
func (d *Document) updateSkipList(pos uint32, delta int) {
	skipList := d.ensureSkipList()

	for _, node := range skipList {
		if uint32(node.countBefore) < pos {
			pos -= uint32(node.countBefore)
			continue
		}

		// pos <= node.countBefore
		if delta < 0 {
			if node.countBefore > uint8(0-delta) {
				node.countBefore -= uint8(delta)
			} else {
				d.skipList = nil
			}
		} else if delta > 0 {
			if node.countBefore+uint8(delta) < 200 {
				node.countBefore += uint8(delta)
			} else {
				d.skipList = nil
			}
		}
	}

	if pos > 200 {
		d.skipList = nil
	}
}

func (d *Document) ensureSkipList() []SkipListNode {
	if d.skipList != nil {
		return d.skipList
	}
	d.skipList = []SkipListNode{{
		countBefore: 0,
		node:        d.head,
	}}
	delta := uint8(0)
	last := (*EditTreeNode)(nil)
	d.head.charsInOrder(func(et *EditTreeNode) bool {
		// only pick on live characters
		if et.char == delete || len(et.next) > 0 && et.next[0].char == delete {
			return true
		}
		len := uint8(utf8.RuneLen(et.char))
		delta += len
		if delta > 100 {
			d.skipList = append(d.skipList, SkipListNode{
				countBefore: delta - len,
				node:        et,
			})
			delta = 0
			last = nil
		} else {
			last = et
		}
		return true
	})
	if last != nil {
		d.skipList = append(d.skipList, SkipListNode{countBefore: delta - uint8(utf8.RuneLen(last.char)), node: last})
	}
	return d.skipList
}

func (d *Document) predForIndex(index uint32) *EditTreeNode {
	var found *EditTreeNode
	if index == 0 {
		return d.head
	}
	skipList := d.ensureSkipList()
	start := d.head
	pos := uint32(0)
	for _, node := range skipList {
		fmt.Println("hmm", node.countBefore, node.node.char, string(node.node.char))
		if pos+uint32(node.countBefore) <= index {
			start = node.node
			pos += uint32(node.countBefore)
		}
	}
	fmt.Println(index, pos, string(start.char), start.next)

	start.charsInOrder(func(et *EditTreeNode) bool {
		pos += uint32(utf8.RuneLen(et.char))
		if pos >= index {
			found = et
			return false
		}
		return true
	})
	if found == nil {
		panic(fmt.Errorf("index %d out of bounds", index))
	}
	return found
}

func (et *EditTreeNode) EditsInOrder(f func(*EditTreeNode, *EditTreeNode) bool) bool {
	for _, next := range et.next {
		if !f(next, et) {
			return false
		}
		if !next.EditsInOrder(f) {
			return false
		}
	}
	return true
}

func (et *EditTreeNode) charsInOrder(f func(*EditTreeNode) bool) bool {
	i := 0
	isDelete := false
	for i < len(et.next) && et.next[i].char == delete {
		isDelete = true
		i += 1
	}

	if !isDelete && et.id.version != 0 && !f(et) {
		return false
	}

	for i < len(et.next) {
		if !et.next[i].charsInOrder(f) {
			return false
		}
		i += 1
	}

	return true
}

func readLengthPrefixed(b []byte) ([]byte, []byte, bool) {
	len, b, ok := readULEB(b)
	if !ok {
		return nil, nil, false
	}
	return b[0:len], b[len:], true
}

func readULEB(b []byte) (uint32, []byte, bool) {
	var n uint64
	var i int
	for {
		if len(b) == 0 {
			return 0, nil, false
		}

		c := b[0]
		b = b[1:]
		n |= uint64(c&0x7f) << (i * 7)
		i++
		if c&0x80 == 0 {
			if c == 0 && i > 1 || i >= 10 || n > math.MaxUint32 {
				return 0, nil, false
			}
			return uint32(n), b, true
		}
	}
}

func appendLengthPrefixd(b []byte, c []byte) []byte {
	b = append(b, uleb(uint32(len(c)))...)
	return append(b, c...)
}

// from: https://github.com/aviate-labs/leb128/blob/v0.1.0/leb.go#L13
func uleb(n uint32) []byte {
	leb := make([]byte, 0)
	if n == 0 {
		return []byte{0}
	}
	for n != 0x00 {
		b := byte(n & 0x7F)
		n >>= 7
		if n != 0x00 {
			b |= 0x80
		}
		leb = append(leb, b)
	}
	return leb
}

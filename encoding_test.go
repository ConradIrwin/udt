package udt

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func encoded(wt io.WriterTo) []byte {
	buf := &bytes.Buffer{}
	_, err := wt.WriteTo(buf)
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestRLEEncoding(t *testing.T) {
	r := require.New(t)

	rle := NewRLEEncoder()
	r.Equal([]byte{0x00}, encoded(rle))

	rle.Append([]byte{'a'})
	r.Equal([]byte{0x02, 0x00, 'a'}, encoded(rle))

	rle.Append([]byte{'a'})
	rle.Append([]byte{'a'})
	r.Equal([]byte{0x03, 0x02, 'a', 'a'}, encoded(rle))

	rle.Append([]byte{'a'})
	rle.Append([]byte{'a'})
	rle.Append([]byte{'a'})
	r.Equal(encoded(rle), []byte{0x02, 0x01, 'a'})

	rle.Append([]byte{'a'})
	rle.Append([]byte{'a'})
	rle.Append([]byte{'a'})
	rle.Append([]byte{'a'})
	r.Equal(encoded(rle), []byte{0x02, 0x03, 'a'})

	rle.Append([]byte{'a'})
	rle.Append([]byte{'b'})
	r.Equal([]byte{0x03, 0x02, 'a', 'b'}, encoded(rle))

	rle.Append([]byte{'a'})
	rle.Append([]byte{'a'})
	rle.Append([]byte{'b'})
	r.Equal([]byte{0x04, 0x04, 'a', 'a', 'b'}, encoded(rle))

	rle.Append([]byte{'a'})
	rle.Append([]byte{'a'})
	rle.Append([]byte{'a'})
	rle.Append([]byte{'b'})
	r.Equal([]byte{0x04, 0x01, 'a', 0x00, 'b'}, encoded(rle))

	rle.Append([]byte{'a'})
	rle.Append([]byte{'a'})
	rle.Append([]byte{'a'})
	rle.Append([]byte{'a'})
	rle.Append([]byte{'b'})
	r.Equal([]byte{0x04, 0x03, 'a', 0x00, 'b'}, encoded(rle))

	rle.Append([]byte{'b'})
	rle.Append([]byte{'a'})
	rle.Append([]byte{'c'})
	r.Equal([]byte{0x04, 0x04, 'b', 'a', 'c'}, encoded(rle))
}

func TestBackingEncoding(t *testing.T) {
	r := require.New(t)

	be := NewBackingEncoder()
	r.Equal([]byte{0x00}, encoded(be))

	noNext := []*EditTreeNode{}
	isDeleted := []*EditTreeNode{{char: delete}}

	be.Append(&EditTreeNode{char: 'a', next: isDeleted})
	be.Append(&EditTreeNode{char: delete, next: noNext})
	be.Append(&EditTreeNode{char: 'b', next: isDeleted})
	be.Append(&EditTreeNode{char: delete, next: noNext})
	be.Append(&EditTreeNode{char: 'c', next: isDeleted})
	be.Append(&EditTreeNode{char: delete, next: noNext})
	r.Equal([]byte{0x04, 0x04, 'a', 'b', 'c'}, encoded(be))
}

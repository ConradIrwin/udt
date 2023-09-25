package udt

import (
	"io"
	"unicode/utf8"

	"slices"
)

// RLEEncoder encodes things in run-length order.
// It presumptively adds things to the buffer to avoid too much storage.
// The format is as follows:
//   - len: uint32 (VLE)
//   - if *odd* (len + 1)/2 copies of the next value
//   - if *even* (len)/2 of the next values are literally encoded
//
// The values depend on the column

type RLEEncoder struct {
	runs [][]byte

	last []byte

	repeatCount uint32

	litCount uint32
	litRun   []byte
	isPair   bool
}

func NewRLEEncoder() *RLEEncoder {
	return &RLEEncoder{
		runs:        nil,
		last:        nil,
		repeatCount: 0,
		litCount:    0,
		litRun:      []byte{},
		isPair:      false,
	}
}

func (e *RLEEncoder) Append(b []byte) {
	isSame := slices.Equal(e.last, b)

	// we were repeating, either continue, or switch to literal
	if e.repeatCount > 0 {
		if isSame {
			e.repeatCount += 1
			return
		}

		buf := append(uleb(e.repeatCount*2-5), e.last...)
		e.runs = append(e.runs, buf)

		e.repeatCount = 0
		e.last = slices.Clone(b)
		return
	}

	// we had a pending pair, if we get a thrid match switch to repeating
	// otherwise flush the pair
	if e.isPair {
		e.isPair = false

		if isSame {
			if e.litCount > 0 {
				e.runs = append(e.runs, uleb(e.litCount*2-2))
				e.runs = append(e.runs, e.litRun)

				e.litCount = 0
				e.litRun = []byte{}
			}
			e.repeatCount = 3
			e.last = slices.Clone(b)
			return
		}

		e.litCount += 2
		e.litRun = append(e.litRun, e.last...)
		e.litRun = append(e.litRun, e.last...)
		e.last = slices.Clone(b)
		return
	}

	// if matches the previous character switch to pending-pair mode
	if isSame {
		e.isPair = true
		return
	}

	// otherwise flush last into the run and continue
	if len(e.last) > 0 {
		e.litCount += 1
		e.litRun = append(e.litRun, e.last...)
	}
	e.last = slices.Clone(b)
}

func (e *RLEEncoder) WriteTo(w io.Writer) (int64, error) {
	if e.repeatCount > 0 {
		buf := append(uleb(e.repeatCount*2-5), e.last...)
		e.runs = append(e.runs, buf)
	} else {
		if e.isPair {
			e.litCount += 2
			e.litRun = append(e.litRun, e.last...)
			e.litRun = append(e.litRun, e.last...)

		} else if len(e.last) > 0 {
			e.litCount += 1
			e.litRun = append(e.litRun, e.last...)
		}

		if e.litCount > 0 {
			e.runs = append(e.runs, uleb(e.litCount*2-2))
			e.runs = append(e.runs, e.litRun)
		}
	}

	total := 0
	for _, b := range e.runs {
		total += len(b)
	}
	i, err := w.Write(uleb(uint32(total)))
	if err != nil {
		return int64(i), err
	}
	for _, b := range e.runs {
		j, err := w.Write(b)
		i += j
		if err != nil {
			return int64(i), err
		}
	}
	*e = *NewRLEEncoder()
	return int64(i), nil
}

type RLEDecoder struct {
	buffer []byte

	litCount uint32
	repCount uint32
	last     []byte
}

func (d *RLEDecoder) Next(next func([]byte) []byte) []byte {
	if d.litCount == 0 && d.repCount == 0 {
		count, rest, ok := readULEB(d.buffer)
		if !ok {
			return nil
		}
		d.buffer = rest
		if count%2 == 0 {
			d.litCount = (count + 2) / 2
		} else {
			d.repCount = (count + 5) / 2
		}
	}
	prefix := next(d.buffer)
	if d.repCount > 0 {
		d.repCount--
	} else {
		d.buffer = d.buffer[len(prefix):]
		d.litCount--
	}
	return prefix
}

type DeltaEncoder struct {
	current uint32
}

func (d *DeltaEncoder) Append(v uint32) uint32 {
	ret := int64(v) - int64(d.current)
	d.current = v
	if ret < 0 {
		return uint32(0-ret)*2 + 1
	}
	return uint32(ret) * 2
}

type BackingEncoder struct {
	rle     *RLEEncoder
	scratch [4]byte
}

func NewBackingEncoder() *BackingEncoder {
	return &BackingEncoder{
		rle:     NewRLEEncoder(),
		scratch: [4]byte{0, 0, 0, 0},
	}

}

const insertOp = 0xFF
const deleteOp = 0xFE

func (e *BackingEncoder) Append(et *EditTreeNode) {
	deleteCount := 0
	for deleteCount < len(et.next) && et.next[deleteCount].char == delete {
		deleteCount += 1
	}

	if deleteCount > 0 {
		len := utf8.EncodeRune(e.scratch[:], et.char)
		for i := 0; i < deleteCount; i++ {
			e.rle.Append(e.scratch[:len])
		}
	} else if et.char != delete {
		e.rle.Append([]byte{insertOp})
	}
}

func (e *BackingEncoder) WriteTo(w io.Writer) (int64, error) {
	return e.rle.WriteTo(w)
}

type IDEncoder struct {
	actorOffset *RLEEncoder
	liveDelta   *DeltaEncoder
	deleteDelta *DeltaEncoder
	version     *RLEEncoder
	table       map[string]uint32
}

func NewIDEncoder(table map[string]uint32) *IDEncoder {
	return &IDEncoder{
		actorOffset: NewRLEEncoder(),
		liveDelta:   &DeltaEncoder{},
		deleteDelta: &DeltaEncoder{},
		version:     NewRLEEncoder(),
		table:       table,
	}

}

func (e *IDEncoder) Append(id ID, char rune) {
	e.actorOffset.Append(uleb(e.table[id.actorID]))
	var delta uint32
	if char == delete {
		delta = e.deleteDelta.Append(id.version)
	} else {
		delta = e.liveDelta.Append(id.version)
	}
	//	fmt.Println("append", id.version, delta, string(char))
	e.version.Append(uleb(delta))
}

func (e *IDEncoder) WriteTo(w io.Writer) (int64, error) {
	i, err := e.actorOffset.WriteTo(w)
	if err != nil {
		return i, err
	}
	j, err := e.version.WriteTo(w)
	return i + j, err
}

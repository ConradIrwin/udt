package udt_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/ConradIrwin/udt"
	"github.com/stretchr/testify/require"
)

func TestUDT(t *testing.T) {
	r := require.New(t)
	d := udt.New()
	a := "aaaaaaaaaaaaaaaa"

	d.Insert(a, 0, 'a')
	r.Equal("a", d.String())
	d.Insert(a, 0, 'b')
	r.Equal("ba", d.String())
	d.Insert(a, 2, 'c')
	r.Equal("bac", d.String())

	d.Remove(a, 0)
	r.Equal("ac", d.String())
	d.Remove(a, 1)
	r.Equal("a", d.String())
	d.Remove(a, 0)
	r.Equal("", d.String())

	buf := bytes.NewBuffer(nil)
	_, err := d.WriteTo(buf)
	r.NoError(err)
	fmt.Print(len(buf.Bytes()), buf.Bytes())
	d, err = udt.Load(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	err = d.PrepareToEdit()
	if err != nil {
		t.Fatal(err)
	}
}

type Edit struct {
	Index  uint32
	Delete uint32
	Insert string
}

func TestUDTTrace(t *testing.T) {
	r := require.New(t)
	data, err := os.ReadFile("references.bib.json")
	r.NoError(err)
	Edits := []Edit{}
	r.NoError(json.Unmarshal(data, &Edits))

	d := udt.New()
	defer func() {
		if p := recover(); p != nil {
			fmt.Println(len(d.String()), d.String())
			panic(p)
		}
	}()
	a := "aaaaaaaaaaaaaaaa"
	for j, e := range Edits {
		if j%100 == 0 {
			fmt.Println(j)
		}
		for i := uint32(0); i < e.Delete; i++ {
			d.Remove(a, e.Index)
		}
		for i, c := range e.Insert {
			d.Insert(a, uint32(i)+e.Index, c)
		}
	}

	buf := bytes.Buffer{}
	d.WriteTo(&buf)

	e, err := udt.Load(slices.Clone(buf.Bytes()))
	r.NoError(err)
	r.NoError(e.PrepareToEdit())
	r.NoError(os.WriteFile("output.tex", []byte(d.String()), 0o666))
	r.NoError(os.WriteFile("output.udt", buf.Bytes(), 0o666))
}

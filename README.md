udt is an attempt at a micro-sized crdt. The idea is to see how much overhead we can remove before everything breaks down.

I want to optimize for the following (in order)
* Getting the current text of the document should be as fast as possible
* The file size should be compact
* Making a single Edit to the document should be fast

The conceptual model of udt is that you have a totally ordered list of Edits:

```
type Edit struct {
    // a unicode char, (or a special value "delete" if it's a delete)
    char rune
    // the ID of the Edit "before" this Edit
    predecessor ID
    // the ID of this Edit
    id ID
}

type ID struct {
    actorID [16]byte // 16 random bytes
    version int // set to 1 more than the highest version that was visible when creating the Edit
}
```

When operating on the CRDT, a tree-like structure is created using the following node type:

```
type EditTreeNode {
    char rune
    id ID
    // contains all Edits with predecessor ID == id
    // sorted to ensure that:
    // deletes come before inserts
    // newer Edits come before older Edits
    // equally recent Edits by the same actor come before Edits by other actors
    // equally recent Edits by other actors are sorted by actor id
    successors []*EditTreeNode
}
```

The order of successors ensure that we can quickly determine if a node is deleted,
and we can be sure that all deletes will be adjacent to the character they deleted when
serialized.

The order of Edits when serializing them to disk is the depth-first traversal of this tree. This is so that the order of Edits on disk matches the order of characters in the latest version of the document.

## File format

Taking inspiration from automerge, the data is stored in a columnar format on disk, and compressed using per-column techniques. Each column is prefixed by its length in bytes.

In order the columns are:

1. The current text of the document encoded as UTF-8
3. The actor ids sorted
2. The deleted text of the document (with placeholders for non-deleted characters) encoded using a run-length encoding.
4. The actor ids of the "predecessor" field (represented as run-length encoded indexes into the actor ids column)
4. The versions of the "predecessor" field (represented as run-length encoded deltas)
4. The actor ids of the "id" field (represented as run-length encoded indexes into the actor ids column)
4. The versions of the "id" field (represented as run-length encoded deltas)

Although the format contains some binary data, it still seems to compress relatively well
with gzip for reducing storage/transfer bandwidth if you want.

## In memory format

When loading a new document, it is loaded into memory as-is. The extent of the current value can be determined by reading the first length-prefix, and it can be read by the application with no further processing.

When making Edits, a hybrid scheme is used where we rely on the on-disk format for pre-existing data, and overlay additional changes that are stored only in memory.

Serializing the document requries merging these two and writing them out.

The time consuming part of making an Edit is figuring out "where" to make the change. The API looks like this:
```
// insert the given unicode character before the index'th character
doc.Insert(actorID, index, char)
// remove the index'th unicode character.
doc.Remove(actorID, index)
```

In order to create an Edit we need to convert the index into a predecessor ID.

Naively we can just iterate over the entire document, but this makes each Edit O(n) in the size of the document, and so rebuilding a document from a list of Edits is O(n^2).

To reduce this significantly we iterate over the document once (on first Edit) to create a skip list, and use that to get closer to O(n * log(n)) for the same operation.

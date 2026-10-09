# Golden files

These files pin gloom's on-disk format and hashing so that filters persisted by
any past release keep loading, and keep answering identically, in every future
release. They are checked by `golden_test.go`.

**Never edit, regenerate, or delete an existing file here.** If a golden test
fails, the code change is what broke compatibility, not the golden file.

## Layout

- `v<N>/<name>.bin`: a filter serialized by `MarshalBinary` in format version N.
- `v<N>/<name>.json`: how that filter was built (block count, k, which keys were
  added and how), what it must decode to, its SHA-256, and the exact `Test`
  result for a fixed set of non-member probe keys.
- `hash_vectors.json`: xxh3 128-bit outputs and the derived block index /
  intra-block hash for inputs of many lengths. This narrows down the cause when
  the filter goldens fail (e.g. after an xxh3 dependency bump).

## What the tests check

- **Decode** (every version, forever): each `.bin` loads, has the recorded
  header fields, returns true for every key added to it, and gives exactly the
  recorded results for the non-member probes.
- **Reproduce** (current version only): rebuilding each recipe with the current
  code yields a byte-identical `MarshalBinary` output, and `AtomicFilter` sets
  exactly the same bits.
- The prime partitions, format constants, and hash vectors match their frozen
  values.

## Adding golden files

Add a case to `goldenFilterCases` in `golden_test.go`, then run:

```sh
go test -run TestGolden -update-golden
```

`-update-golden` only creates missing files; it refuses to overwrite existing
ones. Commit the new files.

## Changing the format

Bump `serializeVersion`, keep `UnmarshalBinary` able to read every older
version, and add goldens for the new version under `v<N+1>/` with
`-update-golden`. The `v1/` (and other older) files stay as they are, and the
decode test keeps checking them.

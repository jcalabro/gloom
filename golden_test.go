package gloom

// Golden-file compatibility tests.
//
// Serialized filters are written to disk and may be read back by any future release of
// gloom, so the following must never change silently:
//
//   - the byte layout produced by MarshalBinary and accepted by UnmarshalBinary,
//   - the hash pipeline (xxh3 128-bit, reduceRange, foldIntra) that maps a key to bits,
//   - the prime partitions and offsets that place those bits inside a block.
//
// A change to any of them means a filter persisted by an older release either fails to load
// or, far worse, loads and returns false negatives. The files under testdata/golden pin all
// of these. They are append-only: -update-golden creates files that are missing but never
// overwrites an existing one. If a golden test fails, the code change is what is wrong, not
// the golden file. See testdata/golden/README.md.

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zeebo/xxh3"
)

var updateGolden = flag.Bool("update-golden", false, "create missing golden files under testdata/golden (never overwrites existing files)")

const (
	goldenDir      = "testdata/golden"
	goldenHashFile = "testdata/golden/hash_vectors.json"
)

// goldenFilterCase describes how to build one golden filter. Adding a case and running
// `go test -run TestGolden -update-golden` writes its files; once committed, a case's
// files must never be regenerated.
type goldenFilterCase struct {
	name        string
	description string
	numBlocks   uint64
	k           uint32
	numKeys     int
	method      string // "bytes" (Add), "string" (AddString), or "mixed" (alternating)
	edgeKeys    bool   // prepend goldenEdgeKeys to the generated members
	numProbes   int
}

func goldenFilterCases() []goldenFilterCase {
	cases := []goldenFilterCase{
		{
			name:        "empty_1block_k7",
			description: "single-block filter with nothing added (all-zero block data)",
			numBlocks:   1, k: 7, numKeys: 0, method: "bytes", numProbes: 256,
		},
		{
			name:        "saturated_1block_k7",
			description: "single block loaded far past capacity so nearly every bit is set",
			numBlocks:   1, k: 7, numKeys: 300, method: "bytes", edgeKeys: true, numProbes: 2000,
		},
		{
			name:        "typical_212blocks_k7",
			description: "params OptimalParams(10_000, 0.01) chose when this file was created, filled to capacity via AddString",
			numBlocks:   212, k: 7, numKeys: 10_000, method: "string", edgeKeys: true, numProbes: 20_000,
		},
		{
			name:        "large_1009blocks_k10",
			description: "prime block count spanning the multiply-shift block selection range, filled via Add to ~1% FP",
			numBlocks:   1009, k: 10, numKeys: 50_000, method: "bytes", edgeKeys: true, numProbes: 20_000,
		},
	}
	// One small filter per supported k, so every prime partition is pinned. Each is loaded
	// to roughly 70% fill per partition so that a meaningful number of probes are false
	// positives, which pins the bit layout far more tightly than an all-negative result.
	for k := uint32(minK); k <= maxK; k++ {
		cases = append(cases, goldenFilterCase{
			name:        fmt.Sprintf("k%02d_3blocks", k),
			description: fmt.Sprintf("three-block filter exercising the k=%d prime partition", k),
			numBlocks:   3, k: k, numKeys: int(3 * 600 / k), method: "mixed", edgeKeys: true, numProbes: 4000,
		})
	}
	return cases
}

// goldenFilterManifest is the JSON sidecar stored next to each golden .bin file. It records
// everything needed to rebuild and check the filter, so a golden file stays tested even
// after its case is removed from goldenFilterCases.
type goldenFilterManifest struct {
	Description   string `json:"description"`
	FormatVersion byte   `json:"format_version"`
	SHA256        string `json:"sha256"`

	NumBlocks uint64 `json:"num_blocks"`
	K         uint32 `json:"k"`
	Count     uint64 `json:"count"`

	NumKeys  int    `json:"num_keys"`
	Method   string `json:"method"`
	EdgeKeys bool   `json:"edge_keys"`

	// ProbeResults is a hex bitmap (bit i = byte i/8, bit i%8) of Test(goldenKey("probe", i))
	// for i in [0, NumProbes). Non-members are deterministic too, so pinning their results
	// (false positives included) pins the full key-to-bit mapping, not just "no false
	// negatives".
	NumProbes    int    `json:"num_probes"`
	ProbeResults string `json:"probe_results"`
}

// goldenKey returns the i-th deterministic key for prefix: "<prefix>-<i>:" followed by
// (i*7)%1031 filler bytes cycling through every byte value. Lengths range from a few bytes
// to just over 1 KiB, crossing every xxh3 input-size code path.
//
// FROZEN: existing golden files depend on this exact output.
func goldenKey(prefix string, i int) []byte {
	key := fmt.Appendf(nil, "%s-%d:", prefix, i)
	for j := range (i * 7) % 1031 {
		key = append(key, byte(j))
	}
	return key
}

// goldenEdgeKeys are inputs at the boundaries of the hash function's code paths.
//
// FROZEN: existing golden files depend on this exact list and order.
var goldenEdgeKeys = [][]byte{
	{},
	{0x00},
	{0xff},
	[]byte("a"),
	[]byte("ab"),
	[]byte("abc"),
	[]byte("abcd"),
	bytes.Repeat([]byte{0x00}, 8),
	bytes.Repeat([]byte{0xff}, 9),
	[]byte("0123456789abcdef"),
	[]byte("0123456789abcdefg"),
	bytes.Repeat([]byte("x"), 128),
	bytes.Repeat([]byte("y"), 129),
	bytes.Repeat([]byte("z"), 240),
	bytes.Repeat([]byte("w"), 241),
	[]byte("hello, 世界"),
}

// goldenMembers returns the keys inserted into a golden filter, in insertion order.
func goldenMembers(edgeKeys bool, numKeys int) [][]byte {
	var keys [][]byte
	if edgeKeys {
		keys = append(keys, goldenEdgeKeys...)
	}
	for i := range numKeys {
		keys = append(keys, goldenKey("member", i))
	}
	return keys
}

// goldenUseString reports whether the i-th member is inserted with AddString (vs Add).
func goldenUseString(method string, i int) bool {
	switch method {
	case "bytes":
		return false
	case "string":
		return true
	case "mixed":
		return i%2 == 1
	}
	panic("unknown golden insertion method " + method)
}

// buildGoldenFilter constructs a filter from a manifest's recipe using the current code.
func buildGoldenFilter(m *goldenFilterManifest) *Filter {
	f := NewWithParams(m.NumBlocks, m.K)
	for i, key := range goldenMembers(m.EdgeKeys, m.NumKeys) {
		if goldenUseString(m.Method, i) {
			f.AddString(string(key))
		} else {
			f.Add(key)
		}
	}
	return f
}

// goldenProbeBitmap returns the hex bitmap of Test results for the first n probe keys.
func goldenProbeBitmap(f *Filter, n int) string {
	bitmap := make([]byte, (n+7)/8)
	for i := range n {
		if f.Test(goldenKey("probe", i)) {
			bitmap[i/8] |= 1 << (i % 8)
		}
	}
	return hex.EncodeToString(bitmap)
}

// goldenFilter is a golden filter manifest along with the path of its .bin file.
type goldenFilter struct {
	binPath string
	*goldenFilterManifest
}

// loadGoldenFilters returns every golden filter on disk, sorted by path. It globs rather
// than consulting goldenFilterCases so that files from older format versions or retired
// cases are checked forever.
func loadGoldenFilters(t *testing.T) []goldenFilter {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(goldenDir, "v*", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]goldenFilter, 0, len(paths))
	for _, p := range paths { // Glob returns paths in lexical order
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var m goldenFilterManifest
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		out = append(out, goldenFilter{binPath: strings.TrimSuffix(p, ".json") + ".bin", goldenFilterManifest: &m})
	}
	return out
}

// writeGoldenFile creates path with data, refusing to overwrite an existing file.
func writeGoldenFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		t.Fatalf("refusing to write golden file: %v", err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("created golden file %s", path)
}

// TestGoldenFilterCasesPresent ensures every case in goldenFilterCases has golden files on
// disk, creating missing ones when -update-golden is set.
func TestGoldenFilterCasesPresent(t *testing.T) {
	dir := filepath.Join(goldenDir, fmt.Sprintf("v%d", serializeVersion))
	for _, c := range goldenFilterCases() {
		binPath := filepath.Join(dir, c.name+".bin")
		jsonPath := filepath.Join(dir, c.name+".json")

		_, binErr := os.Stat(binPath)
		_, jsonErr := os.Stat(jsonPath)
		if binErr == nil && jsonErr == nil {
			continue
		}
		if !errors.Is(binErr, fs.ErrNotExist) && binErr != nil {
			t.Fatal(binErr)
		}
		if !errors.Is(jsonErr, fs.ErrNotExist) && jsonErr != nil {
			t.Fatal(jsonErr)
		}
		if binErr == nil || jsonErr == nil {
			t.Errorf("%s: only one of %s / %s exists; golden files must be created as a pair", c.name, binPath, jsonPath)
			continue
		}
		if !*updateGolden {
			t.Errorf("%s: golden files missing; run `go test -run TestGolden -update-golden` and commit them", c.name)
			continue
		}

		m := &goldenFilterManifest{
			Description:   c.description,
			FormatVersion: serializeVersion,
			NumBlocks:     c.numBlocks,
			K:             c.k,
			NumKeys:       c.numKeys,
			Method:        c.method,
			EdgeKeys:      c.edgeKeys,
			NumProbes:     c.numProbes,
		}
		f := buildGoldenFilter(m)
		data, err := f.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		m.SHA256 = hex.EncodeToString(sum[:])
		m.Count = f.Count()
		m.ProbeResults = goldenProbeBitmap(f, c.numProbes)

		manifest, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		writeGoldenFile(t, binPath, data)
		writeGoldenFile(t, jsonPath, append(manifest, '\n'))
	}
}

// TestGoldenFilterDecode checks that every golden filter ever committed still loads with the
// current code and behaves exactly as it did when it was written: same header fields, no
// false negatives for its members, and identical results for a fixed set of non-members.
//
// This is the backwards-compatibility guarantee for persisted filters, and it applies to
// every format version that has ever been released.
func TestGoldenFilterDecode(t *testing.T) {
	goldens := loadGoldenFilters(t)
	if len(goldens) == 0 {
		t.Fatalf("no golden filters found under %s", goldenDir)
	}
	for _, m := range goldens {
		t.Run(filepath.Base(m.binPath), func(t *testing.T) {
			data, err := os.ReadFile(m.binPath)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			if got := hex.EncodeToString(sum[:]); got != m.SHA256 {
				t.Fatalf("golden file was modified: sha256 %s, manifest says %s (golden files must never change)", got, m.SHA256)
			}
			if data[0] != m.FormatVersion {
				t.Fatalf("version byte = %d, manifest says %d", data[0], m.FormatVersion)
			}

			f, err := UnmarshalBinary(data)
			if err != nil {
				t.Fatalf("UnmarshalBinary rejected a golden filter written by an earlier release: %v", err)
			}
			if f.NumBlocks() != m.NumBlocks {
				t.Errorf("NumBlocks = %d, want %d", f.NumBlocks(), m.NumBlocks)
			}
			if f.K() != m.K {
				t.Errorf("K = %d, want %d", f.K(), m.K)
			}
			if f.Count() != m.Count {
				t.Errorf("Count = %d, want %d", f.Count(), m.Count)
			}

			var falseNegatives int
			for i, key := range goldenMembers(m.EdgeKeys, m.NumKeys) {
				if !f.Test(key) || !f.TestString(string(key)) {
					if falseNegatives < 5 {
						t.Errorf("false negative for member %d (%d bytes)", i, len(key))
					}
					falseNegatives++
				}
			}
			if falseNegatives > 0 {
				t.Fatalf("%d false negatives: the key-to-bit mapping no longer matches persisted filters", falseNegatives)
			}

			if got := goldenProbeBitmap(f, m.NumProbes); got != m.ProbeResults {
				t.Errorf("non-member Test results differ from when the golden was written (first differing probe: %d)",
					firstBitmapDiff(t, got, m.ProbeResults))
			}
		})
	}
}

// TestGoldenFilterReproduce rebuilds each current-version golden filter from its recipe
// and requires MarshalBinary to produce byte-identical output. This catches changes to the
// format, the hash pipeline, or the partition layout at the point they are made, before any
// incompatible file is ever written.
//
// It also builds the same recipe into an AtomicFilter and checks it sets exactly the same
// bits, so every filter type stays consistent with the persisted layout.
//
// Goldens from older format versions are only decoded (TestGoldenFilterDecode), since the
// current MarshalBinary is expected to write the current version.
func TestGoldenFilterReproduce(t *testing.T) {
	goldens := loadGoldenFilters(t)
	var checked int
	for _, m := range goldens {
		if m.FormatVersion != serializeVersion {
			continue
		}
		checked++
		t.Run(filepath.Base(m.binPath), func(t *testing.T) {
			want, err := os.ReadFile(m.binPath)
			if err != nil {
				t.Fatal(err)
			}
			got, err := buildGoldenFilter(m.goldenFilterManifest).MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("MarshalBinary output differs from golden file: %s", describeSerializedDiff(got, want))
			}

			af := NewAtomicWithParams(m.NumBlocks, m.K)
			for i, key := range goldenMembers(m.EdgeKeys, m.NumKeys) {
				if goldenUseString(m.Method, i) {
					af.AddString(string(key))
				} else {
					af.Add(key)
				}
			}
			if af.Count() != m.Count {
				t.Errorf("AtomicFilter Count = %d, want %d", af.Count(), m.Count)
			}
			for i := range af.blocks {
				word := binary.LittleEndian.Uint64(want[headerSize+i*8:])
				if got := af.blocks[i].Load(); got != word {
					t.Fatalf("AtomicFilter block %d word %d = %#016x, golden has %#016x", i/BlockWords, i%BlockWords, got, word)
				}
			}
		})
	}
	if checked == 0 {
		t.Fatalf("no golden filters for current format version %d; add some with -update-golden", serializeVersion)
	}
}

// describeSerializedDiff explains where two serialized filters first differ.
func describeSerializedDiff(got, want []byte) string {
	if len(got) != len(want) {
		return fmt.Sprintf("length %d, golden is %d", len(got), len(want))
	}
	for i := range got {
		if got[i] == want[i] {
			continue
		}
		var region string
		switch {
		case i < 1:
			region = "version"
		case i < 5:
			region = "k"
		case i < 13:
			region = "numBlocks"
		case i < 21:
			region = "count"
		case i < len(got)-checksumSize:
			word := (i - headerSize) / 8
			region = fmt.Sprintf("block %d word %d", word/BlockWords, word%BlockWords)
		default:
			region = "checksum"
		}
		return fmt.Sprintf("first difference at byte %d (%s): got %#02x, golden has %#02x", i, region, got[i], want[i])
	}
	return "identical"
}

// firstBitmapDiff returns the index of the first bit that differs between two hex bitmaps.
func firstBitmapDiff(t *testing.T, a, b string) int {
	t.Helper()
	x, err := hex.DecodeString(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := hex.DecodeString(b)
	if err != nil {
		t.Fatal(err)
	}
	for i := range min(len(x), len(y)) * 8 {
		if (x[i/8]>>(i%8))&1 != (y[i/8]>>(i%8))&1 {
			return i
		}
	}
	return -1
}

// goldenHashNumBlocks are the block counts whose block indices are pinned for every hash
// vector, spanning a single block up to maxNumBlocks.
//
// FROZEN: hash_vectors.json depends on this list.
var goldenHashNumBlocks = []uint64{1, 3, 1000, 1<<20 + 7, maxNumBlocks}

// goldenHashLengths are the input lengths of the hash vectors: every length up to 256
// (covering xxh3's 0, 1-3, 4-8, 9-16, 17-128 and 129-240 byte paths) plus longer inputs
// around its 64-byte stripe and 1024-byte block boundaries.
//
// FROZEN: hash_vectors.json depends on this list.
func goldenHashLengths() []int {
	var lengths []int
	for n := range 257 {
		lengths = append(lengths, n)
	}
	return append(lengths, 300, 511, 512, 513, 1023, 1024, 1025, 2047, 2048, 4096, 10_000, 65_536)
}

// goldenHashInput returns the hash vector input of length n.
//
// FROZEN: hash_vectors.json depends on this exact output.
func goldenHashInput(n int) []byte {
	b := make([]byte, n)
	for j := range b {
		b[j] = byte(j*131 + n + 7)
	}
	return b
}

type goldenHashVector struct {
	Len    int      `json:"len"`
	Hi     string   `json:"hi"`
	Lo     string   `json:"lo"`
	Intra  uint32   `json:"intra"`
	Blocks []uint64 `json:"blocks"` // block index for each of goldenHashNumBlocks
}

type goldenHashVectors struct {
	Description string             `json:"description"`
	NumBlocks   []uint64           `json:"num_blocks"`
	Vectors     []goldenHashVector `json:"vectors"`
}

func computeGoldenHashVector(n int) goldenHashVector {
	in := goldenHashInput(n)
	h := xxh3.Hash128(in)
	v := goldenHashVector{
		Len:   n,
		Hi:    fmt.Sprintf("%016x", h.Hi),
		Lo:    fmt.Sprintf("%016x", h.Lo),
		Intra: foldIntra(h.Lo),
	}
	for _, nb := range goldenHashNumBlocks {
		v.Blocks = append(v.Blocks, reduceRange(h.Hi, nb))
	}
	return v
}

// TestGoldenHashVectors pins the 128-bit xxh3 output and the derived block index and
// intra-block hash for a range of inputs. It localizes the cause when the filter goldens
// fail, and catches an upstream xxh3 change (e.g. a dependency bump, or a bug in one of
// its SIMD code paths on the machine running the tests) on its own.
func TestGoldenHashVectors(t *testing.T) {
	raw, err := os.ReadFile(goldenHashFile)
	if errors.Is(err, fs.ErrNotExist) {
		if !*updateGolden {
			t.Fatalf("%s missing; run `go test -run TestGolden -update-golden` and commit it", goldenHashFile)
		}
		raw = writeGoldenHashVectors(t)
	} else if err != nil {
		t.Fatal(err)
	}

	var file goldenHashVectors
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(file.NumBlocks, goldenHashNumBlocks) {
		t.Fatalf("num_blocks = %v, goldenHashNumBlocks = %v", file.NumBlocks, goldenHashNumBlocks)
	}
	if len(file.Vectors) == 0 {
		t.Fatal("no hash vectors")
	}

	for _, want := range file.Vectors {
		got := computeGoldenHashVector(want.Len)
		if got.Hi != want.Hi || got.Lo != want.Lo {
			t.Errorf("len %d: xxh3.Hash128 = %s%s, golden %s%s", want.Len, got.Hi, got.Lo, want.Hi, want.Lo)
			continue
		}
		if got.Intra != want.Intra {
			t.Errorf("len %d: intra-block hash = %d, golden %d", want.Len, got.Intra, want.Intra)
		}
		if !slices.Equal(got.Blocks, want.Blocks) {
			t.Errorf("len %d: block indices = %v, golden %v", want.Len, got.Blocks, want.Blocks)
		}

		// Every hashing entry point must agree with the pinned values.
		in := goldenHashInput(want.Len)
		for i, nb := range goldenHashNumBlocks {
			for name, fn := range map[string]func() (uint64, uint32){
				"hashData":         func() (uint64, uint32) { return hashData(in, nb) },
				"hashString":       func() (uint64, uint32) { return hashString(string(in), nb) },
				"hashSplitSharded": func() (uint64, uint32) { return hashSplitSharded(hashRaw128(in), nb) },
				"hashSplitSharded(string)": func() (uint64, uint32) {
					return hashSplitSharded(hashRawString128(string(in)), nb)
				},
			} {
				if b, intra := fn(); b != want.Blocks[i] || intra != want.Intra {
					t.Errorf("len %d, numBlocks %d: %s = (%d, %d), golden (%d, %d)", want.Len, nb, name, b, intra, want.Blocks[i], want.Intra)
				}
			}
		}
	}
}

// writeGoldenHashVectors creates hash_vectors.json with one vector per line for readable
// diffs, and returns its contents.
func writeGoldenHashVectors(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	desc, err := json.Marshal("xxh3.Hash128 of goldenHashInput(len), with foldIntra(lo) and reduceRange(hi, n) for each n in num_blocks")
	if err != nil {
		t.Fatal(err)
	}
	nb, err := json.Marshal(goldenHashNumBlocks)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&b, "{\n  \"description\": %s,\n  \"num_blocks\": %s,\n  \"vectors\": [\n", desc, nb)
	lengths := goldenHashLengths()
	for i, n := range lengths {
		line, err := json.Marshal(computeGoldenHashVector(n))
		if err != nil {
			t.Fatal(err)
		}
		sep := ","
		if i == len(lengths)-1 {
			sep = ""
		}
		fmt.Fprintf(&b, "    %s%s\n", line, sep)
	}
	b.WriteString("  ]\n}\n")
	writeGoldenFile(t, goldenHashFile, b.Bytes())
	return b.Bytes()
}

// TestGoldenPrimePartitions pins the partition sizes for every k. The partitions decide which
// bit each probe sets inside a block, so changing any entry (even reordering it) silently
// breaks every persisted filter with that k. The filter goldens catch this too; this test
// just says so directly.
func TestGoldenPrimePartitions(t *testing.T) {
	// FROZEN: a copy of primePartitions as of serialization format version 1.
	frozen := map[uint32][]uint32{
		3:  {167, 173, 172},
		4:  {109, 127, 137, 139},
		5:  {97, 101, 103, 109, 102},
		6:  {61, 79, 83, 89, 97, 103},
		7:  {61, 67, 71, 79, 83, 89, 62},
		8:  {37, 47, 53, 61, 67, 71, 79, 97},
		9:  {41, 43, 47, 53, 59, 67, 71, 73, 58},
		10: {31, 37, 41, 43, 47, 53, 59, 61, 67, 73},
		11: {29, 31, 37, 41, 43, 44, 47, 53, 59, 61, 67},
		12: {17, 23, 29, 31, 37, 41, 43, 47, 53, 59, 61, 71},
		13: {17, 19, 23, 29, 31, 37, 41, 43, 47, 52, 53, 59, 61},
		14: {11, 13, 17, 19, 23, 29, 31, 37, 41, 47, 53, 59, 61, 71},
		15: {11, 13, 17, 19, 23, 28, 29, 31, 37, 41, 43, 47, 53, 59, 61},
		16: {5, 7, 11, 13, 17, 19, 23, 27, 29, 31, 37, 41, 43, 47, 53, 109},
		17: {3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 32, 37, 41, 43, 47, 53, 101},
	}
	for k, want := range frozen {
		if got := GetPrimePartition(k); !slices.Equal(got, want) {
			t.Errorf("k=%d: partition %v, frozen %v (persisted filters with this k would return false negatives)", k, got, want)
		}
	}
	// New k values may be added; existing ones may never be removed or changed.
	if len(primePartitions) < len(frozen) {
		t.Errorf("primePartitions has %d entries, frozen has %d: a supported k was removed", len(primePartitions), len(frozen))
	}
}

// TestGoldenFormatConstants pins the constants that define the on-disk layout.
func TestGoldenFormatConstants(t *testing.T) {
	if BlockBits != 512 || BlockWords != 8 {
		t.Errorf("BlockBits/BlockWords = %d/%d, want 512/8", BlockBits, BlockWords)
	}
	if headerSize != 21 || checksumSize != 4 {
		t.Errorf("headerSize/checksumSize = %d/%d, want 21/4", headerSize, checksumSize)
	}
	// Bumping the version is allowed, but only together with a new testdata/golden/v<N>
	// directory and a decoder that still reads every older version.
	if _, err := os.Stat(filepath.Join(goldenDir, fmt.Sprintf("v%d", serializeVersion))); err != nil {
		t.Errorf("no golden directory for current serializeVersion %d: %v", serializeVersion, err)
	}
}

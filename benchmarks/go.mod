module github.com/jcalabro/gloom/benchmarks

go 1.23.0

require (
	github.com/bits-and-blooms/bloom/v3 v3.7.1
	github.com/cespare/xxhash/v2 v2.3.0
	github.com/ericvolp12/atomic-bloom v0.0.0-20250509232439-6e045c408512
	github.com/greatroar/blobloom v0.8.1
	github.com/jcalabro/gloom v0.0.0
)

require github.com/bits-and-blooms/bitset v1.24.2 // indirect

replace github.com/jcalabro/gloom => ../

package digest

// CombineCRC64NVME returns the CRC-64/NVME of the concatenation A||B from the
// checksums of A and B and the length of B in bytes. The checksums are final
// values, in the form the S3 headers carry.
//
// The identity crc(A||B) = shift(crc(A), len(B)) xor crc(B) holds because the
// initial and final XOR values of CRC-64/NVME are equal (all ones). shift
// advances a register by len(B) zero bytes, computed here by repeated squaring
// of the GF(2) operator for one bit, so the cost is logarithmic in len(B).
func CombineCRC64NVME(crcA, crcB uint64, lenB int64) uint64 {
	return shiftCRC64NVME(crcA, lenB) ^ crcB
}

// shiftCRC64NVME advances the register crc by n zero bytes. It follows the
// classic zlib crc32_combine construction, adapted to 64 bits.
func shiftCRC64NVME(crc uint64, n int64) uint64 {
	if n <= 0 {
		return crc
	}
	var odd, even [64]uint64
	// odd is the operator for one zero bit: the column for bit 0 is the
	// polynomial, and bit i (i >= 1) moves to bit i-1.
	odd[0] = crc64NVMEPoly
	for i := 1; i < 64; i++ {
		odd[i] = 1 << (i - 1)
	}
	gf2Square(&even, &odd) // two bits
	gf2Square(&odd, &even) // four bits
	for {
		gf2Square(&even, &odd) // one byte
		if n&1 != 0 {
			crc = gf2Times(&even, crc)
		}
		n >>= 1
		if n == 0 {
			return crc
		}
		gf2Square(&odd, &even) // two bytes, four, eight, ...
		if n&1 != 0 {
			crc = gf2Times(&odd, crc)
		}
		n >>= 1
		if n == 0 {
			return crc
		}
	}
}

// gf2Times multiplies the GF(2) matrix mat, given by its columns, by vec.
func gf2Times(mat *[64]uint64, vec uint64) uint64 {
	var sum uint64
	for i := 0; vec != 0; i++ {
		if vec&1 != 0 {
			sum ^= mat[i]
		}
		vec >>= 1
	}
	return sum
}

// gf2Square sets square to mat multiplied by itself.
func gf2Square(square, mat *[64]uint64) {
	for i := range square {
		square[i] = gf2Times(mat, mat[i])
	}
}

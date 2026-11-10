package qr

// Error correction in a QR code is Reed-Solomon over the field of 256
// elements, where a byte stands for a polynomial with bits as coefficients
// and products are taken modulo x^8 + x^4 + x^3 + x^2 + 1, written 0x11D
// (ISO/IEC 18004:2015, 7.5.2).

// gfExp[i] is 2 to the power i in the field and gfLog is its inverse. The
// powers repeat after 255; gfExp holds them twice so the sum of two
// logarithms can be looked up without a modulo.
var gfExp, gfLog = gfTables()

func gfTables() (exp [510]byte, log [256]byte) {
	x := 1
	for i := 0; i < 255; i++ {
		exp[i] = byte(x)
		exp[i+255] = byte(x)
		log[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}
	return exp, log
}

// gfMul multiplies two field elements.
func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// rsGenerator is the generator polynomial for a number of error correction
// codewords: the product of (x - 2^0), (x - 2^1), ... up to 2^(degree-1),
// with the coefficient of the highest power first (ISO/IEC 18004:2015,
// annex A lists the results).
func rsGenerator(degree int) []byte {
	poly := []byte{1}
	for i := 0; i < degree; i++ {
		next := make([]byte, len(poly)+1)
		for j, c := range poly {
			next[j] ^= c
			next[j+1] ^= gfMul(c, gfExp[i])
		}
		poly = next
	}
	return poly
}

// rsRemainder gives the error correction codewords of a block: the
// remainder of the data, taken as a polynomial and shifted up by the degree
// of the generator, divided by the generator.
func rsRemainder(data, generator []byte) []byte {
	remainder := make([]byte, len(generator)-1)
	for _, d := range data {
		factor := d ^ remainder[0]
		copy(remainder, remainder[1:])
		remainder[len(remainder)-1] = 0
		for i := range remainder {
			remainder[i] ^= gfMul(generator[i+1], factor)
		}
	}
	return remainder
}

// addErrorCorrection splits the data codewords into the blocks of a version
// and level, works out the error correction codewords of every block and
// returns all codewords in the order they go into the code (ISO/IEC
// 18004:2015, 7.5.1 and 7.6).
//
// All blocks have the same number of error correction codewords. When the
// data does not divide evenly, the last blocks hold one data codeword more.
// The result takes the first codeword of every block, then the second of
// every block and so on, first through the data and then through the error
// correction codewords, so that a stain on the code hits every block a
// little instead of one block a lot.
func addErrorCorrection(data []byte, version int, level Level) []byte {
	total := totalCodewords[version]
	blocks := ecBlocks(version, level)
	ecPerBlock := ecCodewords(version, level) / blocks
	shortData := len(data) / blocks
	longBlocks := len(data) % blocks
	shortBlocks := blocks - longBlocks

	generator := rsGenerator(ecPerBlock)
	dataParts := make([][]byte, blocks)
	ecParts := make([][]byte, blocks)
	rest := data
	for b := range dataParts {
		n := shortData
		if b >= shortBlocks {
			n++
		}
		dataParts[b] = rest[:n]
		ecParts[b] = rsRemainder(rest[:n], generator)
		rest = rest[n:]
	}

	out := make([]byte, 0, total)
	for i := 0; i <= shortData; i++ {
		for _, part := range dataParts {
			if i < len(part) {
				out = append(out, part[i])
			}
		}
	}
	for i := 0; i < ecPerBlock; i++ {
		for _, part := range ecParts {
			out = append(out, part[i])
		}
	}
	return out
}

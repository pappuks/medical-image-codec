// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.
package mic

import (
	"math/bits"
	"sort"
)

const (
	HUFFMAN_SYMBOLS    = 500
	HUFFMAN_DIV_FACTOR = 200
)

// Symbol frequency/codelength
type SymbFreq struct {
	symbol uint16
	freq   uint32 // Also stores code length
}

type CanHuffmanCompressU16 struct {
	maxValue                       uint16
	pixelDepth                     uint8
	delimiterForCompressDecompress uint16
	in                             []uint16
	symbolsOfInterestList          []SymbFreq
	maxCodeLength                  uint8
	symbolsPerCodeLength           []uint32
	symbolStartPerCodeLength       []uint32
	canHuffmanTable                []uint32
	indexOfDelimiter               int
	bw                             bitWriterHuff
	Out                            []byte
	allSymbols                     []SymbolLenDelimiter
	delimiterCode                  uint32
	delimiterCodeLength            uint32
}

type SymbolLenDelimiter struct {
	codeLen   uint8
	delimiter bool
	code      uint32
}

func (c *CanHuffmanCompressU16) Init(in []uint16) {
	c.in = in
	c.Out = make([]byte, 0, len(c.in)*2)
	c.bw.reset(c.Out)
}

func (c *CanHuffmanCompressU16) Compress() {
	c.GenerateFrequencies()
	c.OptimizeSymbolCount()
	c.AddDelimiterToSymbolList()
	c.GenerateCanHuffmanTable()
	//fmt.Println("SymbolList", len(c.symbolsOfInterestList), "MaxCodeLen", c.maxCodeLength)
	c.FindIndexOfDelimiter()
	c.WriteTable()
	c.GenerateAllSymbolTable()

	if c.pixelDepth+c.maxCodeLength > 32 {
		panic("PixelLength + MaxCodelength is creater than 32 bits")
	}

	for i := 0; i < len(c.in); i++ {
		currentSymbol := c.in[i]
		found := c.allSymbols[currentSymbol]
		//fmt.Println(currentSymbol, found, strconv.FormatInt(int64(found.code), 2))
		// Add code
		c.bw.addBits32(found.code, found.codeLen)
		if found.delimiter {
			// Write the symbol itself
			c.bw.addBits32(uint32(currentSymbol), uint8(c.pixelDepth))
			//fmt.Println("Symbol", strconv.FormatInt(int64(currentSymbol), 2), c.pixelDepth)
		}
	}
	// Flush with zeros at the end of the stream
	c.bw.addBits32(0, uint8(c.maxCodeLength+c.pixelDepth))
	c.bw.flushAlign()
	c.Out = c.bw.out
}

func (c *CanHuffmanCompressU16) GenerateAllSymbolTable() {
	c.allSymbols = make([]SymbolLenDelimiter, 1<<c.pixelDepth)

	for i := 0; i < len(c.allSymbols); i++ {
		var v SymbolLenDelimiter
		found := false
		if i != int(c.delimiterForCompressDecompress) {
			for j, w := range c.symbolsOfInterestList {
				if i == int(w.symbol) {
					v.delimiter = false
					v.codeLen = uint8(w.freq)
					v.code = c.canHuffmanTable[j]
					found = true
					break
				}
			}
		}
		if !found {
			v.delimiter = true
			v.code = c.delimiterCode
			v.codeLen = uint8(c.delimiterCodeLength)
		}
		c.allSymbols[i] = v
	}
}

func (c *CanHuffmanCompressU16) FindIndexOfDelimiter() {
	for i, v := range c.symbolsOfInterestList {
		if v.symbol == c.delimiterForCompressDecompress {
			c.indexOfDelimiter = i
			break
		}
	}
	c.delimiterCodeLength = c.symbolsOfInterestList[c.indexOfDelimiter].freq
	c.delimiterCode = c.canHuffmanTable[c.indexOfDelimiter]
}

func (c *CanHuffmanCompressU16) WriteTable() {
	// Write uncompressed size
	c.bw.addBits32(uint32(len(c.in)), 32)
	// Write maxValue
	c.bw.addBits16(c.maxValue, 16)
	//Max code len
	c.bw.addBits16(uint16(c.maxCodeLength), 8)
	// Write size of symbols of interest, can't be more than 2^16
	c.bw.addBits16(uint16(len(c.symbolsOfInterestList)), 16)
	// Store the symbol list
	for _, v := range c.symbolsOfInterestList {
		c.bw.addBits16(v.symbol, uint8(c.pixelDepth))
	}
	// Store symbol vs code length
	maxCodeLenBits := bits.Len8(c.maxCodeLength)
	for _, v := range c.symbolsOfInterestList {
		c.bw.addBits32(v.freq, uint8(maxCodeLenBits))
	}
}

func (c *CanHuffmanCompressU16) GenerateFrequencies() {
	regions := uint32(1 << 16)
	distributionArray := make([]uint32, regions)

	c.maxValue = 0
	for _, v := range c.in {
		distributionArray[v]++
		if v > c.maxValue {
			c.maxValue = v
		}
	}

	c.pixelDepth = uint8(bits.Len16(c.maxValue))
	c.delimiterForCompressDecompress = uint16((1 << (c.pixelDepth)) - 1)
	regions = uint32(1 << c.pixelDepth)

	for i := uint32(0); i < regions; i++ {
		if distributionArray[i] > 0 {
			if uint16(i) != c.delimiterForCompressDecompress {
				c.symbolsOfInterestList = append(c.symbolsOfInterestList, SymbFreq{uint16(i), distributionArray[i]})
			}
		}
	}

	sort.Slice(c.symbolsOfInterestList, func(i, j int) bool {
		return c.symbolsOfInterestList[i].freq > c.symbolsOfInterestList[j].freq
	}) // Sort in descending order
}

func (c *CanHuffmanCompressU16) OptimizeSymbolCount() {
	// // Take only symbols which fall within 1/100 of the max freq symbol.
	// maxFrequency := c.symbolsOfInterestList[0].freq

	// minAllowedFrequency := uint32(maxFrequency / HUFFMAN_DIV_FACTOR)

	// for i := 0; i < len(c.symbolsOfInterestList); i++ {
	// 	if c.symbolsOfInterestList[i].freq < minAllowedFrequency {
	// 		c.symbolsOfInterestList = c.symbolsOfInterestList[0:i] // remove elements with low frequency
	// 		break
	// 	}
	// }

	// // Take only the first 500 symbols
	// if len(c.symbolsOfInterestList) > HUFFMAN_SYMBOLS {
	// 	c.symbolsOfInterestList = c.symbolsOfInterestList[0:HUFFMAN_SYMBOLS] // remove elements more than HUFFMAN_SYMBOLS
	// }

	length := len(c.symbolsOfInterestList)
	for ; length > 0; length-- {
		tempList := make([]SymbFreq, length)
		copy(tempList, c.symbolsOfInterestList)
		codeLen := c.CalculateCodeLengthForGivenSlice(tempList)
		if codeLen <= 14 {
			break
		}
	}

	c.symbolsOfInterestList = c.symbolsOfInterestList[0:length]
}

func (c *CanHuffmanCompressU16) AddDelimiterToSymbolList() {
	// Add the delimiter correctly -- START
	selectedSymbolCount := uint32(0)
	for i := 0; i < len(c.symbolsOfInterestList); i++ {
		selectedSymbolCount += c.symbolsOfInterestList[i].freq
	}

	delimiterCount := uint32(len(c.in)) - selectedSymbolCount
	c.symbolsOfInterestList = append(c.symbolsOfInterestList, SymbFreq{uint16(c.delimiterForCompressDecompress), delimiterCount})

	// Sort again after adding delimiter symbol
	sort.Slice(c.symbolsOfInterestList, func(i, j int) bool {
		return c.symbolsOfInterestList[i].freq > c.symbolsOfInterestList[j].freq
	}) // Sort in descending order

	// Add the delimiter correctly -- END
}

func (c *CanHuffmanCompressU16) GenerateCanHuffmanTable() {
	c.CalculateCodeLength()
	c.CalculateSymbolsPerCodeLength()
	c.CalculateSymbolStartForCodeLength()
	c.ConstructCanHuffmanTable()
}

func (c *CanHuffmanCompressU16) CalculateCodeLengthForGivenSlice(f []SymbFreq) uint32 {
	sort.Slice(f, func(i, j int) bool {
		return f[i].freq < f[j].freq
	}) // Sort in ascending order

	count := len(f)

	// Minimim redudancy code evaluation algorithm written by Alister Moffat and Jyrki Katajainen
	// This code calculates the code lengths in place.
	// http://www.cs.mu.oz.au/~alistair/inplace.c

	//int root;                  /* next root node to be used */
	//int leaf;                  /* next leaf to be used */
	//int next;                  /* next value to be assigned */
	//int avbl;                  /* number of available nodes */
	//int used;                  /* number of internal nodes */
	//int dpth;                  /* current depth of leaves */

	// Check for boundary conditions
	if count == 0 {
		return 0
	}
	if count == 1 {
		// Set the required code lenght as 0
		f[0].freq = 0
		return 0
	}

	// First pass
	f[0].freq += f[1].freq
	root := 0
	leaf := 2

	for next := 1; next < count-1; next++ {
		// Select first item for pairing
		if (leaf >= count) || (f[root].freq < f[leaf].freq) {
			f[next].freq = f[root].freq
			f[root].freq = uint32(next)
			root++
		} else {
			f[next].freq = f[leaf].freq
			leaf++
		}
		// Add on the second item
		if (leaf >= count) || ((root < next) && (f[root].freq < f[leaf].freq)) {
			f[next].freq += f[root].freq
			f[root].freq = uint32(next)
			root++
		} else {
			f[next].freq += f[leaf].freq
			leaf++
		}
	}

	// Second pass, right to left
	f[count-2].freq = 0
	for next := count - 3; next >= 0; next-- {
		f[next].freq = f[f[next].freq].freq + 1
	}

	// Third pass, right to left
	avbl := 1
	used := 0
	dpth := uint32(0)
	root = count - 2
	next := count - 1
	for avbl > 0 {
		for (root >= 0) && (f[root].freq == dpth) {
			used++
			root--
		}
		for avbl > used {
			f[next].freq = dpth
			next--
			avbl--
		}
		avbl = 2 * used
		dpth++
		used = 0
	}

	// Alister Moffat code ends

	return f[0].freq
}

func (c *CanHuffmanCompressU16) CalculateCodeLength() {
	c.maxCodeLength = uint8(c.CalculateCodeLengthForGivenSlice(c.symbolsOfInterestList))
}

func (c *CanHuffmanCompressU16) CalculateSymbolsPerCodeLength() {
	c.symbolsPerCodeLength = make([]uint32, c.maxCodeLength+1)
	for i := 0; i < len(c.symbolsOfInterestList); i++ {
		c.symbolsPerCodeLength[c.symbolsOfInterestList[i].freq]++
	}
}

func (c *CanHuffmanCompressU16) CalculateSymbolStartForCodeLength() {
	c.symbolStartPerCodeLength = make([]uint32, c.maxCodeLength+1)
	symbolStart := uint32(0)
	prevCodeLength := uint8(0)
	numberOfSymbolsForPrevCodeLength := uint32(0)
	for i := uint8(1); i < (c.maxCodeLength + 1); i++ {
		numberOfSymbols := c.symbolsPerCodeLength[i]
		if numberOfSymbols != 0 {
			if prevCodeLength == 0 {
				c.symbolStartPerCodeLength[i] = symbolStart
				prevCodeLength = i
				numberOfSymbolsForPrevCodeLength = numberOfSymbols
			} else {
				c.symbolStartPerCodeLength[i] =
					(c.symbolStartPerCodeLength[prevCodeLength] +
						numberOfSymbolsForPrevCodeLength) << (i - prevCodeLength)
				prevCodeLength = i
				numberOfSymbolsForPrevCodeLength = numberOfSymbols
			}
		}
	}
}

func (c *CanHuffmanCompressU16) ConstructCanHuffmanTable() {
	numberOfSymbols := len(c.symbolsOfInterestList)
	c.canHuffmanTable = make([]uint32, numberOfSymbols)
	copyOfSymbolStartPerCodeLength := make([]uint32, c.maxCodeLength+1)
	copy(copyOfSymbolStartPerCodeLength, c.symbolStartPerCodeLength)
	for i := 0; i < numberOfSymbols; i++ {
		c.canHuffmanTable[i] = copyOfSymbolStartPerCodeLength[c.symbolsOfInterestList[i].freq]
		copyOfSymbolStartPerCodeLength[c.symbolsOfInterestList[i].freq]++
	}
}

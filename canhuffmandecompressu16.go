// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.
package mic

import (
	"fmt"
	"math/bits"
)

type CanHuffmanDecompressU16 struct {
	c                           CanHuffmanCompressU16
	in                          []byte
	br                          bitReaderHuff
	Out                         []uint16
	maxCodeLengthBits           uint32
	bitsUsed                    uint8
	maxCodeLengthMask           uint32
	pixelDepthMask              uint32
	maxCodePlusPixelDepthMask   uint32
	maxCodeLengthAndPixelDepth  uint8
	maxMinusDelimiterCodeLength uint8
	codeToSymbolTable           []CodeToSymbol
}

type CodeToSymbol struct {
	symbol      uint16
	codeLen     uint8
	isDelimiter bool
}

func (d *CanHuffmanDecompressU16) Init(in []byte) {
	d.in = in
	d.br.initFwd(d.in)
}

func (d *CanHuffmanDecompressU16) ReadTable() {
	decompLength := d.br.getBits32NFillFwd(32)
	d.Out = make([]uint16, decompLength)
	d.c.maxValue = d.br.getBitsNFillFwd(16)
	d.c.pixelDepth = uint8(bits.Len16(d.c.maxValue))
	d.pixelDepthMask = 0xffffffff >> (32 - d.c.pixelDepth)
	d.c.delimiterForCompressDecompress = uint16((1 << (d.c.pixelDepth)) - 1)

	d.c.maxCodeLength = uint8(d.br.getBitsNFillFwd(8))
	maxCodeLenBits := bits.Len8(d.c.maxCodeLength)
	d.maxCodeLengthMask = 0xffffffff >> (32 - d.c.maxCodeLength)
	d.maxCodeLengthAndPixelDepth = uint8(d.c.maxCodeLength) + uint8(d.c.pixelDepth)
	d.maxCodePlusPixelDepthMask = 0xffffffff >> (32 - d.maxCodeLengthAndPixelDepth)

	numOfSymbolsOfInterest := d.br.getBitsNFillFwd(16)
	d.c.symbolsOfInterestList = make([]SymbFreq, numOfSymbolsOfInterest)
	for i := uint16(0); i < numOfSymbolsOfInterest; i++ {
		d.c.symbolsOfInterestList[i].symbol = d.br.getBitsNFillFwd(uint8(d.c.pixelDepth))
	}
	for i := uint16(0); i < numOfSymbolsOfInterest; i++ {
		d.c.symbolsOfInterestList[i].freq = d.br.getBits32NFillFwd(uint8(maxCodeLenBits))
	}

	d.c.CalculateSymbolsPerCodeLength()
	d.c.CalculateSymbolStartForCodeLength()
	d.c.ConstructCanHuffmanTable()

	d.codeToSymbolTable = make([]CodeToSymbol, 1<<d.c.maxCodeLength)

	// Populate the code to symbol table
	for j := uint16(0); j < numOfSymbolsOfInterest; j++ {
		symbLen := d.c.symbolsOfInterestList[j]
		leftShitedCode := d.c.canHuffmanTable[j] << (d.c.maxCodeLength - uint8(symbLen.freq))
		for i := uint32(0); i < 1<<(d.c.maxCodeLength-uint8(symbLen.freq)); i++ {
			d.codeToSymbolTable[leftShitedCode+i].symbol = symbLen.symbol
			d.codeToSymbolTable[leftShitedCode+i].codeLen = uint8(symbLen.freq)
			d.codeToSymbolTable[leftShitedCode+i].isDelimiter = symbLen.symbol == d.c.delimiterForCompressDecompress
		}
		if symbLen.symbol == d.c.delimiterForCompressDecompress {
			d.maxMinusDelimiterCodeLength = uint8(d.c.maxCodeLength) - uint8(symbLen.freq)
		}
	}

}

func (d *CanHuffmanDecompressU16) DecompressInit() {
	if d.c.pixelDepth+d.c.maxCodeLength > 32 {
		panic("PixelLength + MaxCodelength is creater than 32 bits")
	}
	d.maxCodeLengthBits = d.br.getBits32NFillFwd(d.maxCodeLengthAndPixelDepth)
}

func (d *CanHuffmanDecompressU16) Decompress() {
	d.DecompressInit()

	outCounter := 0
	for outCounter < (len(d.Out) - 4) {
		d.Out[outCounter] = d.DecodeNextFast()
		outCounter++
		d.Out[outCounter] = d.DecodeNextFast()
		outCounter++
		d.Out[outCounter] = d.DecodeNextFast()
		outCounter++
		d.Out[outCounter] = d.DecodeNextFast()
		outCounter++
	}

	for outCounter < len(d.Out) {
		d.Out[outCounter] = d.DecodeNext()
		outCounter++
	}

}

func (d *CanHuffmanDecompressU16) DecodeNextFast() uint16 {
	outputSymbol := d.JustDecodeNext()
	d.GetNextMaxCodeLenPlusPixelDepthBitsFast()
	return outputSymbol
}

func (d *CanHuffmanDecompressU16) DecodeNext() uint16 {
	outputSymbol := d.JustDecodeNext()
	d.GetNextMaxCodeLenPlusPixelDepthBits()
	return outputSymbol
}

func (d *CanHuffmanDecompressU16) JustDecodeNext() uint16 {
	// d.maxCodeLengthBits contains values equal to maxCodeLength + PixelDepth, so shift and get
	// only the maxCodeLen bits
	maxCodeLengthValue := (d.maxCodeLengthBits >> uint32(d.c.pixelDepth)) & d.maxCodeLengthMask
	codeToSymb := d.codeToSymbolTable[maxCodeLengthValue]
	outputSymbol := codeToSymb.symbol
	outputCodeLength := codeToSymb.codeLen
	if codeToSymb.isDelimiter {
		symbolAfterDelimiter := d.GetSymbolAfterDelimiter()
		outputSymbol = symbolAfterDelimiter
		outputCodeLength += d.c.pixelDepth
	}
	d.bitsUsed += outputCodeLength

	return outputSymbol
}

func (d *CanHuffmanDecompressU16) GetDelimiterMaskForCurrentSymbol(currentSymbol uint16) (uint16, uint16) {
	retMask := uint16(((int(currentSymbol) ^ int(d.c.delimiterForCompressDecompress)) - 1) >> 16)
	return retMask, uint16((int(retMask) - 1) >> 16)
}

func (d *CanHuffmanDecompressU16) GetSymbolAfterDelimiter() uint16 {
	return uint16((d.maxCodeLengthBits >> d.maxMinusDelimiterCodeLength) & d.pixelDepthMask)
}

func (d *CanHuffmanDecompressU16) GetSymbolAfterDelimiterWithMask(mask uint16) uint16 {
	return mask & uint16((d.maxCodeLengthBits>>d.maxMinusDelimiterCodeLength)&d.pixelDepthMask)
}

func (d *CanHuffmanDecompressU16) GetNextMaxCodeLenPlusPixelDepthBits() {
	d.maxCodeLengthBits = (d.maxCodeLengthBits << d.bitsUsed) & d.maxCodePlusPixelDepthMask
	d.maxCodeLengthBits |= d.br.getBits32NFillFwd((d.bitsUsed))
	d.bitsUsed = 0
}

func (d *CanHuffmanDecompressU16) GetNextMaxCodeLenPlusPixelDepthBitsFast() {
	d.maxCodeLengthBits = (d.maxCodeLengthBits << d.bitsUsed) & d.maxCodePlusPixelDepthMask
	d.maxCodeLengthBits |= d.br.getBits32NFillFwdFast((d.bitsUsed))
	d.bitsUsed = 0
}

func (d *CanHuffmanDecompressU16) GetNextMaxCodeLenBits() {
	if d.bitsUsed > 0 {
		d.maxCodeLengthBits = (d.maxCodeLengthBits << d.bitsUsed) & d.maxCodeLengthMask
		d.maxCodeLengthBits |= d.br.getBits32NFillFwd((d.bitsUsed))
		d.bitsUsed = 0
	} else if d.bitsUsed == 0 {
		d.maxCodeLengthBits |= d.br.getBits32NFillFwd((d.c.maxCodeLength))
	}
}

func (d *CanHuffmanDecompressU16) GetNextPixelDepthBits() {
	if d.bitsUsed > 0 {
		d.maxCodeLengthBits = (d.maxCodeLengthBits << ((d.c.pixelDepth) - (d.c.maxCodeLength - d.bitsUsed))) & d.pixelDepthMask
		d.maxCodeLengthBits |= d.br.getBits32NFillFwd(((d.c.pixelDepth) - (d.c.maxCodeLength - d.bitsUsed)))
		d.bitsUsed = 0
	} else if d.bitsUsed == 0 {
		fmt.Printf("***Error case with bitsUsed = 0\n")
		d.maxCodeLengthBits |= d.br.getBits32NFillFwd((d.c.pixelDepth - (d.c.maxCodeLength)))
	}

}

//go:build js && wasm

// mic-wasm provides Go WebAssembly bindings for the MIC decoder.
// Build: GOOS=js GOARCH=wasm go build -o mic-decoder.wasm ./cmd/mic-wasm/
package main

import (
	"encoding/binary"
	"math/bits"
	"mic"
	"syscall/js"
)

// decodeDeltaRleFSE decodes FSE-compressed Delta+RLE data to original pixels.
// Args: compressedBytes (Uint8Array), width (number), height (number)
// Returns: Uint16Array of decoded pixel data
func decodeDeltaRleFSE(_ js.Value, args []js.Value) interface{} {
	if len(args) < 3 {
		return jsError("decodeDeltaRleFSE requires 3 args: compressedBytes, width, height")
	}

	jsBytes := args[0]
	width := args[1].Int()
	height := args[2].Int()

	// Copy bytes from JS to Go
	length := jsBytes.Length()
	compressed := make([]byte, length)
	js.CopyBytesToGo(compressed, jsBytes)

	// FSE decompress
	var s mic.ScratchU16
	rleSymbols, err := mic.FSEDecompressU16(compressed, &s)
	if err != nil {
		return jsError("FSE decompress: " + err.Error())
	}

	// Delta+RLE decompress
	var drd mic.DeltaRleDecompressU16
	drd.Decompress(rleSymbols, width, height)

	// Convert uint16 slice to JS Uint16Array
	result := js.Global().Get("Uint16Array").New(len(drd.Out))
	for i, v := range drd.Out {
		result.SetIndex(i, int(v))
	}

	return result
}

// decodeMicFile decodes a .mic container file.
// Args: fileBytes (Uint8Array)
// Returns: {pixels: Uint16Array, width: number, height: number}
func decodeMicFile(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return jsError("decodeMicFile requires 1 arg: fileBytes")
	}

	jsBytes := args[0]
	length := jsBytes.Length()
	data := make([]byte, length)
	js.CopyBytesToGo(data, jsBytes)

	if length < 20 {
		return jsError("file too small")
	}

	magic := string(data[0:4])
	if magic != "MIC1" {
		return jsError("invalid .mic magic")
	}

	width := int(binary.LittleEndian.Uint32(data[4:8]))
	height := int(binary.LittleEndian.Uint32(data[8:12]))
	pipeline := binary.LittleEndian.Uint32(data[12:16])
	compLen := binary.LittleEndian.Uint32(data[16:20])

	if pipeline != 1 {
		return jsError("unsupported pipeline type")
	}

	compressed := data[20 : 20+compLen]

	var s mic.ScratchU16
	rleSymbols, err := mic.FSEDecompressU16(compressed, &s)
	if err != nil {
		return jsError("FSE decompress: " + err.Error())
	}

	var drd mic.DeltaRleDecompressU16
	drd.Decompress(rleSymbols, width, height)

	pixels := js.Global().Get("Uint16Array").New(len(drd.Out))
	for i, v := range drd.Out {
		pixels.SetIndex(i, int(v))
	}

	result := js.Global().Get("Object").New()
	result.Set("pixels", pixels)
	result.Set("width", width)
	result.Set("height", height)
	return result
}

// fseDecompress performs only the FSE decompression step.
// Args: compressedBytes (Uint8Array)
// Returns: Uint16Array
func fseDecompress(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return jsError("fseDecompress requires 1 arg: compressedBytes")
	}

	jsBytes := args[0]
	compressed := make([]byte, jsBytes.Length())
	js.CopyBytesToGo(compressed, jsBytes)

	var s mic.ScratchU16
	symbols, err := mic.FSEDecompressU16(compressed, &s)
	if err != nil {
		return jsError("FSE decompress: " + err.Error())
	}

	result := js.Global().Get("Uint16Array").New(len(symbols))
	for i, v := range symbols {
		result.SetIndex(i, int(v))
	}
	return result
}

// deltaDecompress performs delta decompression on uint16 data.
// Args: deltaData (Uint16Array), width, height
// Returns: Uint16Array
func deltaDecompress(_ js.Value, args []js.Value) interface{} {
	if len(args) < 3 {
		return jsError("deltaDecompress requires 3 args")
	}

	jsArr := args[0]
	width := args[1].Int()
	height := args[2].Int()

	input := make([]uint16, jsArr.Length())
	for i := range input {
		input[i] = uint16(jsArr.Index(i).Int())
	}

	output := mic.DeltaDecompressU16(input, width, height)

	result := js.Global().Get("Uint16Array").New(len(output))
	for i, v := range output {
		result.SetIndex(i, int(v))
	}
	return result
}

// getVersion returns codec version info.
func getVersion(_ js.Value, _ []js.Value) interface{} {
	_ = bits.Len16 // ensure import
	return "MIC WASM Decoder v1.0 (Delta+RLE+FSE, 16-bit)"
}

func jsError(msg string) interface{} {
	return js.Global().Get("Error").New(msg)
}

func main() {
	// Register functions on the global MICWasm object
	micWasm := js.Global().Get("Object").New()
	micWasm.Set("decode", js.FuncOf(decodeDeltaRleFSE))
	micWasm.Set("decodeFile", js.FuncOf(decodeMicFile))
	micWasm.Set("fseDecompress", js.FuncOf(fseDecompress))
	micWasm.Set("deltaDecompress", js.FuncOf(deltaDecompress))
	micWasm.Set("version", js.FuncOf(getVersion))
	js.Global().Set("MICWasm", micWasm)

	// Signal that WASM is ready
	readyEvent := js.Global().Get("CustomEvent").New("mic-wasm-ready")
	js.Global().Get("document").Call("dispatchEvent", readyEvent)

	// Keep the Go runtime alive
	select {}
}

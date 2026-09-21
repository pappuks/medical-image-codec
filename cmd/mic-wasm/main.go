//go:build js && wasm

// mic-wasm provides Go WebAssembly bindings for the MIC decoder.
// Build: GOOS=js GOARCH=wasm go build -o mic-decoder.wasm ./cmd/mic-wasm/
package main

import (
	"encoding/binary"
	"math/bits"
	"mic"
	"strconv"
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

// decodeMicFile decodes a .mic container file (MIC1 or MIC2).
// Args: fileBytes (Uint8Array)
// Returns: {pixels: Uint16Array, width: number, height: number}
//
//	For MIC2: also includes frameCount, temporal, isMIC2 fields.
//	Returns first frame pixels for MIC2.
func decodeMicFile(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return jsError("decodeMicFile requires 1 arg: fileBytes")
	}

	jsBytes := args[0]
	length := jsBytes.Length()
	data := make([]byte, length)
	js.CopyBytesToGo(data, jsBytes)

	if length < 4 {
		return jsError("file too small")
	}

	magic := string(data[0:4])

	if magic == "PICS" {
		pixels, width, height, err := mic.DecompressParallelStrips(data)
		if err != nil {
			return jsError("PICS decompress: " + err.Error())
		}
		numStrips := int(binary.LittleEndian.Uint32(data[12:16]))
		result := js.Global().Get("Object").New()
		result.Set("pixels", uint16SliceToJS(pixels))
		result.Set("width", width)
		result.Set("height", height)
		result.Set("isPICS", true)
		result.Set("numStrips", numStrips)
		return result
	}

	if magic == "MIC3" {
		return decodeMIC3FileImpl(data)
	}

	if magic == "MIC2" {
		return decodeMIC2FileImpl(data)
	}

	if magic != "MIC1" {
		return jsError("invalid .mic magic: " + magic)
	}

	if length < 20 {
		return jsError("MIC1 file too small")
	}

	width := int(binary.LittleEndian.Uint32(data[4:8]))
	height := int(binary.LittleEndian.Uint32(data[8:12]))
	pipeline := binary.LittleEndian.Uint32(data[12:16])
	compLen := binary.LittleEndian.Uint32(data[16:20])

	if pipeline != 1 {
		return jsError("unsupported pipeline type")
	}

	compressed := data[20 : 20+compLen]

	pixels, err := mic.DecompressSingleFrame(compressed, width, height)
	if err != nil {
		return jsError("decompress: " + err.Error())
	}

	result := js.Global().Get("Object").New()
	result.Set("pixels", uint16SliceToJS(pixels))
	result.Set("width", width)
	result.Set("height", height)
	result.Set("isMIC2", false)
	return result
}

// decodeMIC2FileImpl handles MIC2 multiframe containers.
func decodeMIC2FileImpl(data []byte) interface{} {
	hdr, _, _, err := mic.ReadMIC2Header(data)
	if err != nil {
		return jsError("MIC2 header: " + err.Error())
	}

	// Decode first frame
	pixels, _, err := mic.DecompressFrame(data, 0)
	if err != nil {
		return jsError("MIC2 frame 0: " + err.Error())
	}

	result := js.Global().Get("Object").New()
	result.Set("pixels", uint16SliceToJS(pixels))
	result.Set("width", hdr.Width)
	result.Set("height", hdr.Height)
	result.Set("frameCount", hdr.FrameCount)
	result.Set("temporal", hdr.Temporal)
	result.Set("isMIC2", true)
	return result
}

// parseMIC2Header parses header metadata without decompressing.
// Args: fileBytes (Uint8Array)
// Returns: {width, height, frameCount, temporal}
func parseMIC2Header(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return jsError("parseMIC2Header requires 1 arg: fileBytes")
	}

	jsBytes := args[0]
	data := make([]byte, jsBytes.Length())
	js.CopyBytesToGo(data, jsBytes)

	hdr, _, _, err := mic.ReadMIC2Header(data)
	if err != nil {
		return jsError("MIC2 header: " + err.Error())
	}

	result := js.Global().Get("Object").New()
	result.Set("width", hdr.Width)
	result.Set("height", hdr.Height)
	result.Set("frameCount", hdr.FrameCount)
	result.Set("temporal", hdr.Temporal)
	return result
}

// decodeMIC2Frame decodes a single frame from a MIC2 file.
// Args: fileBytes (Uint8Array), frameIndex (number)
// Returns: {pixels: Uint16Array, width: number, height: number}
func decodeMIC2Frame(_ js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return jsError("decodeMIC2Frame requires 2 args: fileBytes, frameIndex")
	}

	jsBytes := args[0]
	frameIdx := args[1].Int()

	data := make([]byte, jsBytes.Length())
	js.CopyBytesToGo(data, jsBytes)

	pixels, hdr, err := mic.DecompressFrame(data, frameIdx)
	if err != nil {
		return jsError("MIC2 frame " + strconv.Itoa(frameIdx) + ": " + err.Error())
	}

	result := js.Global().Get("Object").New()
	result.Set("pixels", uint16SliceToJS(pixels))
	result.Set("width", hdr.Width)
	result.Set("height", hdr.Height)
	return result
}

// decodePICSFile decodes a PICS parallel-strip container.
// Args: fileBytes (Uint8Array)
// Returns: {pixels: Uint16Array, width: number, height: number, isPICS: true, numStrips: number}
func decodePICSFile(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return jsError("decodePICSFile requires 1 arg: fileBytes")
	}

	jsBytes := args[0]
	data := make([]byte, jsBytes.Length())
	js.CopyBytesToGo(data, jsBytes)

	pixels, width, height, err := mic.DecompressParallelStrips(data)
	if err != nil {
		return jsError("PICS decompress: " + err.Error())
	}

	numStrips := int(binary.LittleEndian.Uint32(data[12:16]))

	result := js.Global().Get("Object").New()
	result.Set("pixels", uint16SliceToJS(pixels))
	result.Set("width", width)
	result.Set("height", height)
	result.Set("isPICS", true)
	result.Set("numStrips", numStrips)
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

	return uint16SliceToJS(symbols)
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

	return uint16SliceToJS(output)
}

// decodeMIC3FileImpl handles MIC3 WSI containers.
func decodeMIC3FileImpl(data []byte) interface{} {
	hdr, err := mic.ReadWSIHeader(data)
	if err != nil {
		return jsError("MIC3 header: " + err.Error())
	}

	// Decompress full level 0
	lv := hdr.Levels[0]
	rgb, err := mic.DecompressWSIRegion(data, 0, 0, 0, lv.Width, lv.Height)
	if err != nil {
		return jsError("MIC3 level 0: " + err.Error())
	}

	result := js.Global().Get("Object").New()
	jsRGB := js.Global().Get("Uint8Array").New(len(rgb))
	js.CopyBytesToJS(jsRGB, rgb)
	result.Set("rgb", jsRGB)
	result.Set("width", lv.Width)
	result.Set("height", lv.Height)
	result.Set("channels", hdr.Channels)
	result.Set("isMIC3", true)

	// Level info
	levelsArr := js.Global().Get("Array").New(len(hdr.Levels))
	for i, l := range hdr.Levels {
		lvObj := js.Global().Get("Object").New()
		lvObj.Set("width", l.Width)
		lvObj.Set("height", l.Height)
		lvObj.Set("tilesX", l.TilesX)
		lvObj.Set("tilesY", l.TilesY)
		levelsArr.SetIndex(i, lvObj)
	}
	result.Set("levels", levelsArr)
	result.Set("tileWidth", hdr.TileWidth)
	result.Set("tileHeight", hdr.TileHeight)
	result.Set("levelCount", len(hdr.Levels))

	return result
}

// decodeWSILevel decodes all tiles at a specific pyramid level.
// Args: fileBytes (Uint8Array), levelIndex (number)
// Returns: {rgb: Uint8Array, width: number, height: number}
func decodeWSILevel(_ js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return jsError("decodeWSILevel requires 2 args: fileBytes, levelIndex")
	}

	jsBytes := args[0]
	levelIdx := args[1].Int()

	data := make([]byte, jsBytes.Length())
	js.CopyBytesToGo(data, jsBytes)

	hdr, err := mic.ReadWSIHeader(data)
	if err != nil {
		return jsError("MIC3 header: " + err.Error())
	}

	if levelIdx < 0 || levelIdx >= len(hdr.Levels) {
		return jsError("MIC3: level " + strconv.Itoa(levelIdx) + " out of range")
	}

	lv := hdr.Levels[levelIdx]
	rgb, err := mic.DecompressWSIRegion(data, levelIdx, 0, 0, lv.Width, lv.Height)
	if err != nil {
		return jsError("MIC3 level " + strconv.Itoa(levelIdx) + ": " + err.Error())
	}

	result := js.Global().Get("Object").New()
	jsRGB := js.Global().Get("Uint8Array").New(len(rgb))
	js.CopyBytesToJS(jsRGB, rgb)
	result.Set("rgb", jsRGB)
	result.Set("width", lv.Width)
	result.Set("height", lv.Height)
	return result
}

// getVersion returns codec version info.
func getVersion(_ js.Value, _ []js.Value) interface{} {
	_ = bits.Len16 // ensure import
	return "MIC WASM Decoder v3.1 (Delta+RLE+FSE, 16-bit, MIC1+MIC2+MIC3 WSI+PICS)"
}

func uint16SliceToJS(data []uint16) js.Value {
	result := js.Global().Get("Uint16Array").New(len(data))
	for i, v := range data {
		result.SetIndex(i, int(v))
	}
	return result
}

func jsError(msg string) interface{} {
	return js.Global().Get("Error").New(msg)
}

func main() {
	// Register functions on the global MICWasm object
	micWasm := js.Global().Get("Object").New()
	micWasm.Set("decode", js.FuncOf(decodeDeltaRleFSE))
	micWasm.Set("decodeFile", js.FuncOf(decodeMicFile))
	micWasm.Set("decodePICS", js.FuncOf(decodePICSFile))
	micWasm.Set("fseDecompress", js.FuncOf(fseDecompress))
	micWasm.Set("deltaDecompress", js.FuncOf(deltaDecompress))
	micWasm.Set("parseMIC2Header", js.FuncOf(parseMIC2Header))
	micWasm.Set("decodeFrame", js.FuncOf(decodeMIC2Frame))
	micWasm.Set("decodeWSILevel", js.FuncOf(decodeWSILevel))
	micWasm.Set("version", js.FuncOf(getVersion))
	js.Global().Set("MICWasm", micWasm)

	// Signal that WASM is ready
	readyEvent := js.Global().Get("CustomEvent").New("mic-wasm-ready")
	js.Global().Get("document").Call("dispatchEvent", readyEvent)

	// Keep the Go runtime alive
	select {}
}

# MIC - Medical Image Codec
This library introduces a lossless medical image compression codec __MIC__ for 16 bit images implemented in __Go__ which provides compression ratio similar to JPEG 2000 but with much higher speed of compression and decompression.

|Branch |Status |
|-------|-------|
|main |![example workflow](https://github.com/pappuks/medical-image-codec/actions/workflows/go.yml/badge.svg)

## Compression Algorithm
The compression algorithm uses a combination of [Delta Encoding](https://en.wikipedia.org/wiki/Delta_encoding), [RLE](https://en.wikipedia.org/wiki/Run-length_encoding) and [Huffman Coding](https://en.wikipedia.org/wiki/Huffman_coding) or [Finite State Entropy (FSE)](https://github.com/Cyan4973/FiniteStateEntropy) to achieve the best compression and get best performance. By tweaking each of the algorithms to efficiently function on 16 bit greyscale medical images we have achieved good compression with very good decompression speed.

### Delta Encoding

The delta encoding implementation encodes the difference between current pixel and average of top and left pixel. If the difference is greater than a threshold then we encode the pixel value directly preceded by a delimiter.

Based on the pixel depth of an image, which is determined by the maximun pixel value, the threshold and delimiter for delta encoding are determined.

### RLE

The RLE algorithm encodes runs of similar or different values by putting the run count followed by the values. Each run length is of minimum length of 3. This is done because it guarantees that the RLE encoded data is never longer than the original.

The similar and different runs are distinguished by ensuring that the run value of similar runs is positive and the run value for dis-similar values is negative. If we don’t want the values to be negative then we can also choose a constant value, where any run value less than this constant is considered to be for similar runs and any value greater than this constant is considered for dis-similar values. In our implementation we choose this constant, called MidCount, based on the max value pixel of the image.

### Huffman Encoding

Huffman encoding of the values is done by first constructing a huffman tree out of the input values. As we are encoding 16 bit alphabet it has a potential of having high number of symbols (2^16). 

To prevent a large huffman table we restrict the number of symbols. We only consider the most highly occurring  symbols for constructing the huffman tree. 

We iteratively select the symbols which gives us a tree with maximum depth of 14. (Old Approach: A maximum of 500 unique symbols and only symbols whose occurrence frequency is more then 1/200 of the occurrence frequency of most frequent symbol are chosen.)

This ensures that the tree length is limited and speeds up the encoding and decoding process. It also saves space as we have to save the huffman tree with the encoded values. [Canonical huffman codes](https://en.wikipedia.org/wiki/Canonical_Huffman_code) are used for construction of the huffman table (codebook) which ensures that the constructed table is most compact and efficient for encoding and decoding.

During the encoding process we add a delimiter before each symbol which is also part of the huffman table. The delimiter is chosen based on the max pixel value of the image.

### FSE

The Finite State Entropy algorithm implementation from https://github.com/klauspost/compress has been re-written for 16 bit dataset.

## Benchmarks
The benchmarks are executed using the golang testing benchmark library. The file __fseu16_test.go__ containes these benchmark tests. These can be executed by using the command `go test -bench=.`

The benchmark tests are run for different DICOM image types MR, CT, CR, XR and MG. All the images are 16 bit with varying level for max pixel values. CT image is the only one with max pixel value of 65535.

The benchmark test focuses on the decompression speed and compression ratio. Compression speed is not considered as we are primarily looking a use case of rendering/decoding compressed images on the fly.

`goos: linux`

`goarch: amd64`

`cpu: Intel(R) Core(TM) i7-7700HQ CPU @ 2.80GHz`

|Test|Decompression|Ratio|
|----|-------------|-----|
|BenchmarkDeltaRLEHuffCompress/MR-8|109.85 MB/s|2.349|
|BenchmarkDeltaRLEHuffCompress/CT-8|99.33 MB/s|2.189|
|BenchmarkDeltaRLEHuffCompress/CR-8|116.76 MB/s|3.709|
|BenchmarkDeltaRLEHuffCompress/XR-8|125.38 MB/s|1.752|
|BenchmarkDeltaRLEHuffCompress/MG1-8|259.15 MB/s|8.895|
|BenchmarkDeltaRLEHuffCompress/MG2-8|257.38 MB/s|8.884|
||||
|BenchmarkDeltaRLEHuffCompress2/MR-8|117.16 MB/s|__2.349__|
|BenchmarkDeltaRLEHuffCompress2/CT-8|__120.97 MB/s__|2.189|
|BenchmarkDeltaRLEHuffCompress2/CR-8|128.75 MB/s|__3.709__|
|BenchmarkDeltaRLEHuffCompress2/XR-8|134.81 MB/s|__1.752__|
|BenchmarkDeltaRLEHuffCompress2/MG1-8|237.83 MB/s|__8.895__|
|BenchmarkDeltaRLEHuffCompress2/MG2-8|234.09 MB/s|__8.884__|
||||
|BenchmarkDeltaRLEFSECompress/MR-8|__153.26 MB/s__|2.348|
|BenchmarkDeltaRLEFSECompress/CT-8|90.19 MB/s|__2.238__|
|BenchmarkDeltaRLEFSECompress/CR-8|__202.38 MB/s__|3.474|
|BenchmarkDeltaRLEFSECompress/XR-8|__188.15 MB/s__|1.738|
|BenchmarkDeltaRLEFSECompress/MG1-8|__342.80 MB/s__|7.995|
|BenchmarkDeltaRLEFSECompress/MG2-8|__343.35 MB/s__|7.984|

`DELTA + RLE + FSE` implementation gives the best speed. The `DELTA + RLE + Huffman` implementation provides the best compression. 
# MIC - Medical Image Codec
This library introduces a lossless medical image compression codec __MIC__ for 16 bit images implemented in __Go__ which provides compression ratio similar to JPEG 2000 but with much higher speed of compression and decompression.

|Branch |Status |
|-------|-------|
|main |![example workflow](https://github.com/pappuks/medical-image-codec/actions/workflows/go.yml/badge.svg)

## Compression Algorithm
The compression algorithm uses a combination of [Delta Encoding](https://en.wikipedia.org/wiki/Delta_encoding), [RLE](https://en.wikipedia.org/wiki/Run-length_encoding) and [Huffman Coding](https://en.wikipedia.org/wiki/Huffman_coding) or [Finite State Entropy (FSE)](https://github.com/Cyan4973/FiniteStateEntropy) to achieve the best compression and get best performance. By tweaking each of the algorithms to efficiently function on 16 bit greyscale medical images we have achieved good compression with very good decompression speed.

### Delta Encoding

The delta encoding implementation encodes the difference between current pixel and average of top and left pixel. If the difference is greater than a threshold then we encode the pixel value directly preceded by a delimiter.

Based on the pixel depth of an image, which is determined by the maximum pixel value, the threshold and delimiter for delta encoding are determined.

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

We use [Finite State Entropy](https://en.wikipedia.org/wiki/Asymmetric_numeral_systems) encoder for compression. Just like the Huffman coding implementation, the FSE algorithm has also been updated to handle 16 bit data. 

I took the 8 bit implementation from https://github.com/klauspost/compress and the 16 bit FSE implementation from https://github.com/Cyan4973/FiniteStateEntropy. The 16 bit FSE implementation in my repository has been updated to handle max values till the range of 65535. This is a deviation from the implementation in https://github.com/Cyan4973/FiniteStateEntropy where the max supported value is 4095. 

The 16 bit FSE implementation can be found in [compressor](./fsecompressu16.go) and [decompressor](./fsedecompressu16.go).

## Compression Ratio

The tests are run for different DICOM image types MR, CT, CR, XR and MG. All the images are 16 bit with varying level for max pixel values. CT image is the only one with max pixel value of 65535.

`DELTA + RLE + FSE` implementation gives the best speed. The `DELTA + RLE + Huffman` implementation provides the best compression. 

Here we are only showing `DELTA + RLE + FSE` implementation results. For `DELTA + RLE + Huffman` you can checkout [BenchmarkDeltaRLEHuffCompress](./fseu16_test.go#L83).


|Test/Modality-Cores|Ratio|Original Size(MB)|Compressed Size (MB)|Rows|Columns|
|-------------------|-----|-----------------|--------------------|----|-------|
|BenchmarkDeltaRLEFSECompress/MR-64|2.348|0.1250|0.05323|256|256|
|BenchmarkDeltaRLEFSECompress/CT-64|2.238|0.5000|0.2235|512|512|
|BenchmarkDeltaRLEFSECompress/CR-64|3.474|7.184|2.068|2140|1760|
|BenchmarkDeltaRLEFSECompress/XR-64|1.738|10.07|5.792|2048|2577|
|BenchmarkDeltaRLEFSECompress/MG1-64|7.995|9.354|1.170|2457|1996|
|BenchmarkDeltaRLEFSECompress/MG2-64|7.984|9.354|1.172|2457|1996|
|BenchmarkDeltaRLEFSECompress/MG3-64|2.237|27.26|12.19|4774|3064|
|BenchmarkDeltaRLEFSECompress/MG4-64|3.474|26.00|7.485|4096|3328|

## Benchmark Tests
The benchmarks are executed using the golang testing benchmark library. The file __fseu16_test.go__ contains these benchmark tests. These can be executed by using the command `go test -bench=.`

The benchmark test focuses on the decompression speed and compression ratio. Compression speed is not considered as we are primarily looking a use case of rendering/decoding compressed images on the fly.

### Benchmarks on different CPU's

Test Command: 
`go test -benchmem -run=^$ -benchtime=200x -bench ^BenchmarkDeltaRLEFSECompress$ mic`

__Observation : Faster RAM has a big impact on performance. For example any machine with DDR5 RAM gives better performance as compared to older machine, even if the older machine has higher number of cores__

---
#### AWS EC2 instance type: c7g.metal | ARM64 | 64 cores

|Test/Modality-Cores|FPS|Decompression Speed|
|-------------------|---|-------------------|
|BenchmarkDeltaRLEFSECompress/MR-64|__17411__|2282.11 MB/s|
|BenchmarkDeltaRLEFSECompress/CT-64|__8455__|4432.93 MB/s|
|BenchmarkDeltaRLEFSECompress/CR-64|__1132__|8526.87 MB/s|
|BenchmarkDeltaRLEFSECompress/XR-64|__891.6__|9411.37 MB/s|
|BenchmarkDeltaRLEFSECompress/MG1-64|__1671__|16387.11 MB/s|
|BenchmarkDeltaRLEFSECompress/MG2-64|__1634__|16023.00 MB/s|
|BenchmarkDeltaRLEFSECompress/MG3-64|__281.4__|8043.95 MB/s|
|BenchmarkDeltaRLEFSECompress/MG4-64|__558.0__|15212.83 MB/s|

---

#### AWS EC2 Instance Type: c7i.8xlarge | AMD64 | 32 cores | cpu: Intel(R) Xeon(R) Platinum 8488C

|Test/Modality-Cores|FPS|Decompression Speed|
|-------------------|---|-------------------|
|BenchmarkDeltaRLEFSECompress/MR-32|8714|1142.14 MB/s|
|BenchmarkDeltaRLEFSECompress/CT-32|2303|1207.58 MB/s|
|BenchmarkDeltaRLEFSECompress/CR-32|421.2|3172.45 MB/s|
|BenchmarkDeltaRLEFSECompress/XR-32|309.7|3268.73 MB/s|
|BenchmarkDeltaRLEFSECompress/MG1-32|532.2|5219.59 MB/s|
|BenchmarkDeltaRLEFSECompress/MG2-32|522.4|5123.66 MB/s|
|BenchmarkDeltaRLEFSECompress/MG3-32|121.4|3468.36 MB/s|
|BenchmarkDeltaRLEFSECompress/MG4-32|182.1|4963.67 MB/s|

---

#### AWS EC2 Instance Type: c7g.8xlarge | ARM64 | 32 cores
|Test/Modality-Cores|FPS|Decompression Speed|
|-------------------|---|-------------------|
|BenchmarkDeltaRLEFSECompress/MR-32|11627|1523.94 MB/s|
BenchmarkDeltaRLEFSECompress/CT-32|4170|2186.07 MB/s|
BenchmarkDeltaRLEFSECompress/CR-32|569.5|4290.26 MB/s|
BenchmarkDeltaRLEFSECompress/XR-32|432.2|4562.04 MB/s|
BenchmarkDeltaRLEFSECompress/MG1-32|907.5|8901.02 MB/s|
BenchmarkDeltaRLEFSECompress/MG2-32|803.3|7878.92 MB/s|
BenchmarkDeltaRLEFSECompress/MG3-32|155.9|4454.75 MB/s|
BenchmarkDeltaRLEFSECompress/MG4-32|261.6|7132.05 MB/s|

---

#### Mac Studio | ARM64 | 12 cores | cpu : M2 Max

|Test/Modality-Cores|FPS|Decompression Speed|
|-------------------|---|-------------------|
|BenchmarkDeltaRLEFSECompress/MR-12|8044|1054.28 MB/s|
|BenchmarkDeltaRLEFSECompress/CT-12|2137|1120.60 MB/s|
|BenchmarkDeltaRLEFSECompress/CR-12|277.3|2089.00 MB/s|
|BenchmarkDeltaRLEFSECompress/XR-12|199.1|2101.12 MB/s|
|BenchmarkDeltaRLEFSECompress/MG1-12|373.8|3666.23 MB/s|
|BenchmarkDeltaRLEFSECompress/MG2-12|373.1|3659.37 MB/s|
|BenchmarkDeltaRLEFSECompress/MG3-12|78.35|2239.37 MB/s|
|BenchmarkDeltaRLEFSECompress/MG4-12|116.9|3188.20 MB/s|

---


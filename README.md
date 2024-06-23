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

### Benchmarks on different CPU's

Test Command: 
`go test -benchmem -run=^$ -benchtime=200x -bench ^BenchmarkDeltaRLEFSECompress$ mic`

__Observation : Faster RAM has a big impact on performance. Any machine with DDR5 RAM gives better performance__

```
c7g.metal
goos: linux
goarch: arm64
pkg: mic
```
|Test|Iterations|Decompression MB/s|Compressed Size (MB)|FPS|Original Size(MB)|Ratio|
|----|----------|------------------|--------------------|---|-----------------|-----|
|BenchmarkDeltaRLEFSECompress/MR-64|640|2004.97 MB/s|0.05323 comp|15297 fps|0.1250 original|2.348 ratio|
|BenchmarkDeltaRLEFSECompress/CT-64|640|4264.35 MB/s|0.2235 comp|8134 fps|0.5000 original|2.238 ratio|
|BenchmarkDeltaRLEFSECompress/CR-64|640|7945.67 MB/s|2.068 comp|1055 fps|7.184 original|3.474 ratio|
|BenchmarkDeltaRLEFSECompress/XR-64|640|8821.79 MB/s|5.792 comp|835.8 fps|10.07 original|1.738 ratio|
|BenchmarkDeltaRLEFSECompress/MG1-64|640|17413.43 MB/s|1.170 comp|1775 fps |9.354 original|7.995 ratio|
|BenchmarkDeltaRLEFSECompress/MG2-64|640|15333.45 MB/s|1.172 comp|1563 fps|9.354 original|7.984 ratio|
|BenchmarkDeltaRLEFSECompress/MG3-64|640|7376.50 MB/s|12.19 comp|258.1 fps|27.26 original|2.237 ratio|
|BenchmarkDeltaRLEFSECompress/MG4-64|640|15064.65 MB/s|7.485 comp|552.6 fps|26.00 original|3.474 ratio|

```
c7g.metal
goos: linux
goarch: arm64
pkg: mic
BenchmarkDeltaRLEFSECompress/MR-64  1280        2282.11 MB/s             0.05323 comp        17411 fps           0.1250 original                 2.348 ratio
BenchmarkDeltaRLEFSECompress/CT-64  1280            118271 ns/op        4432.93 MB/s             0.2235 comp          8455 fps           0.5000 original                 2.238 ratio
BenchmarkDeltaRLEFSECompress/CR-64  1280            883419 ns/op        8526.87 MB/s             2.068 comp           1132 fps           7.184 original          3.474 ratio
BenchmarkDeltaRLEFSECompress/XR-64  1280           1121557 ns/op        9411.37 MB/s             5.792 comp            891.6 fps                10.07 original           1.738 ratio
BenchmarkDeltaRLEFSECompress/MG1-64 1280            598540 ns/op        16387.11 MB/s            1.170 comp           1671 fps           9.354 original          7.995 ratio
BenchmarkDeltaRLEFSECompress/MG2-64 1280            612142 ns/op        16023.00 MB/s            1.172 comp           1634 fps           9.354 original          7.984 ratio
BenchmarkDeltaRLEFSECompress/MG3-64 1280           3553102 ns/op        8043.95 MB/s            12.19 comp             281.4 fps                27.26 original           2.237 ratio
BenchmarkDeltaRLEFSECompress/MG4-64 1280           1792104 ns/op        15212.83 MB/s            7.485 comp            558.0 fps                26.00 original           3.474 ratio
PASS
ok      mic     14.460s
```
```
AWS EC2 Instance Type: c7i.8xlarge
goos: linux
goarch: amd64
pkg: mic
cpu: Intel(R) Xeon(R) Platinum 8488C
BenchmarkDeltaRLEFSECompress/MR-32           200            114760 ns/op        1142.14 MB/s             0.05323 comp         8714 fps           0.1250 original                 2.348 ratio
BenchmarkDeltaRLEFSECompress/CT-32           200            434163 ns/op        1207.58 MB/s             0.2235 comp          2303 fps           0.5000 original                 2.238 ratio
BenchmarkDeltaRLEFSECompress/CR-32           200           2374440 ns/op        3172.45 MB/s             2.068 comp            421.2 fps                 7.184 original          3.474 ratio
BenchmarkDeltaRLEFSECompress/XR-32           200           3229206 ns/op        3268.73 MB/s             5.792 comp            309.7 fps                10.07 original           1.738 ratio
BenchmarkDeltaRLEFSECompress/MG1-32                  200           1879142 ns/op        5219.59 MB/s             1.170 comp            532.2 fps                 9.354 original          7.995 ratio
BenchmarkDeltaRLEFSECompress/MG2-32                  200           1914325 ns/op        5123.66 MB/s             1.172 comp            522.4 fps                 9.354 original          7.984 ratio
BenchmarkDeltaRLEFSECompress/MG3-32                  200           8240490 ns/op        3468.36 MB/s            12.19 comp             121.4 fps                27.26 original           2.237 ratio
BenchmarkDeltaRLEFSECompress/MG4-32                  200           5492502 ns/op        4963.67 MB/s             7.485 comp            182.1 fps                26.00 original           3.474 ratio
PASS
ok      mic     6.877s
```

```
c7g.8xlarge
goos: linux
goarch: arm64
pkg: mic
BenchmarkDeltaRLEFSECompress/MR-32           200             86009 ns/op        1523.94 MB/s             0.05323 comp        11627 fps           0.1250 original                 2.348 ratio
BenchmarkDeltaRLEFSECompress/CT-32           200            239831 ns/op        2186.07 MB/s             0.2235 comp          4170 fps           0.5000 original                 2.238 ratio
BenchmarkDeltaRLEFSECompress/CR-32           200           1755793 ns/op        4290.26 MB/s             2.068 comp            569.5 fps                 7.184 original          3.474 ratio
BenchmarkDeltaRLEFSECompress/XR-32           200           2313742 ns/op        4562.04 MB/s             5.792 comp            432.2 fps                10.07 original           1.738 ratio
BenchmarkDeltaRLEFSECompress/MG1-32                  200           1101935 ns/op        8901.02 MB/s             1.170 comp            907.5 fps                 9.354 original          7.995 ratio
BenchmarkDeltaRLEFSECompress/MG2-32                  200           1244885 ns/op        7878.92 MB/s             1.172 comp            803.3 fps                 9.354 original          7.984 ratio
BenchmarkDeltaRLEFSECompress/MG3-32                  200           6415846 ns/op        4454.75 MB/s            12.19 comp             155.9 fps                27.26 original           2.237 ratio
BenchmarkDeltaRLEFSECompress/MG4-32                  200           3822598 ns/op        7132.05 MB/s             7.485 comp            261.6 fps                26.00 original           3.474 ratio
PASS
ok      mic     6.004s
```
```
Mac Studio
CPU : M2 Max
goos: darwin
goarch: arm64
pkg: mic
BenchmarkDeltaRLEFSECompress/MR-12           200            124324 ns/op        1054.28 MB/s             0.05323 comp         8044 fps           0.1250 original                 2.348 ratio 
BenchmarkDeltaRLEFSECompress/CT-12           200            467862 ns/op        1120.60 MB/s             0.2235 comp          2137 fps           0.5000 original                 2.238 ratio
BenchmarkDeltaRLEFSECompress/CR-12           200           3605936 ns/op        2089.00 MB/s             2.068 comp            277.3 fps                 7.184 original          3.474 ratio
BenchmarkDeltaRLEFSECompress/XR-12           200           5023703 ns/op        2101.12 MB/s             5.792 comp            199.1 fps                10.07 original           1.738 ratio
BenchmarkDeltaRLEFSECompress/MG1-12                  200           2675320 ns/op        3666.23 MB/s             1.170 comp            373.8 fps                 9.354 original          7.995 ratio
BenchmarkDeltaRLEFSECompress/MG2-12                  200           2680334 ns/op        3659.37 MB/s             1.172 comp            373.1 fps                 9.354 original          7.984 ratio
BenchmarkDeltaRLEFSECompress/MG3-12                  200          12762981 ns/op        2239.37 MB/s            12.19 comp              78.35 fps               27.26 original           2.237 ratio
BenchmarkDeltaRLEFSECompress/MG4-12                  200           8551203 ns/op        3188.20 MB/s             7.485 comp            116.9 fps                26.00 original           3.474 ratio
PASS
ok      mic     8.972s
```
```
AWS EC2 Instance Type : c5a.8xlarge
goos: linux
goarch: amd64
pkg: mic
cpu: AMD EPYC 7R32
BenchmarkDeltaRLEFSECompress/MR-32           200            176099 ns/op         744.31 MB/s             0.05323 comp         5679 fps           0.1250 original                 2.348 ratio
BenchmarkDeltaRLEFSECompress/CT-32           200            403565 ns/op        1299.14 MB/s             0.2235 comp          2478 fps           0.5000 original                 2.238 ratio
BenchmarkDeltaRLEFSECompress/CR-32           200           2773651 ns/op        2715.84 MB/s             2.068 comp            360.5 fps                 7.184 original          3.474 ratio
BenchmarkDeltaRLEFSECompress/XR-32           200           3551395 ns/op        2972.18 MB/s             5.792 comp            281.6 fps                10.07 original           1.738 ratio
BenchmarkDeltaRLEFSECompress/MG1-32                  200           1954555 ns/op        5018.20 MB/s             1.170 comp            511.6 fps                 9.354 original          7.995 ratio
BenchmarkDeltaRLEFSECompress/MG2-32                  200           2050966 ns/op        4782.31 MB/s             1.172 comp            487.6 fps                 9.354 original          7.984 ratio
BenchmarkDeltaRLEFSECompress/MG3-32                  200           9941700 ns/op        2874.86 MB/s            12.19 comp             100.6 fps                27.26 original           2.237 ratio
BenchmarkDeltaRLEFSECompress/MG4-32                  200           5900048 ns/op        4620.81 MB/s             7.485 comp            169.5 fps                26.00 original           3.474 ratio
PASS
ok      mic     8.614s
```

```
AWS EC2 Instance Type: c4.8xlarge
goos: linux
goarch: amd64
pkg: mic
cpu: Intel(R) Xeon(R) CPU E5-2666 v3 @ 2.90GHz
BenchmarkDeltaRLEFSECompress/MR-36           200            157621 ns/op         831.56 MB/s             0.05323 comp         6344 fps           0.1250 original                 2.348 ratio
BenchmarkDeltaRLEFSECompress/CT-36           200            343485 ns/op        1526.38 MB/s             0.2235 comp          2911 fps           0.5000 original                 2.238 ratio
BenchmarkDeltaRLEFSECompress/CR-36           200           2864667 ns/op        2629.56 MB/s             2.068 comp            349.1 fps                 7.184 original          3.474 ratio
BenchmarkDeltaRLEFSECompress/XR-36           200           4076667 ns/op        2589.22 MB/s             5.792 comp            245.3 fps                10.07 original           1.738 ratio
BenchmarkDeltaRLEFSECompress/MG1-36                  200           1738341 ns/op        5642.36 MB/s             1.170 comp            575.3 fps                 9.354 original          7.995 ratio
BenchmarkDeltaRLEFSECompress/MG2-36                  200           2223244 ns/op        4411.73 MB/s             1.172 comp            449.8 fps                 9.354 original          7.984 ratio
BenchmarkDeltaRLEFSECompress/MG3-36                  200          10260832 ns/op        2785.45 MB/s            12.19 comp              97.46 fps               27.26 original           2.237 ratio
BenchmarkDeltaRLEFSECompress/MG4-36                  200           6850872 ns/op        3979.49 MB/s             7.485 comp            146.0 fps                26.00 original           3.474 ratio
PASS
ok      mic     9.194s
```



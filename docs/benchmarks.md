# Benchmark baseline

Go 1.26.5, macOS arm64, Apple M4 Max. Each row is the median of five samples,
each measured with exactly 100 operations using `-benchmem -benchtime=100x -count=5`.
Allocation columns use the median when the samples differ. Compare
only equivalent behavior, toolchains, and hosts; these short local samples are
a regression signal, not a capacity forecast.

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| RuntimeHotRead | 413 | 336 | 6 |
| RuntimeRefresh100 | 639,108 | 88,600 | 926 |
| HotRead | 128 | 8 | 1 |
| ColdRead | 450 | 216 | 4 |
| ResolutionDepth | 1,021 | 272 | 7 |
| BulkRead100 | 4,240 | 15,136 | 101 |
| ProviderContention | 895 | 894 | 6 |
| CacheInvalidation | 3,841 | 2,378 | 27 |

# Benchmark baseline

Go 1.26.5, Apple M4 Max, `-benchtime=10x`; compare only equivalent hosts.

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| HotRead | 133 | 8 | 1 |
| ColdRead | 704 | 216 | 4 |
| ResolutionDepth | 592 | 272 | 7 |
| BulkRead100 | 5,000 | 15,136 | 101 |
| ProviderContention | 2,838 | 2,008 | 12 |
| CacheInvalidation | 22,612 | 3,524 | 34 |

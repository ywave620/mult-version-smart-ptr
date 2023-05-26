# mult-version-smart-ptr

A safe and efficent way to share a mult-version immutable resource that needs to be cleaned up whenever useless among multiple long-living goroutines.

## Features

Only one locking is needed for every version of the resource: lock at the first query, and for subsequent quries, only a few atomic operations is incurred if no newer version


## Caveat

- Not suitable for the common use of Go, that is, spawn goroutine without any limit.
- It is profitable only when you have some long-living goroutines need to access an immutable resource concurrently.

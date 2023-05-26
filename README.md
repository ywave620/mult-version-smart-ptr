# mult-version-smart-ptr

A safe and efficent way to share a mult-version immutable resource that needs to be cleaned up whenever useless among multiple long-living goroutines.

## Features

- Only one locking is incurred for every version of the resource: a reader locks at the first query, and for subsequent quries, only a few atomic operations is involved if no newer version.
- Like c++'s `std::shared_ptr`, if the refcnt drops to zero, the resource is automatically deleted.

## Caveat

- Not suitable for the common use of Go, that is, spawn goroutine without any limit.
- It is profitable only when you have some long-living goroutines need to access an immutable resource concurrently.

## Hello World

``` go
func getAndUse(gid) {
  handle := GetResouce(gid, false)
  use(handle)
  DoneUsingResource(gid, handle)
}

for i := 0; i < 10; i ++ {
  go func() {
    gid := AllocateGLocalImmRscHandle()
    for i := 0; i < 10; i ++ {
      getAndUse(gid)
    }
  }()
}
```

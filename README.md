# mult-version-smart-ptr

A safe and efficent way to share a mult-version immutable resource that needs to be cleaned up whenever useless among multiple long-living goroutines.

## Motivation

How to share an immutable resource(once made, never change) among goroutines?

### The most intuitive way(but wrong)

writer:
1. lock
2. save the created resource in a global variable
3. set refcnt to 1
4. unlock

reader:
1. atomically read the global variable
2. atomically incr refcnt by 1
3. make use of it
4. atomically decr refcnt by 1, if it becomes zero, clean up the resource

reader.4 races with reader.2 and may run into a situation where a reader decides to cleanup the resource and another one decides to use it.

### Fix

reader:
1. lock
2. atomically read the global variable
3. atomically incr refcnt by 1
4. unlock
5. make use of it
6. lock
7. atomically decr refcnt by 1
8. unlock
9. if refcnt becomes zero, clean up the resource

What's wrong? Not scalable because every read requires locking.

### Improve(by introducing version number)


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

# mult-version-smart-ptr

A safe and efficent way to share a mult-version immutable resource that needs to be cleaned up whenever useless among multiple long-living goroutines.

## Motivation

How to share an immutable resource(once made, never change) among goroutines?

### The most intuitive way(but wrong)

writer:
1. lock
2. create a resource and save a reference to it in a global variable, called G
3. set refcnt to 1
4. unlock

reader:
1. atomically read G
2. atomically incr refcnt by 1
3. make use of it
4. atomically decr refcnt by 1, if it becomes zero, clean up the resource

reader.4 races with reader.2 and may run into a situation where a reader decides to cleanup the resource and another one decides to use it.

### Fix

reader:
1. lock
2. atomically reads G
3. atomically incr refcnt by 1
4. unlock
5. make use of it
6. lock
7. atomically decr refcnt by 1
8. unlock
9. if refcnt becomes zero, clean up the resource

What's wrong? Not scalable because every read requires locking.

### Improve by thread-localing

writer:
1. lock
2. create a resource and save a reference to it in a global variable, called G
3. set refcnt to 1
4. incr the global version number, called V by 1
5. unlock

reader:
1. compare the thread-local version number(called Vtl) with the global one
2. if differenet
 
- lock
- drop refcnt of the thread-local reference(called, Gtl) by 1, if it becomes 0, then delete the underlying resource
- read G to Gtl
- read V to thread-local store, Vtl
- unlock
- make use of Gtl

3. otherwise, make use of Gtl

What's wrong? No refcnt guarded resource reclaim anymore! A reader might hold a reference to an obsolute version forever and make no use of it. 

### Improve by validating references to an obsolute version

See the code

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

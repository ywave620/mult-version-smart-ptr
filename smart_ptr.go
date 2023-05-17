package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"
)

// Observation, make use of this local-global pattern to minimize locking when reading is not much useful in go.
// Because, locking is incurred at the first read for a goroutine, and goroutine has a short lifetime in go, e.g.
// as short as a HTTP request, as short as a TCP connection.
// However, this patten works perfectly in languages having an idea of thread-pool, like cpp and Java. In such
// language, a thread is long living and might serve many requests.

// ImmRscPtr is used to safely and efficently share a mult-version immutable resource that needs to be cleaned up
// whenever not used by anyone (e.g. a snapshot in DB) across multiple goroutines. It has the following features:
//
// (1) If the call of UpdateResource() in a writer happens-before the call of GetResource() in a reader
// then the reader sees what the writer puts in.
// (2) If the ImmutableResource has not been changed since the last GetResource(), then GetResource() has a cost
// of only two atomic operations.
// (3) As long as the caller of GetResource() manages to call Unref() on the returning ImmRscPtr, then no ImmutableResource
// will be leaked. By saying leaked, we means that Delete() is never be called on an obsolute ImmutableResource even
// there is no one using it.
// (3) As long as the caller of GetResource() does not call Unref() on the returning ImmRscPtr, then it will
// not be Delete() even it becomes obsolute

type ImmutableResource interface {
	Delete()
}

var maxGID int32 = -1
var gLocalImmRscHandles [1024]*ImmRscHandle
var kInuse *ImmRscHandle = newImmRscHandle(nil)


func atomicSwapGLocalImmRscHandle(gID int32, new *ImmRscHandle) *ImmRscHandle {
	gLocalImmRscHandles[gID] = new
	return (*ImmRscHandle)(atomic.SwapPointer((*unsafe.Pointer)((unsafe.Pointer)(&gLocalImmRscHandles[gID])), unsafe.Pointer(new)))
}
func atomicCmpAndSwapGLocalImmRscHandles(gID int32, old, new *ImmRscHandle) bool {
	if old == gLocalImmRscHandles[gID] {
		gLocalImmRscHandles[gID] = new
		return true
	}
	return atomic.CompareAndSwapPointer((*unsafe.Pointer)((unsafe.Pointer)(&gLocalImmRscHandles[gID])), unsafe.Pointer(old), unsafe.Pointer(new))
}


// each goroutine can call this function at most once
func AllocateGLocalImmRscHandle() int32 {
	newId := atomic.AddInt32(&maxGID, 1) // goroutine id starts from 0
	gLocalImmRscHandles[newId] = nil
	return newId
}

var latestImmRscHandle *ImmRscHandle = newImmRscHandle(nil)
var latestImmRscHandleMutex sync.Mutex


type ImmRscHandleWrap struct {
	*ImmRscHandle
	mightPaasToOtherGoroutine bool
}


// If `mightShare`, caller is allowed to call Ref() to make a copy of the ownership
// and then paas the copy to other goroutines. If caller uses in this way, then it must use Unref() to
// release the ownership for evey copy.
//
// Otherwise, caller can not
// 1. call Ref()
// 2. call GetResouce() again before DoneUsingResource()
// 3. share the handle with other goroutines
// Caller must call DoneUsingResource() to release the ownership
//
// The first way is a user-friendly one but it is slower.
func GetResouce(gID int32, mightShare bool) *ImmRscHandle {
	if gID < 0 || gID > maxGID {
		panic("unallocated goroutine ID")
	}

	// gLocalImmRscHandles[gID] is shared between this goroutine and the writer(there can be one writer only at any given time)
	// so this is a one-reader-one-writer conflict. We let them compete by conducting swap instruction so that the winer sees
	// the original value while the loser sees the special mark(kInuse or nil).
	//
	// This is also why we have eventual consistency.
	// Assuming UpdateResouce() is called once and called simultaneously with this func.
	// If this goroutine swaps before the writer, this func returns the second latest version.
	// Otherwise, this func returns the latest version.

	var res *ImmRscHandle
	local := atomicSwapGLocalImmRscHandle(gID, kInuse) // A
	if local == nil { // This is the first time for the current goroutine to call this func or `globalResouce` has been updated
		latestImmRscHandleMutex.Lock()
		res = latestImmRscHandle.Ref() // Ref() can only be called with mutex held, otherwise, it might race with Unref()
		latestImmRscHandleMutex.Unlock()
	} else if local == kInuse {
		panic("gLocalImmRscHandles[" + fmt.Sprint(gID) + "] must be either a valid ptr or a nil set by writer")
	} else {
		res = local
	}

	if mightShare {
		// Make a copy and then return to local store, otherwise we have to lock the mutex to
		// read the global `latestImmRscPtr` at the next call
		if !atomicCmpAndSwapGLocalImmRscHandles(gID, kInuse, res.Ref()) {
			// Failed due to the local ptr(gLocalImmRscHandles[gID]) has been changed to nil by writer since A,
			// then we rather than the next writer are responsible for Unref()
			res.Unref()
		}
		// else the next writer will Unref() when it invalidates our local ptr
	}

	return res
}

func DoneUsingResource(gID int32, gotFromLocal *ImmRscHandle) {
	if !atomicCmpAndSwapGLocalImmRscHandles(gID, kInuse, gotFromLocal) {
		gotFromLocal.Unref()
	}
	// else the next writer will Unref() when it invalidates our local ptr
}

// can be called by any goroutine without any synchronization
func UpdateResouce(r ImmutableResource) {
	latestImmRscHandleMutex.Lock()

	old := latestImmRscHandle
	latestImmRscHandle = newImmRscHandle(r) // overwritten with mutex held and before invalidate local ptrs

	for i := 0; i <= int(maxGID); i ++ {
		local := atomicSwapGLocalImmRscHandle(int32(i), nil)
		if local != kInuse && local != nil {
			if old != local {
				panic("gLocalImmRscHandles[" + fmt.Sprint(i) + "] does not hold the latest version")
			}
			if local.Unref() { // This could not be the last reference, see the last line of this function
				panic("bad refcnt")
			}
		}
	}

	latestImmRscHandleMutex.Unlock()

	// this might call Resouce::Delete(), which might be time-consuming
	// therefore we call it here without mutex held
	old.Unref()
}

// unexported stuffs:

func newImmRscHandle(rsc ImmutableResource) *ImmRscHandle {
	return &ImmRscHandle{
		refcnt: 1,
		R: rsc,
	}
}

// ImmRscHandle must be allocated in heap and can not be copied using = operator
type ImmRscHandle struct {
	refcnt int32
	R ImmutableResource
}

// The goroutine create this ImmRscHandle is the first owner of the underlying resource
// Owner is allowed to call Ref() without any external synchronization
func (p *ImmRscHandle) Ref() *ImmRscHandle {
	if atomic.AddInt32(&p.refcnt, 1) <= 0 {
		panic("bad refcnt")
	}
	return p
}
func (p *ImmRscHandle) Unref() (deleted bool) {
	after := atomic.AddInt32(&p.refcnt, -1)
	if after == 0 {
		p.R.Delete()
		return true
	} else if after < 0 {
		panic("bad refcnt")
	}

	return false
}
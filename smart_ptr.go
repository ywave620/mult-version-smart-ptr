package main

import (
	"fmt"
	"sync"
	"sync/atomic"
)

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

type ImmRscPtr interface {
	//  The goroutine create this ImmRscPtr is the first owner
	Get() ImmutableResource
	Unref() // Unref() might call ImmutableResource::Delete() under the hood

	// Owner is allowed to call ref() without sync
	ref() ImmRscPtr
}
var maxGID int32 = -1
var gLocalImmRscPtr [1024]ImmRscPtr
var kInuse ImmRscPtr = newImmRscPtr(nil)


func atomicSwapGLocalImmRscPtr(gID int32, new ImmRscPtr) (old ImmRscPtr) {
	// TODO make it real
	old = gLocalImmRscPtr[gID]
	gLocalImmRscPtr[gID] = new
	return old
}
func atomicCmpAndSwapGLocalImmRscPtr(gID int32, old, new ImmRscPtr) bool {
	// TODO make it real
	if old == gLocalImmRscPtr[gID] {
		gLocalImmRscPtr[gID] = new
		return true
	}
	return false
}


// each goroutine can call this function at most once
func AllocateGLocalImmRscPtr() int32 {
	newId := atomic.AddInt32(&maxGID, 1) // goroutine id starts from 0
	gLocalImmRscPtr[newId] = nil
	return newId
}

var latestImmRscPtr ImmRscPtr = newImmRscPtr(nil) // before any UpdateResouce() call, GetResouce() yields a ImmRscPtr wrapping a nil
var latestImmRscPtrMutex sync.Mutex

// caller should call ImmRscPtr.Unref() after done
func GetResouce(gID int32) ImmRscPtr {
	if gID < 0 || gID > maxGID {
		panic("unallocated goroutine ID")
	}

	// gLocalImmRscPtr[gID] is shared between this goroutine and the writer(there can be one writer only at any given time)
	// so this is a one-reader-one-writer conflict. We let them compete by conducting swap instruction so that the winer sees
	// the original value while the loser sees the special mark(kInuse or nil).
	//
	// This is also why we have eventual consistency.
	// Assuming UpdateResouce() is called once and called simultaneously with this func.
	// If this goroutine swaps before the writer, this func returns the second latest version.
	// Otherwise, this func returns the latest version.

	var res ImmRscPtr
	local := atomicSwapGLocalImmRscPtr(gID, kInuse) // A
	if local == nil { // This is the first time for the current goroutine to call this func or `globalResouce` has been updated
		latestImmRscPtrMutex.Lock()
		res = latestImmRscPtr.ref() // Ref() can only be called with mutex held, otherwise, it might race with Unref()
		latestImmRscPtrMutex.Unlock()
	} else if local == kInuse {
		panic("gLocalImmRscPtr[" + fmt.Sprint(gID) + "] must be either a valid ptr or a nil set by writer")
	} else {
		res = local
	}

	// Return to local store, otherwise we have to lock the mutex to
	// read the global `latestImmRscPtr` at the next call
	if !atomicCmpAndSwapGLocalImmRscPtr(gID, kInuse, res.ref()) {
		// Failed due to the local ptr(gLocalImmRscPtr[gID]) has been changed to nil by writer since A,
		// then we rather than the next writer are responsible for Unref()
		res.Unref()
	}
	// else the next writer will Unref() when it invalidates our local ptr
	return res
}

// can be called by any goroutine without any synchronization
func UpdateResouce(r ImmutableResource) {
	latestImmRscPtrMutex.Lock()

	for i := 0; i <= int(maxGID); i ++ {
		local := atomicSwapGLocalImmRscPtr(int32(i), nil)
		if local != kInuse && local != nil {
			if latestImmRscPtr != local {
				panic("gLocalImmRscPtr[" + fmt.Sprint(i) + "] does not hold the latest version")
			}
			local.Unref() // This will never call Resource::Delete(), which is checked in the following if block
		}
	}

	if p, ok := latestImmRscPtr.(*resourcePtr); ok {
		if atomic.LoadInt32(&p.refcnt) <= 0 {
			panic("bad refcnt")
		}
	} else {
		panic("bad type")
	}

	old := latestImmRscPtr
	latestImmRscPtr = newImmRscPtr(r) // overwritten with mutex held
	latestImmRscPtrMutex.Unlock()

	// this might call Resouce::Delete(), which might be time-consuming
	// therefore we call it here without mutex held
	old.Unref()
}

// unexported stuffs:

func newImmRscPtr(rsc ImmutableResource) ImmRscPtr {
	return &resourcePtr{
		refcnt: 1,
		ImmutableResource: rsc,
	}
}

// never use this struct directly, use it through ImmRscPtr
// this struct must be allocated in heap and can not be copied using = operator
type resourcePtr struct {
	refcnt int32
	ImmutableResource
}
func (p *resourcePtr) ref() ImmRscPtr {
	atomic.AddInt32(&p.refcnt, 1)
	return p
}
func (p *resourcePtr) Get() ImmutableResource {
	return p.ImmutableResource
}
func (p *resourcePtr) Unref() {
	after := atomic.AddInt32(&p.refcnt, -1)
	if after == 0 {
		p.ImmutableResource.Delete()
	}
}

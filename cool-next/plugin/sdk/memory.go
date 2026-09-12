package sdk

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

type allocationStore struct {
	mu     sync.RWMutex
	values map[uintptr][]byte
}

type responseStore struct {
	mu     sync.RWMutex
	next   uint64
	values map[uint64][]byte
}

func newAllocationStore() *allocationStore {
	return &allocationStore{values: make(map[uintptr][]byte)}
}

func (store *allocationStore) allocate(size int32) unsafe.Pointer {
	if size <= 0 {
		return nil
	}
	value := make([]byte, size)
	pointer := unsafe.Pointer(&value[0])
	store.mu.Lock()
	store.values[uintptr(pointer)] = value
	store.mu.Unlock()

	return pointer
}

func (store *allocationStore) free(pointer unsafe.Pointer, size int32) {
	if pointer == nil || size <= 0 {
		return
	}
	store.mu.Lock()
	if value, exists := store.values[uintptr(pointer)]; exists && len(value) == int(size) {
		delete(store.values, uintptr(pointer))
	}
	store.mu.Unlock()
}

func (store *allocationStore) read(pointer unsafe.Pointer, size int32) ([]byte, error) {
	if size < 0 {
		return nil, errors.New("内存长度不能为负数")
	}
	if size == 0 {
		return []byte{}, nil
	}
	if pointer == nil {
		return nil, errors.New("内存指针不能为空")
	}
	store.mu.RLock()
	value, exists := store.values[uintptr(pointer)]
	if !exists || len(value) < int(size) {
		store.mu.RUnlock()
		return nil, fmt.Errorf("内存范围无效")
	}
	result := append([]byte(nil), value[:size]...)
	store.mu.RUnlock()

	return result, nil
}

func newResponseStore() *responseStore {
	return &responseStore{values: make(map[uint64][]byte)}
}

func (store *responseStore) add(value []byte) int64 {
	store.mu.Lock()
	store.next++
	if store.next == 0 {
		store.next++
	}
	handle := store.next
	store.values[handle] = append([]byte(nil), value...)
	store.mu.Unlock()

	return int64(handle)
}

func (store *responseStore) pointer(handle int64) unsafe.Pointer {
	store.mu.RLock()
	value, exists := store.values[uint64(handle)]
	if !exists || len(value) == 0 {
		store.mu.RUnlock()
		return nil
	}
	pointer := unsafe.Pointer(&value[0])
	store.mu.RUnlock()

	return pointer
}

func (store *responseStore) length(handle int64) int32 {
	store.mu.RLock()
	value, exists := store.values[uint64(handle)]
	store.mu.RUnlock()
	if !exists || len(value) > int(^uint32(0)>>1) {
		return -1
	}

	return int32(len(value))
}

func (store *responseStore) drop(handle int64) {
	store.mu.Lock()
	delete(store.values, uint64(handle))
	store.mu.Unlock()
}

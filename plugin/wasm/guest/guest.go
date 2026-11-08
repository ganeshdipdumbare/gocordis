//go:build wasip1

// Package guest is the host ABI for Go WebAssembly components.
package guest

import "unsafe"

//go:wasmimport cordis log
func hostLog(level uint32, ptr uint32, size uint32)

//go:wasmimport cordis subscribe
func hostSubscribe(ptr uint32, size uint32)

//go:wasmimport cordis emit
func hostEmit(namePtr, nameLen, payloadPtr, payloadLen uint32)

var keep [][]byte

// Alloc returns a guest pointer for size bytes. The buffer is retained until
// the module closes.
func Alloc(size uint32) uint32 {
	if size == 0 {
		return 0
	}
	buffer := make([]byte, size)
	keep = append(keep, buffer)
	return uint32(uintptr(unsafe.Pointer(&buffer[0])))
}

// Read returns a view of guest memory.
func Read(ptr, size uint32) []byte {
	if size == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), size)
}

// Log sends one line to the host. Level 0 is error, 1 warn, 2 info, 3 debug.
func Log(level int, message string) {
	data := []byte(message)
	keep = append(keep, data)
	hostLog(uint32(level), uint32(uintptr(unsafe.Pointer(&data[0]))), uint32(len(data)))
}

// Subscribe asks the host to deliver the named event to cordis_event.
func Subscribe(name string) {
	data := []byte(name)
	keep = append(keep, data)
	hostSubscribe(uint32(uintptr(unsafe.Pointer(&data[0]))), uint32(len(data)))
}

// Emit sends an event to the host with an opaque payload.
func Emit(name string, payload []byte) {
	nameData := []byte(name)
	keep = append(keep, nameData)
	if len(payload) == 0 {
		hostEmit(uint32(uintptr(unsafe.Pointer(&nameData[0]))), uint32(len(nameData)), 0, 0)
		return
	}
	payloadData := make([]byte, len(payload))
	copy(payloadData, payload)
	keep = append(keep, payloadData)
	hostEmit(
		uint32(uintptr(unsafe.Pointer(&nameData[0]))), uint32(len(nameData)),
		uint32(uintptr(unsafe.Pointer(&payloadData[0]))), uint32(len(payloadData)),
	)
}

//go:build wasip1

package main

import "gocordis/plugin/wasm/guest"

func main() {}

//go:wasmexport cordis_alloc
func cordisAlloc(size uint32) uint32 { return guest.Alloc(size) }

//go:wasmexport cordis_apply
func cordisApply(ptr, size uint32) uint32 {
	guest.Log(2, "wasm guest applied with config "+string(guest.Read(ptr, size)))
	guest.Subscribe("ping")
	return 0
}

//go:wasmexport cordis_event
func cordisEvent(namePtr, nameLen, payloadPtr, payloadLen uint32) {
	name := string(guest.Read(namePtr, nameLen))
	if name == "ping" {
		guest.Emit("pong", guest.Read(payloadPtr, payloadLen))
	}
}

//go:wasmexport cordis_dispose
func cordisDispose() { guest.Log(2, "wasm guest disposed") }

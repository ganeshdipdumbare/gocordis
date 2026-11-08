//go:build wasip1

package main

import "gocordis/plugin/wasm/guest"

func main() {}

//go:wasmexport cordis_alloc
func cordisAlloc(size uint32) uint32 { return guest.Alloc(size) }

//go:wasmexport cordis_apply
func cordisApply(ptr, size uint32) uint32 {
	config := guest.Read(ptr, size)
	guest.Log(2, "applied "+string(config))
	guest.Subscribe("ping")
	return 0
}

//go:wasmexport cordis_event
func cordisEvent(namePtr, nameLen, payloadPtr, payloadLen uint32) {
	name := string(guest.Read(namePtr, nameLen))
	payload := guest.Read(payloadPtr, payloadLen)
	if name == "ping" {
		guest.Emit("pong", payload)
	}
}

//go:wasmexport cordis_dispose
func cordisDispose() { guest.Log(2, "disposed") }

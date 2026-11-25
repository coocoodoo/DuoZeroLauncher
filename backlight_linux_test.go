package main

import (
	"testing"
	"unsafe"
)

// The GPIO ioctls take these structs by their exact kernel layout (linux/gpio.h).
func TestGPIOStructLayout(t *testing.T) {
	if n := unsafe.Sizeof(gpioV2LineRequest{}); n != 592 {
		t.Errorf("gpio_v2_line_request is %d bytes, the kernel's is 592", n)
	}
	if n := unsafe.Sizeof(gpioChipInfo{}); n != 68 {
		t.Errorf("gpiochip_info is %d bytes, the kernel's is 68", n)
	}
	if n := unsafe.Offsetof(gpioV2LineRequest{}.Config); n != 288 {
		t.Errorf("config at %d, the kernel's at 288", n)
	}
	if n := unsafe.Offsetof(gpioV2LineRequest{}.Fd); n != 588 {
		t.Errorf("fd at %d, the kernel's at 588", n)
	}
	// the ioctl numbers carry the struct sizes
	if gpioV2GetLineIoctl>>16&0x3fff != 592 || gpioGetChipInfoIoctl>>16&0x3fff != 68 {
		t.Error("ioctl numbers don't match the struct sizes")
	}
}

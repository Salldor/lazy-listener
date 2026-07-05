package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unsafe"

	"github.com/gen2brain/malgo"
)

// selectDevice prints an interactive numbered list and returns the chosen device ID.
// When optional=true the user may press Enter to skip; returns nil in that case.
func selectDevice(ctx *malgo.AllocatedContext, kind malgo.DeviceType, optional bool) (unsafe.Pointer, error) {
	devices, err := ctx.Devices(kind)
	if err != nil {
		return nil, err
	}

	label := "capture"
	if kind == malgo.Playback {
		label = "playback"
	}

	fmt.Printf("\nAvailable %s devices:\n", label)
	for i, d := range devices {
		fmt.Printf("  [%d] %s\n", i+1, d.Name())
	}

	if optional {
		fmt.Printf("Select %s device (Enter to skip): ", label)
	} else {
		fmt.Printf("Select %s device [1-%d]: ", label, len(devices))
	}

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)

	if line == "" {
		if optional {
			return nil, nil
		}
		return nil, fmt.Errorf("capture device is required")
	}

	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(devices) {
		return nil, fmt.Errorf("invalid choice: %q", line)
	}

	return devices[n-1].ID.Pointer(), nil
}

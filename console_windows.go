package main

import (
	"bufio"
	"fmt"
	"os"
	"unsafe"
)

// waitIfOwnConsole - started by a double-click in Explorer, the console is ours alone and closes when we end - wait
// for Enter, so the result can be read. Started from cmd / PowerShell, the console has more processes - no waiting.
func waitIfOwnConsole() {
	var pids [4]uint32
	n, _, _ := getConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n != 1 {
		return
	}
	fmt.Print("\nPress Enter to close this window.")
	bufio.NewReader(os.Stdin).ReadString('\n')
}

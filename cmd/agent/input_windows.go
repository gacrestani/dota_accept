//go:build windows

package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procEnumWindows               = user32.NewProc("EnumWindows")
	procGetWindowTextW            = user32.NewProc("GetWindowTextW")
	procIsWindowVisible           = user32.NewProc("IsWindowVisible")
	procGetWindowThreadProcessId  = user32.NewProc("GetWindowThreadProcessId")
	procSetForegroundWindow       = user32.NewProc("SetForegroundWindow")
	procGetForegroundWindow       = user32.NewProc("GetForegroundWindow")
	procIsIconic                  = user32.NewProc("IsIconic")
	procShowWindow                = user32.NewProc("ShowWindow")
	procSendInput                 = user32.NewProc("SendInput")
	procKeybdEvent                = user32.NewProc("keybd_event")
	procOpenProcess               = kernel32.NewProc("OpenProcess")
	procCloseHandle               = kernel32.NewProc("CloseHandle")
	procQueryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
)

const (
	swRestore                      = 9
	vkMenu                         = 0x12 // Alt
	keyeventfKeyUp                 = 0x0002
	keyeventfScancode              = 0x0008
	inputKeyboard                  = 1
	scanEnter                      = 0x1C
	processQueryLimitedInformation = 0x1000
)

// pressAccept delivers an Enter keypress to the Dota 2 window. It refuses to
// send input anywhere else: if Dota isn't running, it does nothing.
func pressAccept() (string, error) {
	hwnd := findDotaWindow()
	if hwnd == 0 {
		return "", errors.New("Dota 2 window not found — is the game running?")
	}
	focused := focusWindow(hwnd)
	// Give the client UI a beat to settle after the focus change.
	time.Sleep(250 * time.Millisecond)
	if err := sendEnter(); err != nil {
		return "", err
	}
	if !focused {
		return "Enter sent, but could not confirm Dota 2 took focus", nil
	}
	return "Enter sent to Dota 2", nil
}

// enumCallback is created once: syscall.NewCallback allocations are never
// released, so per-call creation would leak.
var (
	enumTarget   uintptr
	enumMu       sync.Mutex
	enumCallback = syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		if r, _, _ := procIsWindowVisible.Call(hwnd); r == 0 {
			return 1 // continue
		}
		if windowTitle(hwnd) != "Dota 2" {
			return 1
		}
		// Titles are spoofable-by-accident (editors, terminals); require the
		// window to actually belong to dota2.exe before we send keys at it.
		if !strings.EqualFold(processExeName(hwnd), "dota2.exe") {
			return 1
		}
		enumTarget = hwnd
		return 0 // stop
	})
)

func findDotaWindow() uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumTarget = 0
	procEnumWindows.Call(enumCallback, 0)
	return enumTarget
}

func windowTitle(hwnd uintptr) string {
	var buf [128]uint16
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

func processExeName(hwnd uintptr) string {
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}
	h, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer procCloseHandle.Call(h)
	var buf [512]uint16
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImageName.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return ""
	}
	full := syscall.UTF16ToString(buf[:size])
	if i := strings.LastIndexAny(full, `\/`); i >= 0 {
		full = full[i+1:]
	}
	return full
}

func focusWindow(hwnd uintptr) bool {
	if r, _, _ := procIsIconic.Call(hwnd); r != 0 {
		procShowWindow.Call(hwnd, swRestore)
		time.Sleep(300 * time.Millisecond)
	}
	// Windows blocks SetForegroundWindow from background processes unless the
	// caller recently generated input; a synthetic Alt tap satisfies that.
	procKeybdEvent.Call(vkMenu, 0, 0, 0)
	procSetForegroundWindow.Call(hwnd)
	procKeybdEvent.Call(vkMenu, 0, keyeventfKeyUp, 0)
	time.Sleep(150 * time.Millisecond)
	fg, _, _ := procGetForegroundWindow.Call()
	return fg == hwnd
}

// keyboardInput mirrors KEYBDINPUT; input mirrors INPUT (40 bytes on x64,
// padded to the size of its largest union member, MOUSEINPUT).
type keyboardInput struct {
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type input struct {
	Type uint32
	_    uint32 // union is 8-byte aligned on x64
	Ki   keyboardInput
	_    [8]byte
}

// sendEnter injects Enter at scan-code level, which games accept even when
// they ignore "soft" virtual-key events.
func sendEnter() error {
	events := []input{
		{Type: inputKeyboard, Ki: keyboardInput{Scan: scanEnter, Flags: keyeventfScancode}},
		{Type: inputKeyboard, Ki: keyboardInput{Scan: scanEnter, Flags: keyeventfScancode | keyeventfKeyUp}},
	}
	for i := range events {
		r, _, err := procSendInput.Call(1, uintptr(unsafe.Pointer(&events[i])), unsafe.Sizeof(events[i]))
		if r != 1 {
			return fmt.Errorf("SendInput failed: %v", err)
		}
		time.Sleep(40 * time.Millisecond)
	}
	return nil
}

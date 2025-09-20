//go:build windows

package app

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

var (
	modUser32   = syscall.NewLazyDLL("user32.dll")
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")
	modShell32  = syscall.NewLazyDLL("shell32.dll")
	modGdi32    = syscall.NewLazyDLL("gdi32.dll")
	modOle32    = syscall.NewLazyDLL("ole32.dll")
)

var (
	procRegisterClassEx    = modUser32.NewProc("RegisterClassExW")
	procCreateWindowEx     = modUser32.NewProc("CreateWindowExW")
	procDefWindowProc      = modUser32.NewProc("DefWindowProcW")
	procDestroyWindow      = modUser32.NewProc("DestroyWindow")
	procShowWindow         = modUser32.NewProc("ShowWindow")
	procUpdateWindow       = modUser32.NewProc("UpdateWindow")
	procGetMessage         = modUser32.NewProc("GetMessageW")
	procTranslateMessage   = modUser32.NewProc("TranslateMessage")
	procDispatchMessage    = modUser32.NewProc("DispatchMessageW")
	procPostQuitMessage    = modUser32.NewProc("PostQuitMessage")
	procSendMessage        = modUser32.NewProc("SendMessageW")
	procSetWindowText      = modUser32.NewProc("SetWindowTextW")
	procGetWindowText      = modUser32.NewProc("GetWindowTextW")
	procGetWindowTextLen   = modUser32.NewProc("GetWindowTextLengthW")
	procCreateMenu         = modUser32.NewProc("CreatePopupMenu")
	procAppendMenu         = modUser32.NewProc("AppendMenuW")
	procTrackPopupMenu     = modUser32.NewProc("TrackPopupMenu")
	procSetForegroundWnd   = modUser32.NewProc("SetForegroundWindow")
	procGetCursorPos       = modUser32.NewProc("GetCursorPos")
	procLoadCursor         = modUser32.NewProc("LoadCursorW")
	procLoadIcon           = modUser32.NewProc("LoadIconW")
	procLoadImage          = modUser32.NewProc("LoadImageW")
	procDestroyIcon        = modUser32.NewProc("DestroyIcon")
	procSendDlgItemMessage = modUser32.NewProc("SendMessageW")
	procSetWindowPos       = modUser32.NewProc("SetWindowPos")
	procGetClientRect      = modUser32.NewProc("GetClientRect")
	procMoveWindow         = modUser32.NewProc("MoveWindow")
	procShellNotifyIcon    = modShell32.NewProc("Shell_NotifyIconW")
	procGetStockObject     = modGdi32.NewProc("GetStockObject")
	procPostMessage        = modUser32.NewProc("PostMessageW")
	procMessageBox         = modUser32.NewProc("MessageBoxW")
	procFindWindow         = modUser32.NewProc("FindWindowW")
	procIsIconic           = modUser32.NewProc("IsIconic")
	procIsWindow           = modUser32.NewProc("IsWindow")
	procCreateMutex        = modKernel32.NewProc("CreateMutexW")
	procReleaseMutex       = modKernel32.NewProc("ReleaseMutex")
	procCloseHandle        = modKernel32.NewProc("CloseHandle")
	procGetConsoleWindow   = modKernel32.NewProc("GetConsoleWindow")
	procEnableWindow       = modUser32.NewProc("EnableWindow")
	procCoInitializeEx     = modOle32.NewProc("CoInitializeEx")
	procCoUninitialize     = modOle32.NewProc("CoUninitialize")
	procCoCreateInstance   = modOle32.NewProc("CoCreateInstance")
)

const (
	wsOverlappedWindow = 0x00CF0000
	wsVisible          = 0x10000000
	wsChild            = 0x40000000
	wsTabStop          = 0x00010000
	wsExAppWindow      = 0x00040000
	wsExClientEdge     = 0x00000200

	cwUseDefault = 0x80000000

	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmCommand       = 0x0111
	wmContextMenu   = 0x007B
	wmRButtonUp     = 0x0205
	wmLButtonDblClk = 0x0203
	wmApp           = 0x8000
	wmUser          = 0x0400
	wmSetFont       = 0x0030
	wmSetIcon       = 0x0080

	swHide    = 0
	swShow    = 5
	swRestore = 9

	bsPushButton  = 0x00000000
	esAutoHScroll = 0x0004

	mfString    = 0x00000000
	mfSeparator = 0x00000800

	tpmLeftAlign   = 0x0000
	tpmRightButton = 0x0002
	tpmBottomAlign = 0x0020

	niifInfo   = 0x00000001
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nidAdd     = 0x00000000
	nidModify  = 0x00000001
	nidDelete  = 0x00000002

	mbOK              = 0x00000000
	mbIconInformation = 0x00000040
	mbIconWarning     = 0x00000030
	mbIconError       = 0x00000010
)

const (
	clsctxInprocServer      = 0x00000001
	coinitApartmentThreaded = 0x00000002
	swShowNormal            = 1
	sFalse                  = 0x00000001
	rpcEChangedMode         = 0x80010106
)

var (
	clsidShellLink  = syscall.GUID{Data1: 0x00021401, Data2: 0, Data3: 0, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIShellLinkW  = syscall.GUID{Data1: 0x000214F9, Data2: 0, Data3: 0, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIPersistFile = syscall.GUID{Data1: 0x0000010b, Data2: 0, Data3: 0, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   syscall.Handle
	Icon       syscall.Handle
	Cursor     syscall.Handle
	Background syscall.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     syscall.Handle
}

type point struct {
	X int32
	Y int32
}

type msg struct {
	Hwnd    syscall.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type notifyIconData struct {
	Size             uint32
	HWnd             syscall.Handle
	ID               uint32
	Flags            uint32
	CallbackMsg      uint32
	HIcon            syscall.Handle
	Tip              [128]uint16
	State            uint32
	StateMask        uint32
	Info             [256]uint16
	TimeoutOrVersion uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GUIDItem         [16]byte
	BalloonIcon      syscall.Handle
}

func registerClass(className string, wndProc uintptr, icon syscall.Handle) error {
	hInstance, _, _ := modKernel32.NewProc("GetModuleHandleW").Call(0)
	clsName, _ := syscall.UTF16PtrFromString(className)
	cursor, _, _ := procLoadCursor.Call(0, uintptr(32512))
	wc := wndClassEx{
		Size:       uint32(unsafe.Sizeof(wndClassEx{})),
		Style:      0,
		WndProc:    wndProc,
		ClsExtra:   0,
		WndExtra:   0,
		Instance:   syscall.Handle(hInstance),
		Icon:       icon,
		Cursor:     syscall.Handle(cursor),
		Background: 6, // COLOR_WINDOW
		MenuName:   nil,
		ClassName:  clsName,
		IconSm:     icon,
	}
	atom, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		return err
	}
	return nil
}

func createWindow(className, title string, width, height int32, param uintptr, visible bool) (syscall.Handle, error) {
	hInstance, _, _ := modKernel32.NewProc("GetModuleHandleW").Call(0)
	clsName, _ := syscall.UTF16PtrFromString(className)
	wndTitle, _ := syscall.UTF16PtrFromString(title)
	exStyle := uintptr(0)
	style := uintptr(wsOverlappedWindow)
	if visible {
		exStyle = uintptr(wsExAppWindow)
		style |= wsVisible
	}
	hwnd, _, err := procCreateWindowEx.Call(
		exStyle,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(wndTitle)),
		style,
		uintptr(cwUseDefault),
		uintptr(cwUseDefault),
		uintptr(width),
		uintptr(height),
		0,
		0,
		hInstance,
		param,
	)
	if hwnd == 0 {
		return 0, err
	}
	return syscall.Handle(hwnd), nil
}

func showWindow(hwnd syscall.Handle) {
	procShowWindow.Call(uintptr(hwnd), swShow)
	procUpdateWindow.Call(uintptr(hwnd))
}

func hideWindow(hwnd syscall.Handle) {
	procShowWindow.Call(uintptr(hwnd), 0)
}

func messageLoop() int {
	var m msg
	for {
		ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		switch int32(ret) {
		case -1:
			return -1
		case 0:
			return int(m.WParam)
		default:
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
		}
	}
}

func defWindowProc(hwnd syscall.Handle, msg uint32, wparam, lparam uintptr) uintptr {
	ret, _, _ := procDefWindowProc.Call(uintptr(hwnd), uintptr(msg), wparam, lparam)
	return ret
}

func postQuitMessage(code int32) {
	procPostQuitMessage.Call(uintptr(code))
}

func destroyWindow(hwnd syscall.Handle) {
	procDestroyWindow.Call(uintptr(hwnd))
}

func setWindowText(hwnd syscall.Handle, text string) {
	ptr, _ := syscall.UTF16PtrFromString(text)
	procSetWindowText.Call(uintptr(hwnd), uintptr(unsafe.Pointer(ptr)))
}

func getWindowText(hwnd syscall.Handle) string {
	length, _, _ := procGetWindowTextLen.Call(uintptr(hwnd))
	buf := make([]uint16, length+1)
	procGetWindowText.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), length+1)
	return syscall.UTF16ToString(buf)
}

func sendMessage(hwnd syscall.Handle, msg uint32, wparam, lparam uintptr) uintptr {
	ret, _, _ := procSendMessage.Call(uintptr(hwnd), uintptr(msg), wparam, lparam)
	return ret
}

func setWindowPos(hwnd syscall.Handle, x, y, cx, cy int32, flags uintptr) {
	procSetWindowPos.Call(uintptr(hwnd), 0, uintptr(x), uintptr(y), uintptr(cx), uintptr(cy), flags)
}

func moveWindow(hwnd syscall.Handle, x, y, cx, cy int32, repaint bool) {
	repaintFlag := uintptr(0)
	if repaint {
		repaintFlag = 1
	}
	procMoveWindow.Call(uintptr(hwnd), uintptr(x), uintptr(y), uintptr(cx), uintptr(cy), repaintFlag)
}

func restoreWindow(hwnd syscall.Handle) {
	if hwnd == 0 {
		return
	}
	procShowWindow.Call(uintptr(hwnd), swRestore)
	procSetForegroundWnd.Call(uintptr(hwnd))
}

func setWindowIconHandle(hwnd, icon syscall.Handle) bool {
	if hwnd == 0 || icon == 0 {
		return false
	}
	procSendMessage.Call(uintptr(hwnd), wmSetIcon, iconSmall, uintptr(icon))
	procSendMessage.Call(uintptr(hwnd), wmSetIcon, iconBig, uintptr(icon))
	return true
}

func findWindowByTitle(title string) syscall.Handle {
	if strings.TrimSpace(title) == "" {
		return 0
	}
	ptr, _ := syscall.UTF16PtrFromString(title)
	hwnd, _, _ := procFindWindow.Call(0, uintptr(unsafe.Pointer(ptr)))
	return syscall.Handle(hwnd)
}

func isWindowHandleValid(hwnd syscall.Handle) bool {
	ret, _, _ := procIsWindow.Call(uintptr(hwnd))
	return ret != 0
}

func isWindowIconic(hwnd syscall.Handle) bool {
	ret, _, _ := procIsIconic.Call(uintptr(hwnd))
	return ret != 0
}

func getClientRect(hwnd syscall.Handle) rect {
	var r rect
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&r)))
	return r
}

var errNotifyIcon = errors.New("shell notify icon failed")

func shellNotifyIcon(msg uint32, data *notifyIconData) error {
	data.Size = uint32(unsafe.Sizeof(*data))
	ret, _, err := procShellNotifyIcon.Call(uintptr(msg), uintptr(unsafe.Pointer(data)))
	if ret == 0 {
		if err != syscall.Errno(0) {
			return err
		}
		return errNotifyIcon
	}
	return nil
}

func loadIconFromFile(path string) syscall.Handle {
	if path == "" {
		return 0
	}
	ptr, _ := syscall.UTF16PtrFromString(path)
	handle, _, _ := procLoadImage.Call(0, uintptr(unsafe.Pointer(ptr)), 1, 0, 0, 0x00000010|0x00000080)
	return syscall.Handle(handle)
}

func loadIconFromBytes(data []byte) syscall.Handle {
	if len(data) == 0 {
		return 0
	}
	file, err := os.CreateTemp("", "vrchat-notification-icon-*.ico")
	if err != nil {
		return 0
	}
	name := file.Name()
	if _, err = file.Write(data); err != nil {
		file.Close()
		os.Remove(name)
		return 0
	}
	if err = file.Close(); err != nil {
		os.Remove(name)
		return 0
	}
	handle := loadIconFromFile(name)
	os.Remove(name)
	return handle
}

func loadDefaultIcon() syscall.Handle {
	handle, _, _ := procLoadIcon.Call(0, 32512) // IDI_APPLICATION
	return syscall.Handle(handle)
}

func destroyIcon(icon syscall.Handle) {
	if icon == 0 {
		return
	}
	procDestroyIcon.Call(uintptr(icon))
}

func getConsoleWindowHandle() syscall.Handle {
	hwnd, _, _ := procGetConsoleWindow.Call()
	return syscall.Handle(hwnd)
}

func hideConsoleWindow() {
	hwnd := getConsoleWindowHandle()
	if hwnd == 0 {
		return
	}
	procShowWindow.Call(uintptr(hwnd), swHide)
}

func getCursorPos() (point, bool) {
	var p point
	ret, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p, ret != 0
}

const (
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010
)

const (
	iconSmall = 0
	iconBig   = 1
)

const (
	wmTrayMessage = wmApp + 1
	wmEventNotify = wmApp + 2
)

func makeMenu() syscall.Handle {
	menu, _, _ := procCreateMenu.Call()
	return syscall.Handle(menu)
}

func appendMenu(menu syscall.Handle, flags uint32, id uint16, text string) {
	ptr, _ := syscall.UTF16PtrFromString(text)
	procAppendMenu.Call(uintptr(menu), uintptr(flags), uintptr(id), uintptr(unsafe.Pointer(ptr)))
}

func trackPopupMenu(menu syscall.Handle, flags uint32, x, y int32, hwnd syscall.Handle) {
	procSetForegroundWnd.Call(uintptr(hwnd))
	procTrackPopupMenu.Call(uintptr(menu), uintptr(flags), uintptr(x), uintptr(y), 0, uintptr(hwnd), 0)
}

func defaultUIFont() syscall.Handle {
	handle, _, _ := procGetStockObject.Call(17) // DEFAULT_GUI_FONT
	return syscall.Handle(handle)
}

func createControl(className, text string, style uint32, x, y, width, height int32, parent syscall.Handle, id uint16, exStyle uint32) syscall.Handle {
	clsPtr, _ := syscall.UTF16PtrFromString(className)
	txtPtr, _ := syscall.UTF16PtrFromString(text)
	hwnd, _, _ := procCreateWindowEx.Call(
		uintptr(exStyle),
		uintptr(unsafe.Pointer(clsPtr)),
		uintptr(unsafe.Pointer(txtPtr)),
		uintptr(style|wsChild|wsVisible),
		uintptr(x),
		uintptr(y),
		uintptr(width),
		uintptr(height),
		uintptr(parent),
		uintptr(id),
		0,
		0,
	)
	return syscall.Handle(hwnd)
}

func enableWindow(hwnd syscall.Handle, enabled bool) {
	var flag uintptr
	if enabled {
		flag = 1
	}
	procEnableWindow.Call(uintptr(hwnd), flag)
}

func postMessage(hwnd syscall.Handle, msg uint32, wparam, lparam uintptr) {
	procPostMessage.Call(uintptr(hwnd), uintptr(msg), wparam, lparam)
}

func messageBox(hwnd syscall.Handle, text, title string, flags uint32) int {
	txtPtr, _ := syscall.UTF16PtrFromString(text)
	titlePtr, _ := syscall.UTF16PtrFromString(title)
	ret, _, _ := procMessageBox.Call(uintptr(hwnd), uintptr(unsafe.Pointer(txtPtr)), uintptr(unsafe.Pointer(titlePtr)), uintptr(flags))
	return int(ret)
}

func createNamedMutex(name string) (syscall.Handle, syscall.Errno) {
	ptr, _ := syscall.UTF16PtrFromString(name)
	handle, _, err := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(ptr)))
	if handle == 0 {
		if errno, ok := err.(syscall.Errno); ok {
			return 0, errno
		}
		return 0, syscall.Errno(0)
	}
	return syscall.Handle(handle), 0
}

func releaseMutex(handle syscall.Handle) {
	if handle != 0 {
		procReleaseMutex.Call(uintptr(handle))
	}
}

func closeHandle(handle syscall.Handle) {
	if handle != 0 {
		procCloseHandle.Call(uintptr(handle))
	}
}

type iShellLinkW struct {
	lpVtbl *iShellLinkWVtbl
}

type iShellLinkWVtbl struct {
	QueryInterface      uintptr
	AddRef              uintptr
	Release             uintptr
	GetPath             uintptr
	GetIDList           uintptr
	SetIDList           uintptr
	GetDescription      uintptr
	SetDescription      uintptr
	GetWorkingDirectory uintptr
	SetWorkingDirectory uintptr
	GetArguments        uintptr
	SetArguments        uintptr
	GetHotkey           uintptr
	SetHotkey           uintptr
	GetShowCmd          uintptr
	SetShowCmd          uintptr
	GetIconLocation     uintptr
	SetIconLocation     uintptr
	SetRelativePath     uintptr
	Resolve             uintptr
	SetPath             uintptr
}

type iPersistFile struct {
	lpVtbl *iPersistFileVtbl
}

type iPersistFileVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	GetClassID     uintptr
	IsDirty        uintptr
	Load           uintptr
	Save           uintptr
	SaveCompleted  uintptr
	GetCurFile     uintptr
}

func (sl *iShellLinkW) Release() {
	if sl == nil || sl.lpVtbl == nil {
		return
	}
	syscall.SyscallN(sl.lpVtbl.Release, uintptr(unsafe.Pointer(sl)))
}

func (sl *iShellLinkW) QueryInterface(riid *syscall.GUID, obj unsafe.Pointer) error {
	if sl == nil || sl.lpVtbl == nil {
		return fmt.Errorf("shell link interface not initialised")
	}
	hr, _, _ := syscall.SyscallN(sl.lpVtbl.QueryInterface, uintptr(unsafe.Pointer(sl)), uintptr(unsafe.Pointer(riid)), uintptr(obj))
	return hresultToError("QueryInterface", hr)
}

func (sl *iShellLinkW) SetPath(path string) error {
	if sl == nil || sl.lpVtbl == nil {
		return fmt.Errorf("shell link interface not initialised")
	}
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	hr, _, _ := syscall.SyscallN(sl.lpVtbl.SetPath, uintptr(unsafe.Pointer(sl)), uintptr(unsafe.Pointer(ptr)))
	return hresultToError("SetPath", hr)
}

func (sl *iShellLinkW) SetWorkingDirectory(dir string) error {
	if sl == nil || sl.lpVtbl == nil {
		return fmt.Errorf("shell link interface not initialised")
	}
	ptr, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	hr, _, _ := syscall.SyscallN(sl.lpVtbl.SetWorkingDirectory, uintptr(unsafe.Pointer(sl)), uintptr(unsafe.Pointer(ptr)))
	return hresultToError("SetWorkingDirectory", hr)
}

func (sl *iShellLinkW) SetArguments(args string) error {
	if sl == nil || sl.lpVtbl == nil {
		return fmt.Errorf("shell link interface not initialised")
	}
	ptr, err := syscall.UTF16PtrFromString(args)
	if err != nil {
		return err
	}
	hr, _, _ := syscall.SyscallN(sl.lpVtbl.SetArguments, uintptr(unsafe.Pointer(sl)), uintptr(unsafe.Pointer(ptr)))
	return hresultToError("SetArguments", hr)
}

func (sl *iShellLinkW) SetDescription(desc string) error {
	if sl == nil || sl.lpVtbl == nil {
		return fmt.Errorf("shell link interface not initialised")
	}
	ptr, err := syscall.UTF16PtrFromString(desc)
	if err != nil {
		return err
	}
	hr, _, _ := syscall.SyscallN(sl.lpVtbl.SetDescription, uintptr(unsafe.Pointer(sl)), uintptr(unsafe.Pointer(ptr)))
	return hresultToError("SetDescription", hr)
}

func (sl *iShellLinkW) SetShowCmd(cmd int32) error {
	if sl == nil || sl.lpVtbl == nil {
		return fmt.Errorf("shell link interface not initialised")
	}
	hr, _, _ := syscall.SyscallN(sl.lpVtbl.SetShowCmd, uintptr(unsafe.Pointer(sl)), uintptr(cmd))
	return hresultToError("SetShowCmd", hr)
}

func (pf *iPersistFile) Release() {
	if pf == nil || pf.lpVtbl == nil {
		return
	}
	syscall.SyscallN(pf.lpVtbl.Release, uintptr(unsafe.Pointer(pf)))
}

func (pf *iPersistFile) Save(path string, remember bool) error {
	if pf == nil || pf.lpVtbl == nil {
		return fmt.Errorf("persist file interface not initialised")
	}
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var keep uintptr
	if remember {
		keep = 1
	}
	hr, _, _ := syscall.SyscallN(pf.lpVtbl.Save, uintptr(unsafe.Pointer(pf)), uintptr(unsafe.Pointer(ptr)), keep)
	return hresultToError("IPersistFile::Save", hr)
}

func (pf *iPersistFile) SaveCompleted() error {
	if pf == nil || pf.lpVtbl == nil {
		return fmt.Errorf("persist file interface not initialised")
	}
	hr, _, _ := syscall.SyscallN(pf.lpVtbl.SaveCompleted, uintptr(unsafe.Pointer(pf)), 0)
	return hresultToError("IPersistFile::SaveCompleted", hr)
}

func initializeCOM() (bool, error) {
	hr, _, err := procCoInitializeEx.Call(0, uintptr(coinitApartmentThreaded))
	switch hr {
	case 0:
		return true, nil
	case sFalse:
		return true, nil
	case rpcEChangedMode:
		return false, nil
	default:
		if int32(hr) >= 0 {
			return false, nil
		}
		if err != nil && err != syscall.Errno(0) {
			return false, err
		}
		return false, fmt.Errorf("CoInitializeEx failed with HRESULT 0x%08X", uint32(hr))
	}
}

func hresultToError(action string, hr uintptr) error {
	if int32(hr) >= 0 {
		return nil
	}
	return fmt.Errorf("%s failed with HRESULT 0x%08X", action, uint32(hr))
}

func createShortcut(shortcutPath, targetPath, arguments, workingDir, description string) error {
	initialized, err := initializeCOM()
	if err != nil {
		return err
	}
	if initialized {
		defer procCoUninitialize.Call()
	}

	var link *iShellLinkW
	hr, _, callErr := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidShellLink)),
		0,
		uintptr(clsctxInprocServer),
		uintptr(unsafe.Pointer(&iidIShellLinkW)),
		uintptr(unsafe.Pointer(&link)),
	)
	if int32(hr) < 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return callErr
		}
		return fmt.Errorf("CoCreateInstance failed with HRESULT 0x%08X", uint32(hr))
	}
	if link == nil {
		return fmt.Errorf("CoCreateInstance returned nil shell link")
	}
	defer link.Release()

	if err := link.SetPath(targetPath); err != nil {
		return err
	}
	if strings.TrimSpace(arguments) != "" {
		if err := link.SetArguments(arguments); err != nil {
			return err
		}
	}
	if strings.TrimSpace(workingDir) != "" {
		if err := link.SetWorkingDirectory(workingDir); err != nil {
			return err
		}
	}
	if strings.TrimSpace(description) != "" {
		if err := link.SetDescription(description); err != nil {
			return err
		}
	}
	if err := link.SetShowCmd(swShowNormal); err != nil {
		return err
	}

	var persist *iPersistFile
	if err := link.QueryInterface(&iidIPersistFile, unsafe.Pointer(&persist)); err != nil {
		return err
	}
	if persist == nil {
		return fmt.Errorf("QueryInterface returned nil IPersistFile")
	}
	defer persist.Release()

	if err := persist.Save(shortcutPath, true); err != nil {
		return err
	}
	if err := persist.SaveCompleted(); err != nil {
		return err
	}
	return nil
}

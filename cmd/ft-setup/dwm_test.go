//go:build windows

package main

import (
	"syscall"
	"testing"
)

func TestSetupWin32ProcsResolve(t *testing.T) {
	procs := []*syscall.LazyProc{
		procWizRegisterClassExW,
		procWizCreateWindowExW,
		procWizDefWindowProcW,
		procWizDestroyWindow,
		procWizGetMessageW,
		procWizTranslateMessage,
		procWizDispatchMessageW,
		procWizPostMessageW,
		procWizPostQuitMessage,
		procWizSendMessageW,
		procWizGetWindowTextLengthW,
		procWizGetWindowTextW,
		procWizSetWindowTextW,
		procWizEnableWindow,
		procWizShowWindow,
		procWizUpdateWindow,
		procWizInvalidateRect,
		procWizBeginPaint,
		procWizEndPaint,
		procWizGetClientRect,
		procWizLoadCursorW,
		procWizLoadIconW,
		procWizLoadImageW,
		procWizSystemParametersInfoW,
		procWizSetDPIAware,
		procWizTrackMouseEvent,
		procWizSetCursor,
		procWizScreenToClient,
		procWizSetWindowPos,
		procWizSetTimer,
		procWizKillTimer,
		procWizDrawTextW,
		procWizSetLayeredWindowAttributes,
		procWizGetModuleHandleW,
		procWizCreateFontW,
		procWizSelectObject,
		procWizDeleteObject,
		procWizCreateSolidBrush,
		procWizCreatePen,
		procWizSetBkMode,
		procWizSetBkColor,
		procWizSetTextColor,
		procWizCreateDIBSection,
		procWizGdiFlush,
		procWizCreateCompatDC,
		procWizDeleteDC,
		procWizBitBlt,
		procWizMoveToEx,
		procWizLineTo,
		procWizTextFaceW,
		procWizDwmSetWindowAttribute,
		procWizDwmExtendFrameIntoClient,
		procWizSHBrowseForFolderW,
		procWizSHGetPathFromIDListW,
		procWizCoInitializeEx,
		procWizCoUninitialize,
		procWizCoTaskMemFree,
		procWizRegGetValueW,
	}
	for _, p := range procs {
		if err := p.Find(); err != nil {
			t.Fatalf("LazyProc %s failed to resolve: %v", p.Name, err)
		}
	}
}

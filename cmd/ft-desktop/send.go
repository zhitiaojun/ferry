//go:build windows

package main

import (
	"fmt"
)

// sendFiles sends the dropped files to the first paired, online device over the
// relay. It is the drop window's action; the tray UI for picking a device comes
// in a later step.
func (d *dropWindow) sendFiles(files []string) error {
	return fmt.Errorf("尚未选择接收设备（投放小窗暂只记录文件，设备选择在下一步实现）")
}

# 拖动时浮现投放面板：可行机制调研（M10 前置）

日期：2026-11-09。目的：桌面端“平常不可见、拖动文件时出现投放面板”，需在 Win32（Go + LazyDLL，无 cgo）条件下可靠实现。

## 结论

1. **正解：监听 shell 的拖动图像窗口 `SysDragImage`**。拖动开始/结束时，shell 会创建/销毁一个类名为 `SysDragImage` 的顶层窗口；用 `SetWinEventHook(EVENT_OBJECT_CREATE / EVENT_OBJECT_DESTROY, WINEVENT_OUTOFCONTEXT)` 监听，命中类名即拖动开始/结束。不装鼠标钩子、不误判文字选中。
2. 面板窗口：`WS_POPUP | WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE | WS_EX_TOPMOST`，`DragAcceptFiles(TRUE)`，`WM_DROPFILES` 拿 `CF_HDROP` 路径；不抢焦点，否则会中断源拖动。
3. 备选：`WH_MOUSE_LL` 低级鼠标钩子（按下左键 + 位移超阈值）。粗糙、会误触发（拖窗口/框选），只作为 `SysDragImage` 不可用时的降级。
4. 不采用“全屏透明窗口”：会吃掉所有点击并挡住资源管理器，且 UIPI 下无效。

## 机制 A：SysDragImage + WinEvent

- 微软 Q&A 中给出的可行方案：`UIAutomation` 结构变化事件里找 `ClassName == "SysDragImage"` 的子窗口；同一线程里也有人用 WinEvent 的 `EVENT_SYSTEM_CAPTURESTART`（噪声大，不推荐）。
- 我们用更轻的 `SetWinEventHook`：
  - `EVENT_OBJECT_CREATE`（0x8000）→ `GetClassNameW(hwnd)` 等于 `SysDragImage` → 拖动开始；
  - `EVENT_OBJECT_DESTROY`（0x8001）→ 记录过的 `hwnd` 被销毁 → 拖动结束（放下/取消）。
- Go 实现要点：`syscall.NewCallback` 注册签名 `func(hWinEventHook, event uint32, hwnd HWND, idObject, idChild int32, idThread, ms uint32) uintptr` 的回调；`SetWinEventHook(1, 0x8001, 0, cb, 0, 0, WINEVENT_OUTOFCONTEXT|WINEVENT_SKIPOWNPROCESS)`；回调里只做 `PostMessage(hwndPanel, WM_APP_DRAG_START/END, 0, 0)`，严禁在回调里做耗时操作（会拖慢全系统）。
- 注意：文字/图片拖动也会产生 `SysDragImage`。面板可以出现，但只有在收到 `WM_DROPFILES`（或 OLE `Drop`）时才真正发送；无文件的拖动松手即消失，代价是“闪一下”。若不可接受，可在 `DragEnter` 里读 `IDataObject` 判断是否含 `CF_HDROP` 再决定显示。
- 事件顺序：`EVENT_OBJECT_CREATE` 可能先于窗口真正显示；建议 create 后 120–180 ms 再显示面板（避开“点了鼠标没拖”的瞬时对象），并用 `GetCursorPos` 定位。

## 机制 B：WH_MOUSE_LL（降级）

- `SetWindowsHookEx(WH_MOUSE_LL, cb, hInstance, 0)`；回调里 `WM_LBUTTONDOWN` 记起点，`WM_MOUSEMOVE` 位移 > 4–6 px 即判定拖动候选，`WM_LBUTTONUP` 结束。
- 缺点：拖窗口边框、框选文字都会触发；需要 150–200 ms 去抖，并结合 `GetForegroundWindow`/`WindowFromPoint` 排除自身窗口。
- 只在机制 A 拿不到事件（例如第三方拖动实现）时启用。

## 放下与传输

- 面板 `DragAcceptFiles(TRUE)` 即可收 `WM_DROPFILES`；此时 `DragQueryFileW` 取路径数组。
- 若需要自定义拖入高亮/更精确的落点反馈，改用 OLE：`RegisterDragDrop(hwndPanel, IDropTarget)`，实现 `DragEnter/DragOver/DragLeave/Drop`；但 Go 无 cgo 手写 COM vtable 成本高，v1 用 `WM_DROPFILES` 足够。
- UIPI：管理员进程拖出的文件不会进入普通权限进程；面板检测到 `GetWindowThreadProcessId` 属于高完整性进程时，提示“请用右键「用 ft 发送」”。

## 与主界面的关系

- 主界面（880×520，托盘打开）承担设备管理、台账、接收开关与配对；面板是“快速通道”。
- 两者共享同一数据源（`core/client` 的 Peers/台账）与同一发送入口（`core/mobile.SendAuto`/`core/client.SendFiles`），面板不另做业务逻辑。

## 参考

- Microsoft Q&A：<https://learn.microsoft.com/en-us/answers/questions/2244534/how-to-detect-system-wide-drag-drop-start-outside>（`SysDragImage` 检测、WinEvent/UIA 讨论）
- WinEvents 基础设施：<https://learn.microsoft.com/en-us/windows/win32/winauto/winevents-infrastructure>
- `SetWinEventHook`：<https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-setwineventhook>
- `RegisterDragDrop` / `IDropTarget`：<https://learn.microsoft.com/en-us/windows/win32/api/ole2/nf-ole2-registerdragdrop>
- `DragAcceptFiles` / `WM_DROPFILES`：<https://learn.microsoft.com/en-us/windows/win32/shell/dragdrop>

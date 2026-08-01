package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	appName       = "TimeClicker"
	appGitHubURL  = "https://github.com/stevengeyue"
	appCreditLine = "GitHub: https://github.com/stevengeyue"
	settingsName  = "settings.json"
	recordsName   = "records.ndjson"
	mutexName      = `Local\TimeClicker.SingleInstance`
	windowWidth    = 300
	windowHeight   = 168
	startupRegPath = `Software\Microsoft\Windows\CurrentVersion\Run`
)

type appSettings struct {
	CopyFormat       string `json:"copyFormat"`
	ShowWidget       bool   `json:"showWidget"`
	AlwaysOnTop      bool   `json:"alwaysOnTop"`
	WidgetCorner     string `json:"widgetCorner"`
	WidgetX          int    `json:"widgetX"`
	WidgetY          int    `json:"widgetY"`
	StartWithWindows bool   `json:"startWithWindows"`
}

type recordEntry struct {
	Local      string `json:"local"`
	UTC        string `json:"utc"`
	EpochMS    int64  `json:"epochMs"`
	CopiedText string `json:"copiedText"`
	FormatID   string `json:"formatId"`
}

type formatOption struct {
	Label string
	Value string
	ID    string
}

var formats = []formatOption{
	{Label: "yyyy-MM-dd HH:mm:ss", Value: "yyyy-MM-dd HH:mm:ss", ID: "default_seconds"},
	{Label: "yyyy/MM/dd HH:mm", Value: "yyyy/MM/dd HH:mm", ID: "slash_minutes"},
	{Label: "HH:mm:ss", Value: "HH:mm:ss", ID: "time_seconds"},
	{Label: "yyyy-MM-dd", Value: "yyyy-MM-dd", ID: "date"},
	{Label: "ISO 8601", Value: "ISO 8601", ID: "iso8601"},
}

var (
	mw           *walk.MainWindow
	notifyIcon   *walk.NotifyIcon
	currentLabel *walk.Label
	lastLabel    *walk.Label
	formatLabel  *walk.Label
	topMostAction *walk.Action
	startupAction *walk.Action
	settings     appSettings
	appDir       string
	settingsPath string
	recordsPath  string
	lastRecord   = "尚未记录"
	mutexHandle  windows.Handle
)

func main() {
	initPaths()
	loadSettings()
	lastRecord = loadLastRecord()

	if len(os.Args) > 1 && os.Args[1] == "--record-once" {
		if err := recordNow(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if alreadyRunning() {
		walk.MsgBox(nil, appName, "TimeClicker 已经在运行，请从系统托盘打开。", walk.MsgBoxIconInformation)
		return
	}
	defer windows.CloseHandle(mutexHandle)

	icon, err := makeClockIcon()
	if err != nil {
		fatal(err)
	}
	defer icon.Dispose()

	if err := buildWindow(icon); err != nil {
		fatal(err)
	}

	if err := buildTray(icon); err != nil {
		fatal(err)
	}
	defer notifyIcon.Dispose()

	applyToolWindowStyle()
	applyTopMost()
	restoreWindowPlacement()
	refreshLabels()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	go func() {
		for range ticker.C {
			if mw != nil {
				mw.Synchronize(refreshLabels)
			}
		}
	}()

	if settings.ShowWidget {
		mw.Show()
	} else {
		mw.Hide()
	}

	mw.Run()
}

func buildWindow(icon *walk.Icon) error {
	err := MainWindow{
		AssignTo: &mw,
		Title:    appName,
		Icon:     icon,
		MinSize:  Size{windowWidth, windowHeight},
		MaxSize:  Size{windowWidth, windowHeight},
		Size:     Size{windowWidth, windowHeight},
		Layout: VBox{
			Margins: Margins{Left: 12, Top: 10, Right: 12, Bottom: 10},
			Spacing: 4,
		},
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 8},
				Children: []Widget{
					Composite{
						Layout: VBox{MarginsZero: true, Spacing: 2},
						Children: []Widget{
							Label{Text: "当前时间"},
							Label{
								AssignTo: &currentLabel,
								Font:     Font{PointSize: 12, Bold: true},
							},
							Label{Text: "上次记录"},
							Label{
								AssignTo: &lastLabel,
								Font:     Font{PointSize: 10, Bold: true},
							},
							Label{
								AssignTo: &formatLabel,
							},
						},
					},
					PushButton{
						Text:      "复制",
						MinSize:   Size{72, 32},
						MaxSize:   Size{72, 32},
						OnClicked: handleCopyOnly,
					},
				},
			},
			PushButton{
				Text:      "记录",
				OnClicked: handleRecord,
			},
		},
		OnBoundsChanged: func() {
			if mw != nil && mw.Visible() {
				saveWindowBounds()
			}
		},
	}.Create()
	if err != nil {
		return err
	}
	mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		*canceled = true
		settings.ShowWidget = false
		saveWindowBounds()
		saveSettings()
		mw.Hide()
	})
	return nil
}

func buildTray(icon *walk.Icon) error {
	var err error
	notifyIcon, err = walk.NewNotifyIcon(mw)
	if err != nil {
		return err
	}
	if err = notifyIcon.SetIcon(icon); err != nil {
		return err
	}
	if err = notifyIcon.SetToolTip(trayTip()); err != nil {
		return err
	}

	notifyIcon.MouseUp().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			if mw != nil && !mw.Visible() {
				showWidget()
				return
			}
			handleRecord()
		}
	})

	actions := notifyIcon.ContextMenu().Actions()
	if err = addAction(actions, "记录当前时间", handleRecord); err != nil {
		return err
	}
	if err = addAction(actions, "显示/隐藏桌面小窗", toggleWidget); err != nil {
		return err
	}
	if err = actions.Add(walk.NewSeparatorAction()); err != nil {
		return err
	}

	formatMenu, err := walk.NewMenu()
	if err != nil {
		return err
	}
	formatAction := walk.NewMenuAction(formatMenu)
	formatAction.SetText("复制格式")
	if err = actions.Add(formatAction); err != nil {
		return err
	}
	for _, opt := range formats {
		option := opt
		if err = addAction(formatMenu.Actions(), option.Label, func() {
			settings.CopyFormat = option.Value
			saveSettings()
			refreshLabels()
		}); err != nil {
			return err
		}
	}
	if err = formatMenu.Actions().Add(walk.NewSeparatorAction()); err != nil {
		return err
	}
	if err = addAction(formatMenu.Actions(), "自定义...", editCustomFormat); err != nil {
		return err
	}

	if err = actions.Add(walk.NewSeparatorAction()); err != nil {
		return err
	}
	topMostAction, err = addCheckAction(actions, "", settings.AlwaysOnTop, toggleTopMost)
	if err != nil {
		return err
	}
	startupAction, err = addCheckAction(actions, "", settings.StartWithWindows, toggleStartup)
	if err != nil {
		return err
	}
	refreshToggleActions()
	if err = addAction(actions, "打开日志文件", openLogFile); err != nil {
		return err
	}
	if err = addAction(actions, "GitHub: stevengeyue", openGitHub); err != nil {
		return err
	}
	if err = addAction(actions, "关于 TimeClicker", showAbout); err != nil {
		return err
	}
	if err = actions.Add(walk.NewSeparatorAction()); err != nil {
		return err
	}
	if err = addAction(actions, "退出", quitApp); err != nil {
		return err
	}

	return notifyIcon.SetVisible(true)
}

func addAction(actions *walk.ActionList, text string, fn func()) error {
	action := walk.NewAction()
	if err := action.SetText(text); err != nil {
		return err
	}
	action.Triggered().Attach(fn)
	return actions.Add(action)
}

func addCheckAction(actions *walk.ActionList, text string, checked bool, fn func()) (*walk.Action, error) {
	action := walk.NewAction()
	if err := action.SetText(text); err != nil {
		return nil, err
	}
	if err := action.SetCheckable(true); err != nil {
		return nil, err
	}
	if err := action.SetChecked(checked); err != nil {
		return nil, err
	}
	action.Triggered().Attach(fn)
	return action, actions.Add(action)
}

func handleRecord() {
	if err := recordNow(); err != nil {
		walk.MsgBox(mw, "记录失败", err.Error(), walk.MsgBoxIconError)
		return
	}
	refreshLabels()
	_ = notifyIcon.SetToolTip(trayTip())
}

func handleCopyOnly() {
	copied := formatTime(time.Now(), settings.CopyFormat)
	if err := walk.Clipboard().SetText(copied); err != nil {
		walk.MsgBox(mw, "复制失败", err.Error(), walk.MsgBoxIconError)
	}
}

func recordNow() error {
	now := time.Now()
	copied := formatTime(now, settings.CopyFormat)
	rec := recordEntry{
		Local:      now.Format("2006-01-02 15:04:05"),
		UTC:        now.UTC().Format(time.RFC3339),
		EpochMS:    now.UnixMilli(),
		CopiedText: copied,
		FormatID:   formatID(settings.CopyFormat),
	}
	if err := appendRecord(rec); err != nil {
		return err
	}
	if err := walk.Clipboard().SetText(copied); err != nil {
		return err
	}
	lastRecord = copied
	return nil
}

func toggleWidget() {
	settings.ShowWidget = !settings.ShowWidget
	if settings.ShowWidget {
		showWidget()
	} else {
		saveWindowBounds()
		mw.Hide()
	}
	saveSettings()
}

func showWidget() {
	settings.ShowWidget = true
	restoreWindowPlacement()
	mw.Show()
	mw.Activate()
	saveSettings()
}

func toggleTopMost() {
	if topMostAction != nil {
		settings.AlwaysOnTop = topMostAction.Checked()
	} else {
		settings.AlwaysOnTop = !settings.AlwaysOnTop
	}
	applyTopMost()
	saveSettings()
	refreshToggleActions()
}

func toggleStartup() {
	next := !settings.StartWithWindows
	if startupAction != nil {
		next = startupAction.Checked()
	}
	if err := setStartup(next); err != nil {
		if startupAction != nil {
			startupAction.SetChecked(settings.StartWithWindows)
		}
		walk.MsgBox(mw, "开机启动设置失败", err.Error(), walk.MsgBoxIconError)
		return
	}
	settings.StartWithWindows = next
	saveSettings()
	refreshToggleActions()
}

func refreshToggleActions() {
	if topMostAction != nil {
		topMostAction.SetChecked(settings.AlwaysOnTop)
		if settings.AlwaysOnTop {
			topMostAction.SetText("窗口置顶：开")
		} else {
			topMostAction.SetText("窗口置顶：关")
		}
	}
	if startupAction != nil {
		startupAction.SetChecked(settings.StartWithWindows)
		if settings.StartWithWindows {
			startupAction.SetText("开机启动：开")
		} else {
			startupAction.SetText("开机启动：关")
		}
	}
}

func openLogFile() {
	ensureLogFile()
	_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", recordsPath).Start()
}

func openGitHub() {
	_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", appGitHubURL).Start()
}

func showAbout() {
	walk.MsgBox(mw, "关于 TimeClicker", appCreditLine, walk.MsgBoxIconInformation)
}

func quitApp() {
	settings.ShowWidget = mw.Visible()
	saveWindowBounds()
	saveSettings()
	if notifyIcon != nil {
		notifyIcon.SetVisible(false)
	}
	walk.App().Exit(0)
}

func editCustomFormat() {
	var dialog *walk.Dialog
	var edit *walk.LineEdit

	_, err := Dialog{
		AssignTo: &dialog,
		Title:    "自定义复制格式",
		MinSize:  Size{360, 140},
		Layout:   VBox{Margins: Margins{18, 14, 18, 14}, Spacing: 8},
		Children: []Widget{
			Label{Text: "格式示例：yyyy-MM-dd HH:mm:ss"},
			LineEdit{AssignTo: &edit, Text: settings.CopyFormat},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					HSpacer{},
					PushButton{
						Text: "确定",
						OnClicked: func() {
							value := strings.TrimSpace(edit.Text())
							if value == "" {
								walk.MsgBox(dialog, "格式不能为空", "请输入类似 yyyy-MM-dd HH:mm:ss 的格式。", walk.MsgBoxIconWarning)
								return
							}
							settings.CopyFormat = value
							saveSettings()
							refreshLabels()
							dialog.Accept()
						},
					},
					PushButton{Text: "取消", OnClicked: func() { dialog.Cancel() }},
				},
			},
		},
	}.Run(mw)
	if err != nil {
		walk.MsgBox(mw, "打开格式设置失败", err.Error(), walk.MsgBoxIconError)
	}
}

func refreshLabels() {
	if currentLabel != nil {
		currentLabel.SetText(time.Now().Format("2006-01-02 15:04:05"))
	}
	if lastLabel != nil {
		lastLabel.SetText(lastRecord)
	}
	if formatLabel != nil {
		formatLabel.SetText("复制格式: " + settings.CopyFormat)
	}
}

func initPaths() {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		if dir, err := os.UserCacheDir(); err == nil {
			base = dir
		}
	}
	if base == "" {
		base = "."
	}
	appDir = filepath.Join(base, appName)
	settingsPath = filepath.Join(appDir, settingsName)
	recordsPath = filepath.Join(appDir, recordsName)
	_ = os.MkdirAll(appDir, 0755)
}

func defaultSettings() appSettings {
	return appSettings{
		CopyFormat:       "yyyy-MM-dd HH:mm:ss",
		ShowWidget:       true,
		AlwaysOnTop:      true,
		WidgetCorner:     "bottom-right",
		WidgetX:          -1,
		WidgetY:          -1,
		StartWithWindows: false,
	}
}

func loadSettings() {
	settings = defaultSettings()
	data, err := os.ReadFile(settingsPath)
	if err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	if strings.TrimSpace(settings.CopyFormat) == "" {
		settings.CopyFormat = defaultSettings().CopyFormat
	}
	if settings.WidgetCorner == "" {
		settings.WidgetCorner = "bottom-right"
	}
	saveSettings()
}

func saveSettings() {
	_ = os.MkdirAll(appDir, 0755)
	data, _ := json.MarshalIndent(settings, "", "  ")
	_ = os.WriteFile(settingsPath, data, 0644)
}

func appendRecord(rec recordEntry) error {
	_ = os.MkdirAll(appDir, 0755)
	f, err := os.OpenFile(recordsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err = f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func loadLastRecord() string {
	f, err := os.Open(recordsPath)
	if err != nil {
		return "尚未记录"
	}
	defer f.Close()

	var last string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			last = line
		}
	}
	if last == "" {
		return "尚未记录"
	}

	var rec recordEntry
	if err := json.Unmarshal([]byte(last), &rec); err == nil {
		if rec.CopiedText != "" {
			return rec.CopiedText
		}
		if rec.Local != "" {
			return rec.Local
		}
	}
	return last
}

func ensureLogFile() {
	_ = os.MkdirAll(appDir, 0755)
	if _, err := os.Stat(recordsPath); errors.Is(err, os.ErrNotExist) {
		_ = os.WriteFile(recordsPath, nil, 0644)
	}
}

func formatTime(t time.Time, format string) string {
	format = strings.TrimSpace(format)
	if strings.EqualFold(format, "ISO 8601") || strings.EqualFold(format, "ISO8601") {
		return t.Format(time.RFC3339)
	}
	layout := strings.NewReplacer(
		"yyyy", "2006",
		"YYYY", "2006",
		"MM", "01",
		"dd", "02",
		"DD", "02",
		"HH", "15",
		"mm", "04",
		"ss", "05",
	).Replace(format)
	return t.Format(layout)
}

func formatID(value string) string {
	for _, opt := range formats {
		if opt.Value == value {
			return opt.ID
		}
	}
	return "custom"
}

func trayTip() string {
	return fmt.Sprintf("%s\n%s\n上次记录: %s\n格式: %s", appName, appCreditLine, lastRecord, settings.CopyFormat)
}

func restoreWindowPlacement() {
	if mw == nil {
		return
	}
	x, y := settings.WidgetX, settings.WidgetY
	if x < 0 || y < 0 {
		work := workArea()
		x = work.X + work.Width - windowWidth - 18
		y = work.Y + work.Height - windowHeight - 18
	}
	mw.SetBounds(walk.Rectangle{X: x, Y: y, Width: windowWidth, Height: windowHeight})
}

func saveWindowBounds() {
	if mw == nil {
		return
	}
	b := mw.Bounds()
	settings.WidgetX = b.X
	settings.WidgetY = b.Y
}

func workArea() walk.Rectangle {
	var rect win.RECT
	const spiGetWorkArea = 0x0030
	if !win.SystemParametersInfo(spiGetWorkArea, 0, unsafe.Pointer(&rect), 0) {
		return walk.Rectangle{X: 80, Y: 80, Width: 1024, Height: 768}
	}
	return walk.Rectangle{
		X:      int(rect.Left),
		Y:      int(rect.Top),
		Width:  int(rect.Right - rect.Left),
		Height: int(rect.Bottom - rect.Top),
	}
}

func applyTopMost() {
	if mw == nil {
		return
	}
	after := win.HWND_NOTOPMOST
	if settings.AlwaysOnTop {
		after = win.HWND_TOPMOST
	}
	win.SetWindowPos(mw.Handle(), after, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOACTIVATE)
}

func applyToolWindowStyle() {
	if mw == nil {
		return
	}
	exStyle := win.GetWindowLong(mw.Handle(), win.GWL_EXSTYLE)
	exStyle |= win.WS_EX_TOOLWINDOW
	exStyle &^= win.WS_EX_APPWINDOW
	win.SetWindowLong(mw.Handle(), win.GWL_EXSTYLE, exStyle)
	win.SetWindowPos(mw.Handle(), 0, 0, 0, 0, 0, win.SWP_FRAMECHANGED|win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOACTIVATE)
}

func setStartup(enable bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, startupRegPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()

	if !enable {
		err = key.DeleteValue(appName)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return key.SetStringValue(appName, `"`+exe+`"`)
}

func alreadyRunning() bool {
	name, _ := windows.UTF16PtrFromString(mutexName)
	h, err := windows.CreateMutex(nil, true, name)
	mutexHandle = h
	return err == windows.ERROR_ALREADY_EXISTS
}

func makeClockIcon() (*walk.Icon, error) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 20, G: 97, B: 172, A: 255}}, image.Point{}, draw.Src)
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	for x := 7; x <= 24; x++ {
		img.Set(x, 8, white)
		img.Set(x, 23, white)
	}
	for y := 8; y <= 23; y++ {
		img.Set(7, y, white)
		img.Set(24, y, white)
	}
	for y := 10; y <= 21; y++ {
		img.Set(15, y, white)
		img.Set(16, y, white)
	}
	for x := 16; x <= 22; x++ {
		img.Set(x, 16, white)
		img.Set(x, 17, white)
	}
	return walk.NewIconFromImage(img)
}

func fatal(err error) {
	walk.MsgBox(nil, appName, err.Error(), walk.MsgBoxIconError)
	os.Exit(1)
}

module ikkyoku

go 1.26.1

require (
	github.com/ShinteLab/core v0.0.0-00010101000000-000000000000
	github.com/ShinteLab/ikkyoku v0.0.0-00010101000000-000000000000
	github.com/wailsapp/wails/v3 v3.0.0-beta.26
	golang.org/x/sys v0.47.0
)

require (
	github.com/dlclark/regexp2/v2 v2.5.2 // indirect
	github.com/dop251/goja v0.0.0-20260723142020-b4aef50fa347 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-sourcemap/sourcemap v2.1.3+incompatible // indirect
	github.com/google/pprof v0.0.0-20250317173921-a4b03ec1a45e // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	modernc.org/libc v1.74.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.54.0 // indirect
)

require (
	github.com/ShinteLab/engine v0.0.0-20260725200156-de4523939d66 // indirect
	github.com/ShinteLab/kicho v0.0.0-00010101000000-000000000000 // indirect
	github.com/ShinteLab/suteme v0.0.0-00010101000000-000000000000 // indirect
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/coder/websocket v1.8.14 // indirect
	github.com/gen2brain/shm v0.1.0 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/goml/gobrain v0.0.0-20201212123421-2e2d98ca8249 // indirect
	github.com/jezek/xgb v1.1.1 // indirect
	github.com/kbinani/screenshot v0.0.0-20250624051815-089614a94018 // indirect
	github.com/lxn/win v0.0.0-20210218163916-a377121e959e // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/wenteasy/log v0.3.0 // indirect
	golang.design/x/hotkey v0.6.1 // indirect
	golang.org/x/image v0.44.0 // indirect
	golang.org/x/xerrors v0.0.0-20231012003039-104605ab7028 // indirect
)

// ikkyoku はタグ未発行のため相対パスの replace で参照する(親 CLAUDE.md の運用に準拠)。
replace github.com/ShinteLab/ikkyoku => ../../

replace github.com/ShinteLab/suteme => ../../../suteme

replace github.com/ShinteLab/core => ../../../core

replace github.com/ShinteLab/engine => ../../../engine

replace github.com/ShinteLab/kicho => ../../../kicho

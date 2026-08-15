module github.com/ShinteLab/ikkyoku

go 1.26.1

require (
	github.com/ShinteLab/core v0.0.0-00010101000000-000000000000
	github.com/ShinteLab/engine v0.0.0-20260725200156-de4523939d66
	github.com/ShinteLab/suteme v0.0.0-00010101000000-000000000000
	github.com/kbinani/screenshot v0.0.0-20250624051815-089614a94018
	golang.design/x/hotkey v0.6.1
	golang.org/x/text v0.40.0
)

require (
	github.com/gen2brain/shm v0.1.0 // indirect
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	github.com/goml/gobrain v0.0.0-20201212123421-2e2d98ca8249 // indirect
	github.com/jezek/xgb v1.1.1 // indirect
	github.com/lxn/win v0.0.0-20210218163916-a377121e959e // indirect
	golang.org/x/image v0.44.0 // indirect
	golang.org/x/sys v0.24.0 // indirect
	golang.org/x/xerrors v0.0.0-20231012003039-104605ab7028 // indirect
)

replace github.com/ShinteLab/suteme => ../suteme

replace github.com/ShinteLab/core => ../core

replace github.com/ShinteLab/engine => ../engine

// ikkyoku コマンドは画面の指定領域をホットキー（既定 Alt+S）または単発で PNG に保存する。
//
// 盤面認識・SFEN 変換などの将棋ロジックはここには一切無い。撮って保存するだけ。
// フラグ処理とキャプチャ処理は分離してあり（本ファイルは flag 処理と入出力のみ）、
// 実処理は親パッケージ github.com/ShinteLab/ikkyoku を呼ぶだけになっている。
// 将来 Wails3 の GUI から呼ぶときも同じ親パッケージを直接 import すればよい。
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ShinteLab/ikkyoku"
	"golang.design/x/hotkey"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ikkyoku:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("ikkyoku", flag.ContinueOnError)
	var (
		list       = fs.Bool("list", false, "接続されているディスプレイの一覧を表示して終了する")
		once       = fs.Bool("once", false, "起動して即座に1枚撮って終了する（動作確認用）")
		displayIdx = fs.Int("display", -1, "キャプチャするディスプレイ番号（-list で確認）。-region と併用不可")
		regionFlag = fs.String("region", "", "キャプチャする矩形領域 x,y,width,height（-display と併用不可）")
		outDir     = fs.String("out", "", "保存先ディレクトリ（既定: os.UserConfigDir()/ikkyoku/captures）")
		hotkeyStr  = fs.String("hotkey", "", "常駐モードのグローバルホットキー（既定 "+ikkyoku.DefaultHotkey+"）")
		configPath = fs.String("config", "", "設定ファイルのパス（既定: os.UserConfigDir()/ikkyoku/config.json）")
		noStdin    = fs.Bool("no-hotkey-fallback", false, "ホットキー登録に失敗した際、標準入力(Enter)へのフォールバックを禁止する")
		saveConfig = fs.Bool("save-config", false, "解決した領域・保存先・ホットキーを設定ファイルに書き出して終了する")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *list {
		return printDisplays(os.Stdout)
	}

	cfgPath := *configPath
	if cfgPath == "" {
		p, err := ikkyoku.DefaultConfigPath()
		if err != nil {
			return err
		}
		cfgPath = p
	}
	cfg, err := ikkyoku.LoadConfig(cfgPath)
	if err != nil {
		return err
	}

	region, err := resolveRegion(*displayIdx, *regionFlag, cfg)
	if err != nil {
		return err
	}

	dir := *outDir
	if dir == "" {
		dir = cfg.OutDir
	}
	if dir == "" {
		dir, err = ikkyoku.DefaultOutDir()
		if err != nil {
			return err
		}
	}

	hk := resolveHotkey(*hotkeyStr, cfg)

	if *saveConfig {
		// 領域は解決済みの矩形として保存する（ディスプレイ番号ではなく確定した座標）。
		// 毎回フラグを渡さずに済ませるための機能。
		next := ikkyoku.Config{Region: &region, OutDir: dir, Hotkey: hk}
		if err := ikkyoku.SaveConfig(cfgPath, next); err != nil {
			return err
		}
		fmt.Printf("saved-config=%s region=%s out=%s hotkey=%s\n", cfgPath, region, dir, hk)
		return nil
	}

	if *once {
		return captureOnce(region, dir)
	}
	return residentMode(region, dir, hk, !*noStdin)
}

// resolveRegion はフラグ・設定ファイルの優先順でキャプチャ領域を決定する。
// 優先順位: -region > -display > 設定ファイル(Region) > 設定ファイル(Display) > プライマリディスプレイ全体。
func resolveRegion(displayIdx int, regionFlag string, cfg ikkyoku.Config) (ikkyoku.Region, error) {
	if regionFlag != "" {
		if displayIdx >= 0 {
			return ikkyoku.Region{}, fmt.Errorf("-region と -display は同時に指定できません")
		}
		return ikkyoku.ParseRegion(regionFlag)
	}
	if displayIdx >= 0 {
		return ikkyoku.DisplayRegion(displayIdx)
	}
	if cfg.Region != nil {
		return *cfg.Region, nil
	}
	if cfg.Display != nil {
		return ikkyoku.DisplayRegion(*cfg.Display)
	}
	return ikkyoku.PrimaryRegion()
}

// resolveHotkey は -hotkey > 設定ファイル > 既定値 の優先順でホットキーを決定する。
func resolveHotkey(flagVal string, cfg ikkyoku.Config) string {
	if flagVal != "" {
		return flagVal
	}
	if cfg.Hotkey != "" {
		return cfg.Hotkey
	}
	return ikkyoku.DefaultHotkey
}

func printDisplays(w *os.File) error {
	displays := ikkyoku.ListDisplays()
	if len(displays) == 0 {
		return fmt.Errorf("ディスプレイが見つかりませんでした")
	}
	for _, d := range displays {
		r := d.Region()
		fmt.Fprintf(w, "%d: %dx%d at (%d,%d)\n", d.Index, r.Width, r.Height, r.X, r.Y)
	}
	return nil
}

func captureOnce(region ikkyoku.Region, dir string) error {
	img, err := ikkyoku.Capture(region)
	if err != nil {
		return err
	}
	path, err := ikkyoku.SavePNG(img, dir)
	if err != nil {
		return err
	}
	printSaved(path, region, img.Bounds())
	return nil
}

// residentMode はホットキーを押すたびにキャプチャする常駐モード。Ctrl+C で終了する。
// ホットキー登録に失敗した場合（他アプリと競合等）、allowFallback が true なら
// 標準入力で Enter を押すたびにキャプチャする方式にフォールバックする。
func residentMode(region ikkyoku.Region, dir, hotkeyStr string, allowFallback bool) error {
	mods, key, err := ikkyoku.ParseHotkey(hotkeyStr)
	if err != nil {
		return err
	}

	hk := hotkey.New(mods, key)
	if regErr := hk.Register(); regErr != nil {
		if !allowFallback {
			return fmt.Errorf("ホットキー(%s)の登録に失敗しました: %w", hotkeyStr, regErr)
		}
		fmt.Fprintf(os.Stderr, "ikkyoku: ホットキー(%s)の登録に失敗しました（%v）。"+
			"標準入力で Enter を押すたびにキャプチャする方式に切り替えます。\n", hotkeyStr, regErr)
		return stdinFallbackMode(region, dir)
	}
	defer hk.Unregister()

	fmt.Printf("ikkyoku: 常駐モード開始（ホットキー %s、Ctrl+C で終了）\n", hotkeyStr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case <-hk.Keydown():
			if err := captureOnce(region, dir); err != nil {
				fmt.Fprintln(os.Stderr, "ikkyoku:", err)
			}
		case <-sigCh:
			fmt.Println("ikkyoku: 終了します")
			return nil
		}
	}
}

// stdinFallbackMode はグローバルホットキーが使えない環境向けのフォールバック。
// 標準入力から改行を読むたびに 1 枚キャプチャする。Ctrl+C で終了する。
func stdinFallbackMode(region ikkyoku.Region, dir string) error {
	fmt.Println("ikkyoku: Enter キーでキャプチャします（Ctrl+C で終了）")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if err := captureOnce(region, dir); err != nil {
			fmt.Fprintln(os.Stderr, "ikkyoku:", err)
		}
	}
	return scanner.Err()
}

func printSaved(path string, region ikkyoku.Region, bounds interface{ String() string }) {
	fmt.Printf("saved=%s region=%s size=%s\n", path, region, bounds)
}

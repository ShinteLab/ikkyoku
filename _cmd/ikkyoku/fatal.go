package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/ShinteLab/ikkyoku/log"
)

// 続けられなくなったときの最後の口（2026-10-04）。
//
// **配る exe は `-H windowsgui` で標準エラーの行き先が無い。** それまでは WebView2 が
// 無い・Wails の致命的なエラー・main の panic のどれも、**画面に何も出さずに消えていた**
// （ログに 1 行残るだけ）。利用者からは「起動しない」にしか見えないので、
// **OS のメッセージボックスで理由とログの場所を出してから終わる。**
//
// ⚠️ **ここに来るのは画面を出せない（出していられない）ものだけ。** 起動はできるが
// 足りないもの（認識器・棋譜データベース・壊れた設定）は `app.IssueService` へ出し、
// メイン画面のツールバーの ⚠ に並べる。**起動を止めるほうへ寄せないこと**（設計原則3）。
//
// ⚠️ **自分で起こした goroutine の panic はここを通らない**（Go のランタイムがそのまま
// プロセスを落とす）。そちらは `log.CatchCrash` がファイルに残し、次の起動の ⚠ で
// 「前回は異常終了しました」と出す。

// revealed はメイン画面を一度でも出したか。メッセージの書き出しを
// 「起動できませんでした」と「続けられなくなりました」で分けるためだけに使う。
var revealed atomic.Bool

// fatalReasonMax はメッセージボックスに載せる理由の長さの上限（文字数）。
// panic はスタックトレースごと来るので、**全部はログに任せ、箱には頭だけ載せる。**
const fatalReasonMax = 600

// fatalMessage はメッセージボックスの本文を組む。
func fatalMessage(reason string, started bool, logDir string) string {
	var b strings.Builder
	if started {
		b.WriteString("ikkyoku は動作を続けられなくなったため終了します。")
	} else {
		b.WriteString("ikkyoku を起動できませんでした。")
	}
	reason = strings.TrimSpace(reason)
	if r := []rune(reason); len(r) > fatalReasonMax {
		reason = string(r[:fatalReasonMax]) + "…"
	}
	b.WriteString("\n\n" + reason)
	// WebView2 は Windows 11 には最初から入っているが、更新していない Windows 10 には
	// 無いことがある。**画面が 1 枚も出ない原因として一番ありそうなもの**なので名指しする。
	if strings.Contains(strings.ToLower(reason), "webview2") {
		b.WriteString("\n\nMicrosoft Edge WebView2 ランタイムが入っていないか、壊れている可能性があります。" +
			"\nhttps://developer.microsoft.com/microsoft-edge/webview2/ から入れ直してください。")
	}
	if logDir != "" {
		b.WriteString("\n\nログ: " + logDir)
	}
	return b.String()
}

// showFatal はログに残し、メッセージボックスを出して、閉じられるまで待つ。
// **終了はしない**（呼び出し側が決める。Wails の致命的なエラーは Wails が os.Exit する）。
func showFatal(reason string) {
	log.Error("続けられないので終了します", "reason", reason)
	if err := messageBox("ikkyoku", fatalMessage(reason, revealed.Load(), log.Dir())); err != nil {
		log.Error("メッセージボックスを出せませんでした", "error", err)
	}
}

// handleWailsError は `application.Options.ErrorHandler`。
//
// ⚠️ **致命的なもの（`*application.FatalError`）だけを箱にする。** ここには WebView2 の
// 致命的でないエラーも来る（`chromium.SetErrorCallback`）ので、全部を箱にすると
// 動いているのに止まって見える。それ以外は Wails の既定どおりログに出すだけ。
// 致命的なものは、ここから戻ったあと **Wails が `os.Exit(1)` する。**
func handleWailsError(err error) {
	var fe *application.FatalError
	if errors.As(err, &fe) {
		log.Error("Wails の致命的なエラー", "error", err)
		reason := err.Error()
		if cause := fe.Unwrap(); cause != nil {
			reason = cause.Error()
		}
		showFatal(reason)
		return
	}
	log.Error(err.Error())
}

// recoverMain は main の goroutine の panic を受け、箱を出して終わる。
// `defer recoverMain()` を main の頭（ログを用意した直後）に置く。
func recoverMain() {
	r := recover()
	if r == nil {
		return
	}
	log.Error("main で panic しました", "panic", r, "stack", string(debug.Stack()))
	showFatal(fmt.Sprintf("panic: %v", r))
	os.Exit(2)
}

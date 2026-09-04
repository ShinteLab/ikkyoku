package main

import (
	"fmt"

	"github.com/wailsapp/wails/v3/pkg/application"

	ikkyokuapp "github.com/ShinteLab/ikkyoku/app"
)

// pickFile はファイル選択ダイアログを `ikkyoku/app` へ差し込む形にして返す。
//
// ⚠️ **ダイアログは Wails の口なのでここに残す。** 設定のロジック
// （`SettingsService`）は Wails と関係が無いので `ikkyoku/app` にあり、
// **口だけを関数で受け取る**（`app.FilePicker`）。
//
// **パスを手で打たせないためのもの。** 将棋エンジンも棋譜データベースも
// 深いディレクトリに置かれることが多く、打ち間違いが一番起きやすい入口。
// ただし**テキスト欄も残してある**（貼り付けと確認のため）ので、
// ダイアログが開けなくても設定はできる（設計原則3）。
func pickFile(a *application.App) ikkyokuapp.FilePicker {
	return func(title, startDir string, filters ...string) (string, error) {
		if a == nil {
			return "", fmt.Errorf("ダイアログを開けません")
		}
		dlg := a.Dialog.OpenFile()
		dlg.SetTitle(title)
		dlg.CanChooseFiles(true)
		dlg.CanChooseDirectories(false)
		if startDir != "" {
			dlg.SetDirectory(startDir)
		}
		// filters は「表示名, ワイルドカード」の対で来る。
		// ⚠️ **端数は捨てる**（対になっていない指定でダイアログを壊さない）。
		for i := 0; i+1 < len(filters); i += 2 {
			dlg.AddFilter(filters[i], filters[i+1])
		}
		return dlg.PromptForSingleSelection()
	}
}

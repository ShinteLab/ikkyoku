package main

import (
	ikkyokuapp "github.com/ShinteLab/ikkyoku/app"
	"github.com/ShinteLab/ikkyoku/recognize"
)

// 起動はできたが足りないもの・できないことを `app.IssueService` へ載せる（2026-10-04）。
// メイン画面のツールバーの ⚠ が一覧にする。
//
// ⚠️ **ここに足すのは「操作する前から分かっていて、操作しないと気づけないもの」だけ。**
// その場で失敗を返せる操作（エンジンの起動・棋譜の取得）は、その場で出ている。
// ⚠️ **ホットキーの登録失敗は載せない**（2026-08-10 の決定。案内していない操作なので、
// 失敗を伝えても何をすればよいか分からない。mainscreen.ts の末尾）。
//
// ⚠️ **文言に「棚」と書かないこと**（画面の語彙は「棋譜」。frontend/src/AGENTS.md の 13）。

// 問題の鍵。**同じ問題は同じ鍵で上書き・取り消す**（直ったら ⚠ から消えるように）。
const (
	issueConfig     = "config"
	issueLog        = "log"
	issueCrash      = "crash"
	issueRecognizer = "recognizer"
	issueStrip      = "recognizer-strip"
	issueKifuDB     = "kifudb"
	issueStudyStore = "studystore"
	issueMainWindow = "main-window"
)

// recognizerEffect は認識器が無いときに何ができないか。
const recognizerEffect = "撮った画像から盤面を読めません（撮ること・棋譜の読み込み・解析はできます）"

// syncRecognizerIssue は認識器の読み込み結果を ⚠ に合わせる（読み直すたびに呼ぶ）。
//
// ⚠️ **既定の探索（Mode が空）は読み込みが撮るときまで走らない**ので、ここでデータが
// 置いてあるかだけ前もって見る（`recognize.FindDefaultPredictor`）。見ないと、
// **焼き込みの無い exe を別の端末へ持っていったときに、撮るまで気づけない。**
func syncRecognizerIssue(issues *ikkyokuapp.IssueService, st RecognizerStatus) {
	switch {
	case st.Error != "":
		issues.Set(ikkyokuapp.Issue{
			Key: issueRecognizer, Level: ikkyokuapp.IssueError,
			Title: "認識器を読み込めません", Effect: recognizerEffect, Detail: st.Error,
		})
	case st.Mode == "":
		if err := recognize.FindDefaultPredictor(); err != nil {
			issues.Set(ikkyokuapp.Issue{
				Key: issueRecognizer, Level: ikkyokuapp.IssueError,
				Title: "認識器が見つかりません", Effect: recognizerEffect, Detail: err.Error(),
			})
		} else {
			issues.Clear(issueRecognizer)
		}
	default:
		issues.Clear(issueRecognizer)
	}

	if st.StripError != "" {
		issues.Set(ikkyokuapp.Issue{
			Key: issueStrip, Level: ikkyokuapp.IssueWarn,
			Title:  "盤の縁の判定データを読み込めません",
			Effect: "盤の位置が 1 マス滑ったまま認識することがあります",
			Detail: st.StripError,
		})
	} else {
		issues.Clear(issueStrip)
	}
}

// syncKifuDBIssue は棋譜データベースを開けたかを ⚠ に合わせる（開き直すたびに呼ぶ）。
func syncKifuDBIssue(issues *ikkyokuapp.IssueService, st ikkyokuapp.KifuDBStatus) {
	if st.Ready || st.Error == "" {
		issues.Clear(issueKifuDB)
		return
	}
	detail := st.Error
	if st.Path != "" {
		detail = st.Path + "\n" + st.Error
	}
	issues.Set(ikkyokuapp.Issue{
		Key: issueKifuDB, Level: ikkyokuapp.IssueError,
		Title:  "棋譜データベースを開けません",
		Effect: "棋譜タブに保存・一覧できません（棋譜の読み込みと解析はできます）",
		Detail: detail,
	})
}

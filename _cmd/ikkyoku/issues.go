package main

import (
	"strings"

	"github.com/ShinteLab/ikkyoku"
	ikkyokuapp "github.com/ShinteLab/ikkyoku/app"
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

// recognizerModeLabel は段の呼び名（画面に出す）。
func recognizerModeLabel(mode string) string {
	switch mode {
	case ikkyoku.SutemeSourceDir:
		return "学習データ"
	case ikkyoku.SutemeSourceModel:
		return "配布モデル"
	case ikkyoku.SutemeSourceEmbed:
		return "焼き込み"
	}
	return mode
}

// syncRecognizerIssue は認識器の読み込み結果を ⚠ に合わせる（読み直すたびに呼ぶ）。
//
// 認識器は 3 段（学習データ → 配布モデル → 焼き込み。2026-10-04）で、出すのは 3 つ:
//
//   - **どの段も読めない** … 赤。盤面が読めない
//   - **置いてあるのに使えなかった段がある**（Skipped）… 黄。下の段で動いている
//   - **盤の縁の判定データが無い** … 黄。盤の位置が 1 マス滑ることがある
func syncRecognizerIssue(issues *ikkyokuapp.IssueService, st RecognizerStatus) {
	switch {
	case !st.Ready:
		issues.Set(ikkyokuapp.Issue{
			Key: issueRecognizer, Level: ikkyokuapp.IssueError,
			Title: "認識器がありません", Effect: recognizerEffect,
			Detail: strings.Join(append([]string{st.Error}, st.Skipped...), "\n"),
		})
	case len(st.Skipped) > 0:
		issues.Set(ikkyokuapp.Issue{
			Key: issueRecognizer, Level: ikkyokuapp.IssueWarn,
			Title:  "指定した認識器を使えないので、" + recognizerModeLabel(st.Mode) + "で動いています",
			Detail: strings.Join(st.Skipped, "\n"),
		})
	default:
		issues.Clear(issueRecognizer)
	}

	if st.Ready && st.StripError != "" {
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

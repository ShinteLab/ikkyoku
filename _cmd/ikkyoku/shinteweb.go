package main

import (
	"net/http"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	shinteweb "github.com/ShinteLab/core/web"
)

// shinteWebPrefix は core/web の共有フロントエンド資産を配信する URL の接頭辞。
// suteme の学習用サーバ(training/server.go)と同じパスに揃えてある。
const shinteWebPrefix = "/shinte-web/"

// shinteWebMiddleware は `<shogi-board>` などの共有 Web Component を、
// core の embed からそのまま配信する。
//
// **ファイルをフロントにコピーしない。** 将棋の仕様(SFEN の解釈・盤の描画)は core に
// 一本化する方針で、コピーするとそこが二重になる。core/web はまさにこの用途のために
// `web.Assets` を embed で公開している(core/web/assets.go のコメント参照)。
//
// npm 依存にしない理由: `@shinte/web` は private パッケージで、file: 参照にすると
// 相対パスが git worktree で壊れるうえ、node_modules の共有(wails3 skill worktree.md)も
// 絡んでくる。Go 側は既に core に依存しているので、embed を配信するほうが単純で確実。
func shinteWebMiddleware(next http.Handler) http.Handler {
	files := http.StripPrefix(shinteWebPrefix, http.FileServer(http.FS(shinteweb.Assets)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, shinteWebPrefix) {
			files.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// assetOptions は埋め込んだフロントバンドルと core/web の配信をまとめたもの。
func assetOptions() application.AssetOptions {
	return application.AssetOptions{
		Handler:    application.AssetFileServerFS(assets),
		Middleware: shinteWebMiddleware,
	}
}

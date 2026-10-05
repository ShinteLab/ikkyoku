package main

import (
	"fmt"
	"strconv"
	"strings"
)

// appVersion は起動ログに出す版（2026-10-05）。
//
// 配るビルドは `version` ファイルの値そのまま。**`wails3 dev` などの開発ビルドは
// patch を 1 つ上げて `+dev` を付ける**（0.2.3 → 0.2.4+dev）。`version` ファイルは
// `local:deploy` が上げてタグを打つので、中身は**最後に配った版**になっている ——
// そのまま名乗ると、配った exe と開発中のコードのどちらのログか見分けが付かない。
//
// ⚠️ **`version` ファイルは書き換えないこと**（表示だけの話。唯一の正は `_cmd/version.go` が扱う）。
func appVersion() string {
	return displayVersion(strings.TrimSpace(version), devBuild)
}

// displayVersion は appVersion の中身（ビルドタグに依らず試せるように分けてある）。
// 読めない版なら、上げずに `+dev` だけ付ける（起動を止める話ではない）。
func displayVersion(v string, dev bool) string {
	if !dev {
		return v
	}
	parts := strings.Split(v, ".")
	if len(parts) == 3 {
		if patch, err := strconv.Atoi(parts[2]); err == nil && patch >= 0 {
			return fmt.Sprintf("%s.%s.%d+dev", parts[0], parts[1], patch+1)
		}
	}
	return v + "+dev"
}

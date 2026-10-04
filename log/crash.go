package log

import (
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// CrashFile は異常終了（panic・Go のランタイムの致命的なエラー）の書き出し先。Dir に置く。
//
// **配る exe（`-H windowsgui`）では、panic の内容は標準エラーにしか出ず、どこにも残らない。**
// 利用者からは「何も言わずに消えた」にしか見えないので、ファイルにも書かせておき、
// 次の起動で「前回は異常終了しました」と出す（2026-10-04）。
const CrashFile = FilePrefix + "_crash.log"

// CatchCrash は以後の異常終了の内容を Dir の CrashFile にも書かせる。
// 前回の記録が残っていれば `ikkyoku_crash_<日時>.log` に改名して残し、その場所を返す
// （＝前回は異常終了した）。**Init のあとに 1 回だけ呼ぶ。**
//
// ⚠️ **書けなくても起動は止めないこと**（設計原則3）。エラーを返すだけ。
func CatchCrash() (previous string, err error) {
	dir := Dir()
	if dir == "" {
		return "", errors.New("ログを書けないので、異常終了の記録も残せません")
	}
	previous = keepPreviousCrash(dir)
	f, err := os.Create(filepath.Join(dir, CrashFile))
	if err != nil {
		return previous, err
	}
	// SetCrashOutput は記述子を複製して持つので、ここで閉じてよい。
	defer f.Close()
	return previous, debug.SetCrashOutput(f, debug.CrashOptions{})
}

// keepPreviousCrash は中身のある CrashFile を、書かれた時刻の名前に改名して返す。
// 空（＝前回は正常に終わった）・無い・改名できないときは空を返す。
//
// ⚠️ **改名すること（消さない・上書きしない）。** 次の CatchCrash が CrashFile を空で作り直すので、
// そのままにしておくと前回の記録が消える。
func keepPreviousCrash(dir string) string {
	path := filepath.Join(dir, CrashFile)
	st, err := os.Stat(path)
	if err != nil || st.Size() == 0 {
		return ""
	}
	name := strings.TrimSuffix(CrashFile, ".log") + "_" + st.ModTime().Format("20060102-150405") + ".log"
	dst := filepath.Join(dir, name)
	if err := os.Rename(path, dst); err != nil {
		return ""
	}
	return dst
}

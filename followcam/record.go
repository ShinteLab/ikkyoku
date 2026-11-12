package followcam

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/log"
)

// startFollowDir は録画の保存先を作り、**マスタ画像**を残す（2026-09-15）。
//
// ⚠️ **設定「中継の録画とログを残す（デバッグ）」が切ってあれば何も作らない**（2026-10-11。dev でも配る exe でも
// この設定だけで決まる）。置き場所はログと同じ一時ディレクトリの ikkyoku の下（`RecordRoot`）で、
// 作るたびに 1 週間より古い録画を消す（`RemoveOldRecordings`）。
// ⚠️ **マスタを残すのが要点。** 追いかける基準そのものなので、
// **これが無いと「なぜ別の盤だと判断したか」を後から確かめられない**
// （`recognize.Signature` は矩形と色しか持っていない）。
//
// master はマスタにした 1 枚（nil なら今撮った 1 枚）。撮った 1 枚の盤を使ったときは
// そちらを渡す（2026-10-09）—— 盤の映っていない今の 1 枚を残しても基準にならない。
//
// ⚠️ **失敗しても追跡を止めないこと**（設計原則3）。録画はおまけで、
// **残らなくても中継は追える。**
func (s *Camera) startFollowDir(master image.Image) string {
	s.mu.Lock()
	on := s.debug
	s.mu.Unlock()
	if !on {
		return ""
	}
	base := RecordRoot()
	if base == "" {
		log.Follow.Warn("ログを書けていないので、録画も残しません")
		return ""
	}
	RemoveOldRecordings(base, time.Now())
	// ⚠️ **今撮った 1 枚をそのまま使うこと**（`CaptureQuiet` が控えている）。
	// 撮り直すと**マスタと記述子が別のフレームになる** —— 数えるのは同じ盤でも、
	// 手が被った瞬間などに**画像と判断がずれる**。
	img := master
	if img == nil {
		s.mu.Lock()
		img = s.fol.lastQuiet
		s.mu.Unlock()
	}
	dir := filepath.Join(base, time.Now().Format(followRecordLayout))
	if img != nil {
		if _, err := ikkyoku.SavePNGAs(img, dir, "master.png"); err != nil {
			log.Follow.Warn("マスタ画像を残せません", "error", err)
			return ""
		}
	}
	s.mu.Lock()
	s.fol.followDir, s.fol.followMisses, s.fol.lastMissKind = dir, 0, ""
	s.mu.Unlock()
	return dir
}

// SaveFollowFrame は**手を決めた 1 枚**を録画として残す（2026-09-15）。
//
// number は最初の手の手数（棋譜の数え方）、moves はその 1 枚で足した手（USI）。
// ⚠️ **guess（推測で足したか）を名前に入れること** —— **後から見たいのはそれ**で、
// 「手が映り込んで誤認識した」はまず推測の側に出る。
//
// ⚠️ **戻り値でエラーを返さないこと。** 呼ぶのは追跡のループの中なので、
// **残せなかっただけで追跡が止まるのは割に合わない**（設計原則3）。
// 理由はログに出す。
func (s *Camera) SaveFollowFrame(number int, moves []string, guess bool) {
	s.saveFollowFrame(number, moves, guess)
}

// saveFollowFrame は `SaveFollowFrame` の中身（残した場所を返す。残さなければ空）。
func (s *Camera) saveFollowFrame(number int, moves []string, guess bool) string {
	s.mu.Lock()
	dir, img, on := s.fol.followDir, s.fol.lastQuiet, s.debug
	s.mu.Unlock()
	if dir == "" || img == nil || !on {
		return ""
	}
	// ⚠️ **足せたら数え直すこと。** 残したいのは**崩れ始め**なので、
	// 追いついているあいだに使い切っていては意味が無い。
	s.mu.Lock()
	s.fol.followMisses, s.fol.lastMissKind = 0, ""
	s.mu.Unlock()

	name := followFrameName(number, moves, guess)
	path, err := ikkyoku.SavePNGAs(img, dir, name)
	if err != nil {
		log.Follow.Warn("採用した画像を残せません", "name", name, "error", err)
		return ""
	}
	// 直前の「手を足しました（追跡）」と並べて、その手を決めた 1 枚に辿れるようにする（2026-10-08）。
	log.Follow.Info("録画を残しました（追跡）", "name", name)
	return path
}

// followRecordLayout は録画のディレクトリの名前（「追う」を押した時刻）。古いものを消すときもこれで読む。
const followRecordLayout = "20060102-150405"

// RecordRoot は中継の録画を置くディレクトリ（ログのディレクトリの follow。2026-10-11）。ログを書けていなければ空。
// それまでは撮った画像と同じ `%APPDATA%\ikkyoku\captures\follow` で、消す仕組みが無く 2GB 溜まっていた。
func RecordRoot() string {
	if d := log.Dir(); d != "" {
		return filepath.Join(d, "follow")
	}
	return ""
}

// followRecordKeepDays は中継の録画を置いておく日数（2026-10-11）。
// ⚠️ **ログの日数（`log.KeepDays`）とは別**（2026-11-08 にログだけ 90 日へ延ばした。録画は PNG で圧縮が効かず、
// 1 局で 200MB を超える）。
const followRecordKeepDays = 7

// RemoveOldRecordings は root の下の録画のうち、`followRecordKeepDays` より前に始めたものを消す（1 週間）。
// ⚠️ **名前が録画の時刻として読めるディレクトリだけ消す**（ほかのものは置いた人のもの）。消せなくても何もしない。
func RemoveOldRecordings(root string, now time.Time) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	limit := now.AddDate(0, 0, -followRecordKeepDays)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t, err := time.ParseInLocation(followRecordLayout, e.Name(), now.Location())
		if err != nil || !t.Before(limit) {
			continue
		}
		os.RemoveAll(filepath.Join(root, e.Name()))
	}
}

// followMissMax は**繋げなかった周を残す枚数の上限**。
//
// ⚠️ **知りたいのは崩れ始めの数枚**（そこから先は同じ状態が続くだけ）。
const followMissMax = 12

// SaveFollowMiss は**繋げなかった周**の画像を残す（2026-09-15）。
//
// ⚠️ **採用した画像だけでは原因が分からない。** 実機で追従が 4 手目から
// 90 手ぶん止まったとき、**止まった瞬間の画像が 1 枚も残っていなかった**ので、
// 「認識が外したのか」「順番が決まらなかったのか」を切り分けられなかった。
//
// ⚠️ **上限を超えたら黙って捨てること**（設計原則3）。追いつけない状態は
// 何十分も続くので、全部残すとディスクが埋まる。
func (s *Camera) SaveFollowMiss(number int, kind string) {
	s.saveFollowMiss(number, kind)
}

// saveFollowMiss は `SaveFollowMiss` の中身（残した場所を返す。残さなければ空）。
func (s *Camera) saveFollowMiss(number int, kind string) string {
	s.mu.Lock()
	dir, img, n := s.fol.followDir, s.fol.lastQuiet, s.fol.followMisses
	if !s.debug {
		dir = "" // 追っている途中で設定を切った
	}
	// ⚠️ **同じ理由が続いているあいだは残さない**（変わり目だけ）。
	// 「別の盤」も「変わっていない」も何十周も続くので、毎周残すと
	// **同じ絵で上限を使い切り、そのあとの本当の変わり目が残らない。**
	same := kind == s.fol.lastMissKind
	if dir != "" && img != nil && n < followMissMax && !same {
		s.fol.followMisses, s.fol.lastMissKind = n+1, kind
	}
	s.mu.Unlock()
	if dir == "" || img == nil || n >= followMissMax || same {
		return ""
	}
	name := followMissName(number, kind, n)
	path, err := ikkyoku.SavePNGAs(img, dir, name)
	if err != nil {
		log.Follow.Warn("繋げなかった画像を残せません", "name", name, "error", err)
		return ""
	}
	log.Follow.Info("繋げなかった周を残しました", "name", name)
	return path
}

// followMissName は「x042-choices-1.png」のような名前を組み立てる。
//
// ⚠️ **頭に x を付けること** —— 採用した画像（`042-7g7f.png`）と並ぶので、
// **どれが成立した手でどれが失敗かが名前で分かる**必要がある。
// ⚠️ **連番を付けること** —— 同じ手数で何周も失敗するので、
// 付けないと**崩れ始めの 1 枚が後の周に上書きされる**（一番見たい 1 枚が消える）。
func followMissName(number int, kind string, seq int) string {
	if number < 0 {
		number = 0
	}
	k := strings.Map(safeNameRune, kind)
	if k == "" {
		k = "miss"
	}
	return fmt.Sprintf("x%03d-%s-%d.png", number, k, seq+1)
}

// followFrameName は「042-7g7f-guess.png」のような名前を組み立てる。
//
// ⚠️ **手数を 0 詰めにすること** —— そうしないとファイルの並びが手順の順に
// ならない（9 手目の次に 10 手目ではなく 100 手目が並ぶ）。
// ⚠️ **パス区切りを作らないこと**（`SavePNGAs` が弾くが、ここで作らないのが本筋）。
func followFrameName(number int, moves []string, guess bool) string {
	if number < 0 {
		number = 0
	}
	parts := make([]string, 0, len(moves))
	for _, mv := range moves {
		if mv = strings.Map(safeNameRune, mv); mv != "" {
			parts = append(parts, mv)
		}
	}
	name := fmt.Sprintf("%03d", number)
	if len(parts) > 0 {
		name += "-" + strings.Join(parts, "_")
	}
	if guess {
		name += "-guess"
	}
	return name + ".png"
}

// safeNameRune はファイル名に使ってよい字だけを通す（USI は英数字なので普通は素通り）。
//
// ⚠️ **打つ手の `*` は `@` にすること**（2026-10-05 に実機で踏んだ）。`*` は Windows の
// ファイル名に使えないので、`P*4a` の画像が「ファイルの作成に失敗しました」で残らず、
// **誤った打つ手の証拠が 1 枚も無かった。**
func safeNameRune(r rune) rune {
	switch {
	case r == '*':
		return '@'
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '+':
		return r
	default:
		return -1
	}
}

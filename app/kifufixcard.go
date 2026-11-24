package app

// 入力タブのカードと棋譜タブの行の「⚠ N手目」を解析タブで直したか（2026-11-25。`_docs/app-kifu-fix.md` の「⚠ の修正済」）。
//
// ⚠ は kicho が原本の本文から付けるので、**直しても消えない**（原本は書き換えない）。
// そのままだと直したのかどうかが分からないので、**直しの表を原本に当てて ⚠ の手を越えられたら**
// 横に「修正済」を添える。
//
// ⚠️ **判定は Go だけ**（画面で手数を比べない。`fixActionsLocked` と同じ線引き）。
// ⚠️ **原本は今のカード・棚の行の本文**（控えに書いた原本ではない）—— 解析タブへ送るのもこの本文なので、
// 取り直して原本が変わり、直しが当たらなくなったら（`Built.Dropped`）修正済も消える。
// ⚠️ **`GameSummary` に載せない**（あちらは kicho の値をそのまま運ぶ。これは ikkyoku の値なので別の口で聞く）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ShinteLab/ikkyoku/position"
)

// kifuFixedEvent はカード・行の修正済を見直させる知らせ（直しの表が変わった。`FixMove`）。
// ⚠️ **中身は無い** —— カードの鍵と控えの鍵は作りが違うので、画面は ⚠ のカード・行を全部聞き直す。
const kifuFixedEvent = "kifu:fixed"

// FixedNote は入力タブのカードの「⚠ N手目」が解析タブで直してあるかを答える（2026-11-25）。
// 直してあればホバーに出す文を、直していなければ空を返す。
//
// pasted は貼り付けたカードか（控えの鍵の作り方が違う。`pasteKeyOf` / `sourceKeyOf`）。
// ⚠️ **答えられなくてもエラーにしない**（印が出ないだけ。設計原則3）。
func (s *KifuService) FixedNote(d GameDetail, pasted bool) string {
	if d.ErrorMove <= 0 || d.KIF == "" {
		return ""
	}
	key := sourceKeyOf(d.Source, d.SourceID)
	if pasted {
		key = pasteKeyOf(d.KIF)
	}
	if key == "" {
		return ""
	}
	fixes, ok := s.study.fixesOf(func() bool { return s.study.sourceKey == key })
	if !ok && s.Store != nil {
		fixes = s.Store.fixesIn(func() string { return s.Store.keys[key] })
	}
	return fixedNote(d.KIF, d.ErrorMove, fixes)
}

// FixedNotes は棋譜タブの行の「⚠ N手目」が解析タブで直してあるかを答える（2026-11-25）。
// 直してある棋譜の id → ホバーに出す文（直していない棋譜は入らない）。
//
// ⚠️ **本文を読むのは直しの表がある棋譜だけ**（⚠ の行が多くても、棚を 1 件ずつ読みに行かない）。
// 表は棚の棋譜 id で引く（今の検討 → 控え。控えは「棋譜に保存」で id と結ばれる。`LinkGame`）。
// ⚠️ **答えられない棋譜は黙って飛ばす**（印が出ないだけ。設計原則3）。
func (s *KifuService) FixedNotes(ids []string) map[string]string {
	out := map[string]string{}
	for _, id := range ids {
		if id == "" {
			continue
		}
		fixes, ok := s.study.fixesOf(func() bool { return s.study.gameID == id })
		if !ok && s.Store != nil {
			fixes = s.Store.fixesIn(func() string { return s.Store.games[id] })
		}
		if len(fixes) == 0 {
			continue
		}
		d, err := s.Get(id)
		if err != nil {
			continue
		}
		if note := fixedNote(d.KIF, d.ErrorMove, fixes); note != "" {
			out[id] = note
		}
	}
	return out
}

// fixedNote は原本の本文に直しの表を当てて、errorMove の手を越えられたかを見る。
//
// ⚠️ **直さなければそこで止まることも確かめる**（ikkyoku の読みが kicho の ⚠ と食い違って
// 元から越えていたら、直しと関係なく「修正済」になってしまう）。
func fixedNote(text string, errorMove int, fixes []position.KifuFix) string {
	if errorMove <= 0 || len(fixes) == 0 {
		return ""
	}
	o, err := position.ReadOriginal(text)
	if err != nil {
		return ""
	}
	_, _, plain, err := o.Build(nil)
	if err != nil || !stopsBy(plain, errorMove) {
		return ""
	}
	_, _, b, err := o.Build(fixes)
	if err != nil || stopsBy(b, errorMove) {
		return ""
	}
	applied := len(fixes) - len(b.Dropped)
	if applied <= 0 {
		return ""
	}
	return fmt.Sprintf("解析タブで直してあります（直し %d か所）。原本の ⚠ はそのまま残ります", applied)
}

// stopsBy は組み立てた本譜が原本の errorMove 手目までに止まったか。
func stopsBy(b position.Built, errorMove int) bool {
	return b.Stop != nil && b.Stop.Move <= errorMove
}

// fixesOf は今の検討が is に当たる棋譜なら、その直しの表を返す（ok は今の検討がそれか）。
// is はロックを取った中で呼ぶ（`sourceKey` / `gameID` を比べる）。
//
// ⚠️ **今の検討を先に見る** —— 控えは間引いて書く（3 秒）ので、直した直後は古い。
// 今の検討がそれなら表が空でも ok（古い控えへ落ちない）。
func (s *StudyService) fixesOf(is func() bool) ([]position.KifuFix, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil || s.fix.original == nil || !is() {
		return nil, false
	}
	return append([]position.KifuFix(nil), s.fix.fixes...), true
}

// fixesIn は索引で引いた控え（lookup はロックを取った中で索引からファイル名を返す）の直しの表を読む（無ければ nil）。
// ⚠️ **今の検討と入れ替えない**（`RestoreKey` / `RestoreGame` と違って読むだけ）。
func (t *StudyStore) fixesIn(lookup func() string) []position.KifuFix {
	t.mu.Lock()
	t.index()
	name := lookup()
	t.mu.Unlock()
	if name == "" {
		return nil
	}
	body, err := os.ReadFile(filepath.Join(t.dir, name))
	if err != nil {
		return nil
	}
	// ⚠️ **表だけ読めばよい**（木も評価値も要らない）。
	var rec struct {
		Fixes []position.KifuFix `json:"fixes"`
	}
	if err := json.Unmarshal(body, &rec); err != nil {
		return nil
	}
	return rec.Fixes
}

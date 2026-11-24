package app

// 入力タブのカードの「⚠ N手目」を解析タブで直したか（2026-11-25。`_docs/app-kifu-fix.md` の「カードの修正済」）。
//
// カードの ⚠ は kicho が原本の本文から付けるので、**直しても消えない**（原本は書き換えない）。
// そのままだと直したのかどうかが分からないので、**直しの表を原本に当てて ⚠ の手を越えられたら**
// 横に「修正済」を添える。
//
// ⚠️ **判定は Go だけ**（画面で手数を比べない。`fixActionsLocked` と同じ線引き）。
// ⚠️ **原本は今のカードの本文**（控えに書いた原本ではない）—— 解析タブへ送るのもこの本文なので、
// 取り直して原本が変わり、直しが当たらなくなったら（`Built.Dropped`）修正済も消える。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ShinteLab/ikkyoku/position"
)

// kifuFixedEvent はカードの修正済を見直させる知らせ（直しの表が変わった。`FixMove`）。
// ⚠️ **中身は無い** —— カードの鍵と控えの鍵は作りが違うので、画面は ⚠ のカードを全部聞き直す。
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
	fixes, ok := s.study.fixesOfKey(key)
	if !ok && s.Store != nil {
		fixes = s.Store.fixesByKey(key)
	}
	return fixedNote(d.KIF, d.ErrorMove, fixes)
}

// fixedNote は原本の本文に直しの表を当てて、errorMove の手を越えられたかを見る。
//
// ⚠️ **直さなければそこで止まることも確かめる**（ikkyoku の読みが kicho の ⚠ と食い違って
// 元から越えていたら、直しと関係なく「修正済」になってしまう）。
func fixedNote(text string, errorMove int, fixes []position.KifuFix) string {
	if len(fixes) == 0 {
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

// fixesOfKey は今の検討がこの鍵の棋譜なら、その直しの表を返す（ok は今の検討がそれか）。
//
// ⚠️ **今の検討を先に見る** —— 控えは間引いて書く（3 秒）ので、直した直後は古い。
// 今の検討がそれなら表が空でも ok（古い控えへ落ちない）。
func (s *StudyService) fixesOfKey(key string) ([]position.KifuFix, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil || s.sourceKey != key || s.fix.original == nil {
		return nil, false
	}
	return append([]position.KifuFix(nil), s.fix.fixes...), true
}

// fixesByKey は出どころの鍵が同じ一番新しい控えの直しの表を読む（無ければ nil）。
// ⚠️ **今の検討と入れ替えない**（`RestoreKey` と違って読むだけ）。
func (t *StudyStore) fixesByKey(key string) []position.KifuFix {
	t.mu.Lock()
	t.index()
	name := t.keys[key]
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

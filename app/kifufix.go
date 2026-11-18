package app

// 棋譜の誤記を直す・指摘する（2026-11-19。`_docs/app-kifu-fix.md`）。
//
// 直しは**表として持つ**（`kifuFixState.fixes`。木に焼き付けない）。原本を読むたびに表を当ててから
// 据え直す（`position.Original.Build` → `mergeReloadLocked`）ので、取り直しで直しが枝へ押し戻されない。
//
// ⚠️ **「直しにする」は原本を持つ検討だけ**（棋譜・URL・棚から読んだもの）。撮った局面・新規対局には出さない。
// ⚠️ **項目を出す条件はここで決める**（`moveActionsLocked`）。フロントは並べて、押されたら `FixMove` を呼ぶだけ。
// ⚠️ **自分の枝が黙って本譜になる道を作らない** —— 本譜が変わるのは人が右クリックで言ったときだけ。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ShinteLab/ikkyoku/log"
	"github.com/ShinteLab/ikkyoku/position"
	"github.com/ShinteLab/kicho/store"
)

// flagByIkkyoku / flagByUser は指摘を誰が付けたか（kicho の `Flag.By`）。
const (
	flagByIkkyoku = "ikkyoku"
	flagByUser    = "user"
)

// stopRestShown は止まった手の後ろに灰色で出す原本の手数（残りは手数だけ出す。実機で見て決める）。
const stopRestShown = 6

// kifuFixState は今の検討の原本と直しの表（**根を入れ替えたら捨てる**。`newSessionLocked`）。
type kifuFixState struct {
	// original は原本（棋譜・URL・棚から読んだときだけ）。text はその本文（控えに書き、開き直しで読み直す）。
	original *position.Original
	text     string
	// fixes は直しの表。⚠️ **手数は原本の手数**。
	fixes []position.KifuFix
	// built は原本に表を当てた結果（本譜の手の出どころ・止まった手）。
	built position.Built
	// flags は棚の棋譜の指摘（`gameID` があるときだけ。kicho から読んだもの）。errorMove は kicho のエラーの印。
	flags     []store.Flag
	errorMove int
	// dismissed は**人が外した ikkyoku の指摘**（同じ原本のままなら付け直さない）。
	dismissed []DismissedFlag
	// registered は訂正版を登録したときの本譜（`mainDigest`）。同じ本譜で 2 度登録しない。
	registered string
}

// DismissedFlag は人が外した ikkyoku の指摘（控えに書く）。⚠️ **原本のその手の表記が同じあいだは付け直さない**。
type DismissedFlag struct {
	Move int    `json:"move"`
	Text string `json:"text"`
}

// KifuStop は原本を読み進められなかった手（手順リストの ⚠ の行）。⚠️ **節点ではない**（終局の行と同じ）。
type KifuStop struct {
	// After はこの行を出す手（その手の直後。0 なら開始局面の直後）。
	After int `json:"after"`
	// Number は画面に出す手数。Text は原本の表記、Reason は止めた理由（ホバー）。
	Number int    `json:"number"`
	Text   string `json:"text"`
	Reason string `json:"reason"`
	// Rest は後ろの原本の表記（灰色。先頭の数手だけ）、More は出していない残りの手数。
	Rest []string `json:"rest"`
	More int      `json:"more"`
	// Actions は ⚠ の行の右クリックで出す項目（「この手を飛ばす」）。
	Actions []MoveAction `json:"actions"`
}

// MoveMark は手の後ろの印（直した手 ✎・枝に下がった原本の手・指摘 ⚑）。
type MoveMark struct {
	ID int `json:"id"`
	// Fixed は直した手のホバー（空なら直した手ではない）。
	Fixed string `json:"fixed,omitempty"`
	// Struck は枝に下がった原本の手のホバー（空なら違う）。
	Struck string `json:"struck,omitempty"`
	// Flags はこの手への指摘。
	Flags []FlagMark `json:"flags,omitempty"`
}

// FlagMark は指摘 1 つ（kicho の `Flag` のうち画面に要るもの）。
type FlagMark struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	By     string `json:"by"`
	Stale  bool   `json:"stale"`
}

// MoveAction は手の右クリックで出す、直し・指摘の項目。⚠️ **出すかどうかはここで決める。**
type MoveAction struct {
	// ID は右クリックした手（0 は ⚠ の行）。
	ID int `json:"id"`
	// Op は replace / insert / skip / unfix（`StudyService.FixMove`）・flag / unflag（`KifuService`）。
	Op    string `json:"op"`
	Label string `json:"label"`
	// Flag は外す指摘の id（unflag のとき）。
	Flag string `json:"flag,omitempty"`
}

// fixAction は項目と、押されたときに表をどう変えるか。
type fixAction struct {
	MoveAction
	add    *position.KifuFix
	remove []position.KifuFix
}

// FixMove は直しの表を変える（手順リストの右クリック。op は `MoveAction.Op` のうち replace / insert / skip / unfix）。
//
// 表を変えたら原本を読み直して据え直す（`rebuildLocked`）。⚠️ **出していない項目は断る**（画面の古い表示から押されても
// 別の手を直さない）。
func (s *StudyService) FixMove(id int, op string) (st StudyState, err error) {
	defer func() { s.publish(st, err) }()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil || s.fix.original == nil {
		return s.state(), fmt.Errorf("原本の無い検討は直せません")
	}
	var act *fixAction
	for _, a := range s.fixActionsLocked() {
		if a.ID == id && a.Op == op && (a.add != nil || len(a.remove) > 0) {
			a := a
			act = &a
			break
		}
	}
	if act == nil {
		return s.state(), fmt.Errorf("その手はそう直せません")
	}
	if act.add != nil {
		s.fix.fixes = append(s.fix.fixes, *act.add)
	}
	for _, r := range act.remove {
		s.fix.fixes = removeFix(s.fix.fixes, r)
	}
	note, err := s.rebuildLocked()
	if err != nil {
		return s.state(), err
	}
	// ⚠️ **取り消した直しの手は、人が指した枝として残す**（消さない）。原本の手が生えずに本譜のまま残ったら下げる。
	if op == "unfix" {
		s.demoteUnfixedLocked(id)
	}
	log.Info("棋譜の直しを変えました", "op", op, "id", id, "fixes", len(s.fix.fixes), "note", note)
	return s.changed(), nil
}

// removeFix は表から 1 行を外す。
func removeFix(fixes []position.KifuFix, r position.KifuFix) []position.KifuFix {
	out := fixes[:0:0]
	for _, f := range fixes {
		if f == r {
			continue
		}
		out = append(out, f)
	}
	return out
}

// rebuildLocked は原本に今の表を当てて今の木に据え直す（**ロックを取った状態で呼ぶこと**）。
// 原本が変わって外した直しがあれば、その断りを返す。
func (s *StudyService) rebuildLocked() (string, error) {
	next, _, b, err := s.fix.original.Build(s.fix.fixes)
	if err != nil {
		return "", err
	}
	s.mergeReloadLocked(next)
	return s.takeBuiltLocked(b), nil
}

// takeBuiltLocked は組み立てた結果を持ち、**原本が変わって外した直しを表から消して断りを返す**（1 回だけ知らせる）。
func (s *StudyService) takeBuiltLocked(b position.Built) string {
	s.fix.built = b
	var notes []string
	for _, d := range b.Dropped {
		s.fix.fixes = removeFix(s.fix.fixes, d)
		notes = append(notes, fmt.Sprintf("原本の %d 手目「%s」が変わったので、そこの直しを外しました", d.Move, d.Text))
	}
	return strings.Join(notes, "／")
}

// readOriginal は原本を読み、今の表を当てて組み立てる（読み込み・再読み込み・据え直しの 3 つが通る）。
func readOriginal(text string, fixes []position.KifuFix) (*position.Original, *position.Study, position.KIFLoad, position.Built, error) {
	o, err := position.ReadOriginal(text)
	if err != nil {
		return nil, nil, position.KIFLoad{}, position.Built{}, err
	}
	study, k, b, err := o.Build(fixes)
	if err != nil {
		return nil, nil, position.KIFLoad{}, position.Built{}, err
	}
	return o, study, k, b, nil
}

// demoteUnfixedLocked は取り消した直しの手が本譜に残っていたら変化へ下げる。
func (s *StudyService) demoteUnfixedLocked(id int) {
	n, ok := s.nodeLocked(id)
	if !ok || !n.Main {
		return
	}
	plies := s.fix.built.Plies
	if n.Number <= len(plies) && plies[n.Number-1].USI == n.USI {
		return
	}
	_ = s.study.Branch(id)
}

// alignedLocked は本譜の節点 id を、原本から組み立てた手（`Built.Plies`）と揃っているところまで返す。
//
// ⚠️ **揃っているところまでしか印も項目も出さない** —— 人が本線を選び直すなどして本譜が原本の並びと
// 違っていれば、そこから先はどの原本の手か分からない。
func (s *StudyService) alignedLocked(nodes []position.Node) []position.Node {
	var main []position.Node
	for _, n := range nodes {
		if n.Main {
			main = append(main, n)
		}
	}
	plies := s.fix.built.Plies
	var out []position.Node
	for i, n := range main {
		if i >= len(plies) || n.Number != i+1 || plies[i].USI != n.USI {
			break
		}
		out = append(out, n)
	}
	return out
}

// kifuStopLocked は ⚠ の行（止まった所が今の本譜に揃っているときだけ）。
func (s *StudyService) kifuStopLocked(aligned []position.Node, actions []fixAction) *KifuStop {
	stop := s.fix.built.Stop
	if s.fix.original == nil || stop == nil || len(aligned) < stop.Ply {
		return nil
	}
	after := 0
	if stop.Ply > 0 {
		after = aligned[stop.Ply-1].ID
	}
	rest := stop.Rest
	more := 0
	if len(rest) > stopRestShown {
		more = len(rest) - stopRestShown
		rest = rest[:stopRestShown]
	}
	ks := &KifuStop{
		After: after, Number: s.moveBaseLocked() + stop.Ply + 1,
		Text: stop.Text, Reason: stop.Reason,
		Rest: append([]string{}, rest...), More: more, Actions: []MoveAction{},
	}
	for _, a := range actions {
		if a.ID == 0 {
			ks.Actions = append(ks.Actions, a.MoveAction)
		}
	}
	return ks
}

// fixActionsLocked は直し・指摘の項目を全部出す（**判断はここ 1 か所**）。
func (s *StudyService) fixActionsLocked() []fixAction {
	if s.study == nil || s.fix.original == nil {
		return nil
	}
	nodes := s.study.Nodes()
	aligned := s.alignedLocked(nodes)
	plies := s.fix.built.Plies
	base := s.moveBaseLocked()
	orig := s.fix.original.Moves
	var out []fixAction

	// 本譜の手: 直しを取り消す・指摘する・指摘を外す。
	for i, n := range aligned {
		p := plies[i]
		if p.Fix != nil || len(p.Skipped) > 0 {
			var rm []position.KifuFix
			if p.Fix != nil {
				rm = append(rm, *p.Fix)
			}
			rm = append(rm, p.Skipped...)
			out = append(out, fixAction{MoveAction: MoveAction{ID: n.ID, Op: "unfix", Label: "直しを取り消す"}, remove: rm})
		}
		if s.gameID != "" && p.Orig > 0 {
			out = append(out, fixAction{MoveAction: MoveAction{ID: n.ID, Op: "flag", Label: "指摘する…"}})
			for _, f := range s.fix.flags {
				if f.Move == p.Orig {
					out = append(out, fixAction{MoveAction: MoveAction{
						ID: n.ID, Op: "unflag", Label: "指摘を外す（" + f.Reason + "）", Flag: f.ID}})
				}
			}
		}
	}

	parentOf := func(ply int) int { // 本譜の ply 手目の親の id（0 = 根）
		if ply == 0 {
			return 0
		}
		return aligned[ply-1].ID
	}
	stop := s.fix.built.Stop
	for _, n := range nodes {
		if n.Main {
			continue
		}
		ply := n.Number - 1 // この手の親までの手数
		if ply > len(aligned) || n.Parent != parentOf(ply) {
			continue
		}
		switch {
		case stop != nil && ply == stop.Ply:
			// 止まった手の 1 つ前の局面で指した手。⚠️ 本譜の続きは無いので、指した手は枝にも本譜にもなりうる。
			out = append(out, s.stopActions(n, stop, base)...)
		case ply < len(aligned) && plies[ply].Orig > 0 && plies[ply].USI != n.USI:
			// 本譜の手と分かれる枝の手（盤に当たる誤記の直し）。
			p := plies[ply]
			add := position.KifuFix{Move: p.Orig, Text: orig[p.Orig-1].Text, Kind: position.FixReplace,
				USI: n.USI, OrigUSI: p.USI}
			if p.Fix != nil {
				// 直した手をさらに直すときは、前の直しを置き換える。
				add.OrigUSI = p.Fix.OrigUSI
			}
			act := fixAction{MoveAction: MoveAction{ID: n.ID, Op: "replace",
				Label: fmt.Sprintf("本譜の %d 手目の直しにする", base+ply+1)}, add: &add}
			if p.Fix != nil {
				act.remove = []position.KifuFix{*p.Fix}
			}
			out = append(out, act)
		}
	}
	// 本譜が止まった手の親まで揃っていて、そこから本譜で指した手（子が 1 つなら本譜になる）。
	if stop != nil && len(aligned) >= stop.Ply {
		for _, n := range nodes {
			if n.Main && n.Number == stop.Ply+1 && n.Parent == parentOf(stop.Ply) {
				out = append(out, s.stopActions(n, stop, base)...)
			}
		}
		out = append(out, fixAction{MoveAction: MoveAction{ID: 0, Op: "skip", Label: "この手を飛ばす"},
			add: &position.KifuFix{Move: stop.Move, Text: orig[stop.Move-1].Text, Kind: position.FixSkip}})
	}
	return out
}

// stopActions は止まった手の 1 つ前の局面で指した手 n の項目（置き換え・足す）。
func (s *StudyService) stopActions(n position.Node, stop *position.Stop, base int) []fixAction {
	text := s.fix.original.Moves[stop.Move-1].Text
	rest := len(stop.Rest)
	replace := position.KifuFix{Move: stop.Move, Text: text, Kind: position.FixReplace, USI: n.USI}
	insert := position.KifuFix{Move: stop.Move, Text: text, Kind: position.FixInsert, USI: n.USI}
	return []fixAction{
		{MoveAction: MoveAction{ID: n.ID, Op: "replace",
			Label: fmt.Sprintf("%d 手目の直しにする（残り %d 手を読み直す）", base+stop.Ply+1, rest)}, add: &replace},
		{MoveAction: MoveAction{ID: n.ID, Op: "insert",
			Label: fmt.Sprintf("抜けていた手として足す（原本の %s を %d 手目に回す）", stop.Text, base+stop.Ply+2)}, add: &insert},
	}
}

// moveMarksLocked は手の後ろの印（直した手・枝に下がった原本の手・指摘）。
func (s *StudyService) moveMarksLocked(nodes, aligned []position.Node) []MoveMark {
	if s.fix.original == nil {
		return []MoveMark{}
	}
	plies := s.fix.built.Plies
	base := s.moveBaseLocked()
	marks := map[int]*MoveMark{}
	at := func(id int) *MoveMark {
		if m, ok := marks[id]; ok {
			return m
		}
		m := &MoveMark{ID: id}
		marks[id] = m
		return m
	}
	struck := map[[2]string]string{} // {親の id, 原本の手} → ホバー
	for i, n := range aligned {
		p := plies[i]
		switch {
		case p.Fix != nil && p.Fix.Kind == position.FixInsert:
			at(n.ID).Fixed = "原本に無い手（足した手）"
		case p.Fix != nil:
			at(n.ID).Fixed = "原本: " + s.originalText(p.Orig, i)
			if p.Fix.OrigUSI != "" {
				struck[[2]string{fmt.Sprint(n.Parent), p.Fix.OrigUSI}] =
					fmt.Sprintf("原本の手（%d 手目で直しました）", base+i+1)
			}
		}
		for _, k := range p.Skipped {
			m := at(n.ID)
			line := fmt.Sprintf("この手の前の原本の「%s」を飛ばしました", k.Text)
			if m.Fixed != "" {
				line = m.Fixed + "\n" + line
			}
			m.Fixed = line
		}
		for _, f := range s.fix.flags {
			if p.Orig > 0 && f.Move == p.Orig {
				at(n.ID).Flags = append(at(n.ID).Flags, FlagMark{ID: f.ID, Reason: f.Reason, By: f.By, Stale: f.Stale})
			}
		}
	}
	for _, n := range nodes {
		if n.Main {
			continue
		}
		if h, ok := struck[[2]string{fmt.Sprint(n.Parent), n.USI}]; ok {
			at(n.ID).Struck = h
		}
	}
	out := []MoveMark{}
	for _, n := range nodes {
		if m, ok := marks[n.ID]; ok {
			out = append(out, *m)
		}
	}
	return out
}

// originalText は原本の orig 手目の表記（手番記号つき。i は本譜の何手目か = 指した側）。
func (s *StudyService) originalText(orig, i int) string {
	t := s.fix.original.Moves[orig-1].Text
	if strings.HasPrefix(t, "▲") || strings.HasPrefix(t, "△") || strings.HasPrefix(t, "☗") || strings.HasPrefix(t, "☖") {
		return t
	}
	black := true
	if r := s.study.Root(); r.Turn == position.TurnWhite {
		black = false
	}
	if i%2 == 1 {
		black = !black
	}
	if black {
		return "▲" + t
	}
	return "△" + t
}

// hasFixesLocked は今の本譜に直した手があるか（訂正版として登録する口を開くか）。
func (s *StudyService) hasFixesLocked() bool {
	if s.study == nil || s.fix.original == nil {
		return false
	}
	aligned := s.alignedLocked(s.study.Nodes())
	for i := range aligned {
		p := s.fix.built.Plies[i]
		if p.Fix != nil || len(p.Skipped) > 0 {
			return true
		}
	}
	return false
}

// mainDigest は本譜の手の並びの要約（訂正版を同じ本譜で 2 度登録しないため）。
func mainDigest(moves []string) string {
	sum := sha256.Sum256([]byte(strings.Join(moves, " ")))
	return hex.EncodeToString(sum[:8])
}

// bodyDigestOf は原本の本文の要約（kicho の `BodyDigest` と同じ作り。sha256 の 16 進）。
func bodyDigestOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// correctionFlag は訂正版を登録するときに親へ付ける指摘（盤に当たる誤記を置き換えた手）。
type correctionFlag struct {
	Move   int
	Reason string
}

// correctionFlagsLocked は**盤に当たる誤記を置き換えた手**の指摘（盤に当たらない誤記は kicho のエラーの印が既にある）。
func (s *StudyService) correctionFlagsLocked() []correctionFlag {
	aligned := s.alignedLocked(s.study.Nodes())
	var out []correctionFlag
	for i, n := range aligned {
		p := s.fix.built.Plies[i]
		if p.Fix == nil || p.Fix.Kind != position.FixReplace || p.Fix.OrigUSI == "" {
			continue
		}
		out = append(out, correctionFlag{Move: p.Orig, Reason: "実際は " + n.Text})
	}
	return out
}

// refreshFlags は棚の棋譜の指摘を読み直す（**ロックの外で呼ぶこと**。棚を読むため）。
func (s *StudyService) refreshFlags() {
	s.mu.Lock()
	gid, of := s.gameID, s.flagsOf
	s.mu.Unlock()
	var flags []store.Flag
	errMove := 0
	if gid != "" && of != nil {
		flags, errMove = of(gid)
	}
	s.mu.Lock()
	if s.gameID == gid {
		s.fix.flags, s.fix.errorMove = flags, errMove
	}
	s.mu.Unlock()
}

// afterKifuRead は棋譜を読んだあとの片付け（**ロックの外で呼ぶこと**）。指摘を読み直し、反則手で止まったら指摘を付けて、
// 描き直した状態を返す（⚠️ **ここでは publish しない** —— 呼ぶ側の publish がこの状態を出す）。
func (s *StudyService) afterKifuRead() StudyState {
	s.refreshFlags()
	if s.autoFlagIllegal() {
		s.refreshFlags()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed()
}

// autoFlagIllegal は**棚の棋譜を読んで反則手で止まったら、その手に指摘を付ける**（`By` は "ikkyoku"）。付けたら true。
//
// ⚠️ **同じ指摘を重ねない**（同じ手に古くない ikkyoku の指摘があれば付けない）・⚠️ **人が外した指摘を付け直さない**
// （原本のその手の表記が同じあいだ）・⚠️ **kicho のエラーの印がある手には付けない**・
// ⚠️ **付けられなくても読み込みは止めない**（設計原則3）。
func (s *StudyService) autoFlagIllegal() bool {
	s.mu.Lock()
	gid, add, stop := s.gameID, s.addFlag, s.fix.built.Stop
	flags, dismissed, errMove := s.fix.flags, s.fix.dismissed, s.fix.errorMove
	s.mu.Unlock()
	if gid == "" || add == nil || stop == nil || !stop.Illegal || stop.Move == errMove {
		return false
	}
	for _, f := range flags {
		if f.Move == stop.Move && f.By == flagByIkkyoku && !f.Stale {
			return false
		}
	}
	for _, d := range dismissed {
		if d.Move == stop.Move && d.Text == stop.Text {
			return false
		}
	}
	if err := add(gid, stop.Move, "反則手: "+stop.Reason, flagByIkkyoku); err != nil {
		log.Warn("反則手の指摘を付けられませんでした", "gameId", gid, "move", stop.Move, "error", err)
		return false
	}
	log.Info("反則手に指摘を付けました", "gameId", gid, "move", stop.Move, "text", stop.Text)
	return true
}

// flagTarget は手 id に指摘を付けるときの棚の棋譜と原本の手数（付けられなければ理由）。
func (s *StudyService) flagTarget(id int) (gameID string, move int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.fixActionsLocked() {
		if a.ID == id && a.Op == "flag" {
			aligned := s.alignedLocked(s.study.Nodes())
			for i, n := range aligned {
				if n.ID == id {
					return s.gameID, s.fix.built.Plies[i].Orig, nil
				}
			}
		}
	}
	return "", 0, fmt.Errorf("その手には指摘を付けられません")
}

// dismissFlag は**人が ikkyoku の指摘を外した**ことを覚える（同じ原本のままなら付け直さない）。
func (s *StudyService) dismissFlag(f store.Flag) {
	if f.By != flagByIkkyoku || f.Move < 1 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fix.original == nil || f.Move > len(s.fix.original.Moves) {
		return
	}
	text := s.fix.original.Moves[f.Move-1].Text
	if st := s.fix.built.Stop; st != nil && st.Move == f.Move {
		text = st.Text
	}
	s.fix.dismissed = append(s.fix.dismissed, DismissedFlag{Move: f.Move, Text: text})
	s.markDirty()
}

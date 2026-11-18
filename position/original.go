package position

// 原本（取得元・棚にある棋譜そのもの）と、それを人の「直し」で読み替える表（2026-11-19）。
//
// ふわりと将棋の棋譜速報は手で書いた KI2 なので、記録の誤記が混じる（`_docs/spec-kifu-fix.md`）。
// 直しは**木に焼き付けず、表として持つ** —— 取り直し（`Graft`）は原本の手を本譜へ据え直すので、
// 木の上で本譜を入れ替えただけだと、盤に当たる誤記の直しが次の取り直しで枝へ押し戻される。
// だから原本を読むたびに**表を当ててから**手を並べる（`Original.Build`）。
//
// ⚠️ **読み取りも表記も core**（`kifu.ParseKI2` / `kifu.KI2Decoder` / `kifu.Decoder` / `kifu.Notation`）。
// ここがやるのは「原本の手と直しを 1 手ずつ Study に指させる」というアプリ側の組み立てだけ。
//
// ⚠️ **原本の手は止まった手の先まで持つ**（`Original.Moves`）。直したあとに続きを読み直すのに要る。

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ShinteLab/core/kifu"
)

// Original は原本の指し手の並び（ヘッダと開始局面を含む）。
type Original struct {
	// Game は対局の素性（ヘッダ）。⚠️ **`Moves` は空**（手は `Moves` に持つ）。
	Game Game
	// Start は開始局面（完全形の SFEN）。
	Start string
	// Moves は原本の指し手（**終局の印は含まない**）。⚠️ **盤に当てられない手より先も持つ。**
	Moves []OriginalMove
	// End は原本に書いてあった終局（"投了" など。無ければ空）。
	End string
	// ki2 は KI2（移動元の無い表記）か。読み直しのやり方が変わる。
	ki2 bool
	// timed は原本に消費時間の欄があったか（`kifu.Document.ShowTime`）。
	timed bool
	// note は読み取りの段階で止まった理由（CSA の指し手が途中で読めないなど。無ければ空）。
	note string
}

// OriginalMove は原本の 1 手。
type OriginalMove struct {
	// Text は原本の表記そのもの（KI2 は手番記号つきで書かれたとおり、KIF / CSA は
	// "７七角(88)" のように表記と移動元）。⚠️ **直しの表はこれで原本が変わったかを見る。**
	Text string
	// Comment はこの手のコメント。Spend は消費時間（`timed` のときだけ意味がある）。
	Comment string
	Spend   time.Duration
	// kif は KIF / CSA の手（移動元つき）。KI2 では使わない。
	kif kifu.Move
}

// FixKind は直しの種類。
type FixKind string

const (
	// FixReplace はその手を別の手に置き換える。
	FixReplace FixKind = "replace"
	// FixInsert はその手の前に、抜けていた手を入れる。
	FixInsert FixKind = "insert"
	// FixSkip はその手を飛ばす（二重に書いた手など）。
	FixSkip FixKind = "skip"
)

// KifuFix は直しの表の 1 行。
//
// ⚠️ **手数は原本の手数で持つ**（直したあとの手数で持つと、足す・飛ばすの直しを取り消したときに
// 後ろの直しが別の手を指す）。
type KifuFix struct {
	// Move は原本の何手目か（1 始まり）。足すときは「この手の前に入れる」。
	Move int `json:"move"`
	// Text はそのときの原本の表記。**当てる前に今の原本と比べ、違えば外す**（サイトが直した）。
	Text string `json:"text"`
	Kind FixKind `json:"kind"`
	// USI は直した手（置き換え・足すのとき）。
	USI string `json:"usi,omitempty"`
	// OrigUSI は置き換える前の原本の手（**盤に当たる手を置き換えたときだけ**。当たらない手には無い）。
	// 枝に下がった原本の手を見つける（取り消し線）のと、訂正版を登録するときの指摘に使う。
	OrigUSI string `json:"origUsi,omitempty"`
}

// Built は原本に直しを当てて並べた結果の内訳。
type Built struct {
	// Plies は本譜の 1 手ずつ（`Study.MainLine` と同じ並び）。
	Plies []BuiltPly
	// Stop は原本を最後まで読めなかったときの、止まった手（読めたなら nil）。
	Stop *Stop
	// Dropped は**原本が変わったので外した直し**（1 回だけ知らせて、表から消す）。
	Dropped []KifuFix
}

// BuiltPly は本譜の 1 手がどこから来たか。
type BuiltPly struct {
	USI string
	// Orig は原本の何手目か（足した手は 0）。
	Orig int
	// Fix はこの手を作った直し（置き換え・足す）。原本どおりなら nil。
	Fix *KifuFix
	// Skipped はこの手の直前で飛ばした原本の手の直し。
	Skipped []KifuFix
}

// Stop は原本を読み進められなかった手。
type Stop struct {
	// Move は原本の何手目か（1 始まり）。Ply はそこまでに本譜へ載った手数（＝止まった手の親の手数）。
	Move int
	Ply  int
	// Text は原本の表記（手番記号つき）。Reason は止めた理由（止めた側の文）。
	Text   string
	Reason string
	// Illegal は**盤には当たったが合法手でなかった**か（二歩・王手放置など。反則手の指摘にする）。
	// false なら盤に当てられなかった（kicho のエラーの印と同じ種類）。
	Illegal bool
	// Rest は止まった手より後ろの原本の表記（手番記号つき）。
	Rest []string
}

// ReadOriginal は棋譜のテキストを、形式（KIF / CSA / KI2）を見分けて原本として読む。
//
// 見分けは `kifu.IsCSA` / `kifu.IsKI2`（**仕様は core**）。error を返すのは何も読めなかったとき・
// 開始局面が組めないとき（知らない手合割など）だけ。**指し手が読めないのはエラーにしない**（`Build` が止まる）。
func ReadOriginal(text string) (*Original, error) {
	switch {
	case kifu.IsCSA(text):
		return readCSA(text)
	case kifu.IsKI2(text):
		return readKI2(text)
	}
	return readKIF(text)
}

func readKIF(text string) (*Original, error) {
	doc, err := kifu.Parse(text)
	if err != nil {
		return nil, err
	}
	if err := readable(doc.Empty(), doc.Start); err != nil {
		return nil, err
	}
	return fromDocument(doc, "")
}

func readCSA(text string) (*Original, error) {
	doc, err := kifu.ParseCSA(text)
	note := ""
	if err != nil {
		if !errors.Is(err, kifu.ErrCSAMoves) {
			return nil, err
		}
		note = err.Error()
	}
	if err := readable(doc.Empty(), doc.Start); err != nil {
		return nil, err
	}
	return fromDocument(doc, note)
}

func readKI2(text string) (*Original, error) {
	k, err := kifu.ParseKI2(text)
	if err != nil {
		return nil, err
	}
	// ⚠️ **盤に当てる前の原本で見ること。** 当てた後だと 1 手目の誤記で手が 0 になり、
	// ヘッダの無い KI2 が「何も読めない」で断られる。
	if err := readable(k.Empty(), k.Start); err != nil {
		return nil, err
	}
	start, err := k.Document.StartSFEN()
	if err != nil {
		return nil, err
	}
	o := &Original{Game: Game(k.Document), Start: start, End: k.End, ki2: true}
	o.Game.Moves = nil
	for _, m := range k.Moves {
		o.Moves = append(o.Moves, OriginalMove{Text: m.Text, Comment: m.Comment})
	}
	return o, nil
}

// fromDocument は KIF / CSA の `Document` を原本にする。⚠️ **CSA も KIF と同じ形に寄せてから通す**
// （形式ごとに組み立てを分けない）。
func fromDocument(doc kifu.Document, note string) (*Original, error) {
	start, err := doc.StartSFEN()
	if err != nil {
		return nil, err
	}
	o := &Original{Game: Game(doc), Start: start, timed: doc.ShowTime, note: note}
	o.Game.Moves = nil
	for _, m := range doc.Moves {
		if mark, ok := kifu.TerminalMarker(m.Name); ok {
			o.End = mark
			break
		}
		o.Moves = append(o.Moves, OriginalMove{
			Text: kifText(m), Comment: m.Comment, Spend: m.Spend, kif: m,
		})
	}
	return o, nil
}

// kifText は KIF の手の原本の表記（"７七角(88)"。打ちは移動元が無い）。
func kifText(m kifu.Move) string {
	name := strings.TrimSpace(m.Name)
	if m.FromX > 0 && m.FromY > 0 {
		return fmt.Sprintf("%s(%d%d)", name, m.FromX, m.FromY)
	}
	return name
}

// readable は棋譜として何か読めたかを確かめる（empty は形式ごとの `Empty()`、start は盤面図）。
func readable(empty bool, start string) error {
	// ⚠️ **kifu.Parse は指し手 0 手をエラーにしない**（中継は対局前から
	// ヘッダだけの棋譜を配信しているため）。断るのは**ヘッダも手も盤面図も
	// 1 つも読めなかった**とき（HTML やただの文章 = 貼り間違い）だけ。
	//
	// ⚠️ **指し手 0 手でも通すこと**（2026-10-02）。開始局面は**盤面図があれば
	// それ、無ければ手合割の初期局面**（手合割の指定が無ければ平手）で決まって
	// いるので、手が無くても**解析できる局面はある**。以前は「手も盤面図も無ければ
	// 断る」にしていて、URL から取った対局前の棋譜が解析タブへ送れなかった。
	// 盤面図だけの棋譜（撮った 1 局面を棚に入れて開き直す口。Step 3）も同じ理由で通す。
	if empty && start == "" {
		return fmt.Errorf("棋譜として読み取れるものがありませんでした")
	}
	return nil
}

// reader は原本の手を、盤を進めながら USI にしていくもの（KI2 と KIF で読み方が違う）。
type reader interface {
	// read は原本の手を読む（中で盤を 1 手進める）。
	read(m OriginalMove) (string, error)
	// sync は原本以外の手（直した手）を指したことを伝える。mt はその手の表記、sfen は指したあとの局面。
	sync(mt kifu.MoveText, sfen string)
}

// ki2Reader は KI2 を読む。移動元が無いので、**直した局面から残りを読み直せる**。
type ki2Reader struct{ d *kifu.KI2Decoder }

func (r *ki2Reader) read(m OriginalMove) (string, error) {
	if r.d == nil {
		return "", fmt.Errorf("この局面からは読めません")
	}
	t, err := r.d.Next(m.Text)
	if err != nil {
		return "", err
	}
	return t.USI, nil
}

// sync は直した手を KI2 の表記で読ませて変換器を進める（「同」が直した手の移動先を指すように）。
// ⚠️ **読ませた結果が違う手になったら、指したあとの局面から作り直す**（「同」は読めなくなるが、ずれるよりよい）。
func (r *ki2Reader) sync(mt kifu.MoveText, sfen string) {
	if r.d != nil && mt.OK {
		if t, err := r.d.Next(mt.Text); err == nil && t.USI == mt.USI {
			return
		}
	}
	r.d, _ = kifu.NewKI2Decoder(sfen)
}

// kifReader は KIF / CSA を読む。手は移動元つきなので、直したあともそのまま当て直す。
type kifReader struct{ d *kifu.Decoder }

func (r *kifReader) read(m OriginalMove) (string, error) {
	mv, ok, err := r.d.Next(m.kif)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("指し手ではありません")
	}
	return mv, nil
}

// sync は直した手を「同」のために読ませる（移動先を覚えさせるだけ）。
func (r *kifReader) sync(mt kifu.MoveText, _ string) {
	if mt.OK {
		_, _, _ = r.d.Next(kifu.Move{Name: mt.Name, FromX: mt.FromX, FromY: mt.FromY})
	}
}

// Build は原本に直しの表を当てて検討を組み立てる（**読み込み・再読み込み・自動更新・直しの 4 つとも
// ここを通る**。分けると片方だけ直しが消える）。
//
// 手は**合法手として 1 手ずつ指す**ので、盤に当てられない手・反則手でそこで止まる。
// ⚠️ **止まってもエラーにしない**（設計原則3）—— そこまでの手を載せ、止まった手を `Built.Stop` に、
// 理由を `KIFLoad.Note` に出す。error は開始局面が組めないときだけ。
//
// 直しは**当てる前に原本の表記を比べ**、違えば外して `Built.Dropped` に返す（サイトが直した）。
// ⚠️ **黙って外さない** —— 知らせるのは呼び出し側。
func (o *Original) Build(fixes []KifuFix) (*Study, KIFLoad, Built, error) {
	root, err := FromFullSFEN(o.Start)
	if err != nil {
		return nil, KIFLoad{}, Built{}, err
	}
	nt, err := kifu.NewNotation(o.Start)
	if err != nil {
		return nil, KIFLoad{}, Built{}, err
	}
	var rd reader
	if o.ki2 {
		d, err := kifu.NewKI2Decoder(o.Start)
		if err != nil {
			return nil, KIFLoad{}, Built{}, err
		}
		rd = &ki2Reader{d: d}
	} else {
		rd = &kifReader{d: kifu.NewDecoder()}
	}

	var b Built
	inserts := map[int][]KifuFix{}
	solo := map[int]KifuFix{}
	for _, f := range fixes {
		if f.Move < 1 || f.Move > len(o.Moves) || o.Moves[f.Move-1].Text != f.Text {
			b.Dropped = append(b.Dropped, f)
			continue
		}
		if f.Kind == FixInsert {
			inserts[f.Move] = append(inserts[f.Move], f)
			continue
		}
		if _, dup := solo[f.Move]; dup {
			b.Dropped = append(b.Dropped, f)
			continue
		}
		solo[f.Move] = f
	}

	study := NewStudy(root)
	study.top.note.Comment = o.Game.Comment
	load := KIFLoad{Game: o.Game, RootSFEN: o.Start, Total: len(o.Moves), Note: o.note}
	var skipped []KifuFix

	// apply は 1 手を本譜に指す（原本の手でも直した手でも）。
	apply := func(usi string, orig int, fix *KifuFix, note MoveNote) error {
		if err := study.play(usi, mark{}); err != nil {
			return err
		}
		study.cur.note = note
		mt, _ := nt.Next(usi)
		if fix != nil {
			sfen, _ := study.curPos.SFEN()
			rd.sync(mt, sfen)
		}
		b.Plies = append(b.Plies, BuiltPly{USI: usi, Orig: orig, Fix: fix, Skipped: skipped})
		skipped = nil
		load.Loaded++
		return nil
	}
	stop := func(i int, text string, err error, illegal bool) {
		if !o.ki2 {
			text = sideMark(nt.Black()) + text
		}
		b.Stop = &Stop{
			Move: i + 1, Ply: len(b.Plies), Text: text, Reason: err.Error(), Illegal: illegal,
			Rest: o.restTexts(i+1, !nt.Black()),
		}
		load.Note = fmt.Sprintf("%d手目「%s」で止まりました: %v", i+1, text, err)
	}

	for i := 0; i < len(o.Moves) && b.Stop == nil; i++ {
		om := o.Moves[i]
		note := MoveNote{Spend: om.Spend, Timed: o.timed, Comment: om.Comment}
		for _, f := range inserts[i+1] {
			f := f
			if err := apply(f.USI, 0, &f, MoveNote{}); err != nil {
				stop(i, om.Text, fmt.Errorf("足した手が指せません: %w", err), false)
				break
			}
		}
		if b.Stop != nil {
			break
		}
		if f, ok := solo[i+1]; ok {
			f := f
			if f.Kind == FixSkip {
				skipped = append(skipped, f)
				continue
			}
			if err := apply(f.USI, i+1, &f, note); err != nil {
				stop(i, om.Text, fmt.Errorf("直した手が指せません: %w", err), false)
			}
			continue
		}
		usi, err := rd.read(om)
		if err != nil {
			stop(i, om.Text, err, false)
			break
		}
		if err := apply(usi, i+1, nil, note); err != nil {
			stop(i, om.Text, err, true)
		}
	}
	if b.Stop == nil && len(skipped) > 0 && len(b.Plies) > 0 {
		// 最後の手を飛ばした直しは、その前の手に付ける（取り消す口が要る）。
		last := &b.Plies[len(b.Plies)-1]
		last.Skipped = append(last.Skipped, skipped...)
	}
	// **最後まで読めて、原本に終局が書いてあれば、そこが対局の終わり**（2026-09-16）。
	// ⚠️ **途中で止まったなら立てない**（読めなかっただけで、対局の終わりではない）。
	if b.Stop == nil && o.End != "" {
		study.SetRecordEnd(study.CurrentID(), o.End)
	}
	// 終局が書いていなくても、千日手ならそこで終わっている（2026-10-09）。⚠️ **最後に 1 回**。
	study.settleRepetition()
	return study, load, b, nil
}

// restTexts は from 手目（0 始まり）から後ろの原本の表記（手番記号つき）。black は from 手目を指す側。
func (o *Original) restTexts(from int, black bool) []string {
	var out []string
	for i := from; i < len(o.Moves); i++ {
		t := o.Moves[i].Text
		if !o.ki2 {
			t = sideMark(black) + t
		}
		out = append(out, t)
		black = !black
	}
	return out
}

func sideMark(black bool) string {
	if black {
		return "▲"
	}
	return "△"
}

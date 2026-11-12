package app

// 中継セッション（2026-10-11。追従の作り直し。理由は `_docs/design-follow.md`）。
//
// **中継を追う 1 回（「中継を追う」を押してから止めるまで）を、Go の 1 か所が持つ。** 以前は 1 周の段取り
// （速い経路 → 違いの出るマスを読む → 81 マス → 見た目を比べる → もう一度）・回し続けるループ・見失いの数・
// 控えの有無・帯の候補を画面（`mainscreen.ts`）が持っていて、判断が Go と画面に分かれていた（観点 4）。
// 画面は「追う / 止める / 打った駒を選ぶ」を呼び、`follow:update` で来た様子を描くだけにした。
//
// **追従の状態もここが持つ**（第 2 段。2026-10-11）。解析タブ側の答えの控え（控え・差し替えの控え・聞いている候補・比べる相手の
// 局面・最後に読んだ盤）は `followSession` の下半分、撮る側（マスタ・比べる相手の 1 枚・ふるい・録画）は `followcam.Camera` の
// `captureFollow`。どちらも**追い始めで作り直し、止めたときに捨てる**（`StartFollow` / `followStop` の 1 か所から）。
//
// ⚠️ **撮る側は `FollowCamera` として差し込む**（`StudyService.Camera`。実体は `ikkyoku/followcam` の `Camera`。2026-11-13 に `_cmd/ikkyoku` の `CaptureService` から移した）。
// app は Wails を知らないので、段取りは Go のテストで確かめられる（`followsession_test.go`）。

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ShinteLab/ikkyoku/followcam"
	"github.com/ShinteLab/ikkyoku/log"
	"github.com/ShinteLab/ikkyoku/position"
)

// FollowCamera は中継セッションが使う**撮る側**（実体は `ikkyoku/followcam` の `Camera`。返す 1 枚は `followcam.Shot`）。
// 中身の約束はそれぞれの `followcam.Camera` のメソッド（`_docs/cmd-capture.md`）。
type FollowCamera interface {
	// AnchorBoard は追う盤（マスタ）を決めて、そのとき読んだ 1 枚を返す（最初の 1 周に流す）。
	AnchorBoard() (followcam.Shot, error)
	// CaptureQuiet は撮って、ふるい・速い経路・81 マスの読みのどこまで進んだかを返す。
	CaptureQuiet() (followcam.Shot, error)
	// RecognizeQuiet は直前に撮った 1 枚を 81 マスで読む（速い経路で決まらなかったとき）。
	RecognizeQuiet() (followcam.Shot, error)
	ReadCells(cells []int) ([]followcam.CellPiece, error)
	ComparePieces(pairs []followcam.CellPair) []float64
	CellsUnmoved()
	CellsUnclear()
	ForgetUnmoved()
	SetCellBase()
	SaveFollowFrame(number int, moves []string, guess bool)
	SaveFollowMiss(number int, kind string)
	// BeginFollow は撮る側の持ち物を作り直す（追い始め。追っているあいだは枠を隠させない）。
	BeginFollow()
	// EndFollow は撮る側の持ち物を丸ごと捨てる（追う盤・録画・比べる相手の 1 枚・ふるい。どの止まり方でも呼ぶ）。
	// 外部エンジンの優先度を下げていれば戻す。
	EndFollow()
}

// FollowUpdate は中継セッションの様子（イベント `follow:update`。画面はこれを描くだけ）。
//
// ⚠️ **イベントのペイロードの型は bindings に出ない**ので、変えたら `followcontrol.ts` の受け手も直すこと。
type FollowUpdate struct {
	// On は追っているか。
	On bool `json:"on"`
	// Waiting は次に来るはずの手数（棋譜の数え方。0 は局面が無い）。枠の札に「n手目待ち」と出す。
	Waiting int `json:"waiting"`
	// Status / StatusKind は枠の下の帯の状態の行（1 周ごとに変わる 1 行）と、その色（"" / "warn" / "error"）。
	Status     string `json:"status"`
	StatusKind string `json:"statusKind"`
	// Notice は帯の知らせ（たまに出すもの。止めた理由・差し替えた手・打った駒の候補）。⚠️ **nil は「知らせは変えない」**。
	Notice *FollowNotice `json:"notice,omitempty"`
}

// FollowNotice は帯の知らせ（`FollowUpdate.Notice`）。Text が空なら消す。
type FollowNotice struct {
	Text string `json:"text"`
	Kind string `json:"kind"`
	// Fade はしばらくして消すか（人が自分で止めた「止めました」など。読めば済むもの）。
	Fade bool `json:"fade"`
	// Choices は人に選んでもらう候補（打った駒が決まらないとき。帯にボタンで並ぶ）。
	Choices []FollowNoticeChoice `json:"choices,omitempty"`
}

// FollowNoticeChoice は帯に並べる候補 1 つ（押されたら `ChooseFollow` に Moves が来る）。
type FollowNoticeChoice struct {
	Label string   `json:"label"`
	Moves []string `json:"moves"`
}

const (
	// followTick は次の 1 周までの間（撮って前の 1 枚と比べるだけなら数ミリ秒なので短くする。2026-09-18）。
	// ⚠️ **0 に近づけないこと** —— 撮るのも画面の更新も只ではない。
	followTick = 250 * time.Millisecond
	// followSoon は**追いついていないとき**の間（手が増えた・控えた）。待つとそのぶん遅れが開く。⚠️ 0 にしない。
	followSoon = 50 * time.Millisecond
	// followLostLimit は「見失っています」と言い出すまでの、繋がらない周の数。
	// ⚠️ **1 周で言わないこと** —— 駒が飛んでいる途中の 1 枚は普通に繋がらない。続くことが異常。
	followLostLimit = 5
	// followStopWait は `StopFollow` が走っている 1 周の終わりを待つ上限（撮る側が固まっても画面を止めない）。
	followStopWait = 5 * time.Second
	// followOk は**読めて、追っている盤が映っている**周の状態。
	// ⚠️ **読んでいない周で出さないこと**（2026-10-10。ふるいが省いた周は盤を見ていないので前の状態を残す）。
	followOk = "正常に盤面を認識しています"
)

// followSession は**中継を追う 1 回の解析タブ側の持ち物**（`StudyService.follow`）。撮る側の持ち物は `followcam.Camera` の
// `captureFollow`（`FollowCamera.BeginFollow` / `EndFollow` で作り直す・捨てる）。
//
// **守る mu が 2 つある**: ループの状態（上半分）は自分の mu、答えの控え（下半分）は `StudyService.mu`
// （控えは `FollowAuto` などが局面と一緒に読み書きするため）。⚠️ **こちらの mu を持ったまま StudyService のメソッドを呼ばないこと**。
// ⚠️ **追い始め（`StartFollow`）と止めたとき（`followStop`）に、下半分も捨てる**（`resetLocked`）。前の回の控えを次の回の
// 最初の 1 枚で足さない。
type followSession struct {
	// ---- ループの状態（mu で守る）----
	mu sync.Mutex
	// on は追っているか。gen は回の番号（止めるたびに進める。古い回のループの結果を捨てる）。
	on  bool
	gen int
	// done は走っているループが終わったら閉じる（`StopFollow` が待つ）。
	done chan struct{}
	// lost は繋がらない周が続いた数（`followLostLimit`）。
	lost int
	// awaiting は**解析タブ側が答えを控えていて、次の 1 枚で確かめる**ところか（`FollowAuto.Pending`）。
	// ⚠️ **立っているときに「変わっていない」が来たら `FollowConfirm`**。⚠️ **読んだ周の後でしか確かめない** ——
	// 別の盤・盤が無い・動いている周を挟んだら倒す。
	awaiting bool
	// awaitingCells は控えた 1 枚の、比べる相手から変わったマス（確かめの周は測らないので、足したあと `FrameFits` に渡す）。
	// ⚠️ **落とすと、81 マスで読んだ手のあと比べる相手が替わらない**（2026-10-07 に実機で踏んだ）。
	awaitingCells []int
	// dropKey は帯に出している打った駒の候補（空なら出していない）。同じ候補を毎周出し直さない。
	dropKey string
	// status / statusKind は最後に出した状態の行（読んでいない周は前のまま残す）。
	status, statusKind string

	// ---- 解析タブ側の答えの控え（StudyService.mu で守る）----

	// pending は追従が 1 枚目で見つけた、まだ足していない答え（`followPending`。2026-10-05）。
	pending *followPending
	// rewind は追従が 1 枚目で見つけた、推測で足した末尾の手の差し替え（`followRewind`。2026-10-08）。
	rewind *followRewind
	// dropOffer は人に聞いている、打った駒の候補（`offerDrop` / `FollowChoose`。2026-10-09）。
	dropOffer *followDropOffer
	// base は**速い経路の比べる相手の 1 枚が、どの局面だったか**（`followBase`。2026-10-07。撮る側の `cellBase` と対）。
	base *followBase
	// lastFrame は**追従が最後に 81 マスで読んだ盤**（解析の向き。2026-10-08。`CellsLookUnmoved` が使う）。
	lastFrame *position.Board
}

// resetLocked は答えの控えを捨てる（`StudyService.mu` を持って呼ぶ）。
func (fs *followSession) resetLocked() {
	fs.pending, fs.rewind, fs.dropOffer, fs.base, fs.lastFrame = nil, nil, nil, nil, nil
}

// StartFollow は**中継を追い始める**（「中継を追う」・枠のボタン・訂正タブの「そのまま追跡」）。追っていれば何もしない。
//
// 押した瞬間の盤を追う盤として覚え（訂正してから追うなら訂正した 1 枚。撮る側の `AnchorBoard`）、そのとき読んだ 1 枚を
// 最初の 1 周として流す（捨てると速い経路の比べる相手が決まるまで 81 マスの読みを待つ。2026-10-07）。決まらなければ
// 理由を帯に出して止まる。⚠️ **戻るのは始めたところまで**（追う盤を決めるのはループの中）。
func (s *StudyService) StartFollow() error {
	cam := s.Camera
	if cam == nil {
		return errors.New("撮る側が無いので中継を追えません")
	}
	fs := &s.follow
	fs.mu.Lock()
	if fs.on {
		fs.mu.Unlock()
		return nil
	}
	fs.on = true
	fs.gen++
	gen := fs.gen
	fs.done = make(chan struct{})
	done := fs.done
	fs.lost, fs.awaiting, fs.awaitingCells, fs.dropKey = 0, false, nil, ""
	fs.status, fs.statusKind = "", ""
	fs.mu.Unlock()

	s.mu.Lock()
	fs.resetLocked()
	s.mu.Unlock()
	cam.BeginFollow()
	log.Follow.Info("中継の追跡を始めました")
	s.followPublish(gen, "追う盤を決めています…", "", &FollowNotice{})
	go s.followLoop(gen, done)
	return nil
}

// StopFollow は**人が中継の追跡を止める**（「追跡を止める」・枠のボタン・追従を壊す操作の確認で「はい」）。
//
// ⚠️ **走っている 1 周が終わるまで待ってから返す**（`followStopWait` まで）。画面はこれを待ってから根を入れ替える操作を
// 通す —— 待たないと、走っている 1 周が入れ替えた局面へ手を足す（`followguard.ts`）。
func (s *StudyService) StopFollow() {
	done := s.followStop(-1, "中継の追跡を止めました", "")
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(followStopWait):
		log.Follow.Warn("走っている 1 周が終わらないまま止めました")
	}
}

// ChooseFollow は**帯に並べた打った駒の候補から、人が選んだ手を足す**（2026-10-09。`FollowChoose`）。
// ⚠️ **選べなくなっていたら理由を帯に出すだけ**（追跡は止めない。次の 1 枚でまた聞く）。
func (s *StudyService) ChooseFollow(moves []string) {
	fs := &s.follow
	fs.mu.Lock()
	on, gen := fs.on, fs.gen
	if on {
		fs.dropKey = ""
	}
	fs.mu.Unlock()
	if !on || len(moves) == 0 {
		return
	}
	if _, err := s.FollowChoose(moves); err != nil {
		s.followPublish(gen, "", "", &FollowNotice{Text: err.Error(), Kind: "warn", Fade: true})
		return
	}
	s.followPublish(gen, followOk, "", &FollowNotice{})
}

// FollowHideRefused は**追っているあいだに枠を隠そうとして断った**ことを帯に出す（2026-10-10。撮る側の `HideFrame` から）。
// 打った駒の候補を出していたら、この文で消えるので次の 1 周で出し直させる。
//
//wails:ignore
func (s *StudyService) FollowHideRefused(message string) {
	fs := &s.follow
	fs.mu.Lock()
	on, gen := fs.on, fs.gen
	fs.dropKey = ""
	fs.mu.Unlock()
	if on {
		s.followPublish(gen, "", "", &FollowNotice{Text: message, Kind: "error", Fade: true})
	}
}

// followStop は追跡を止める。gen が -1 なら今の回を、そうでなければその回だけを止める（古い回のループは何もしない）。
// 止めたら走っているループの終わりを知らせる口を返す（止めていなければ nil）。
//
// ⚠️ **どの止まり方でも撮る側の持ち物を捨てること**（`EndFollow`。2026-10-10。止まり方によって忘れ損ねると、
// 外部エンジンの優先度が下がったまま残り、次に追ったときの録画が前の回のディレクトリに混ざった）。
// kind が空なら**人が自分で止めた**ときで、読めば済むのでしばらくして消す。
func (s *StudyService) followStop(gen int, message, kind string) <-chan struct{} {
	fs := &s.follow
	fs.mu.Lock()
	if !fs.on || (gen >= 0 && gen != fs.gen) {
		fs.mu.Unlock()
		return nil
	}
	fs.on = false
	fs.gen++
	done := fs.done
	fs.awaiting, fs.awaitingCells, fs.dropKey = false, nil, ""
	fs.status, fs.statusKind = "", ""
	fs.mu.Unlock()

	s.mu.Lock()
	fs.resetLocked()
	s.mu.Unlock()
	if cam := s.Camera; cam != nil {
		cam.EndFollow()
	}
	log.Follow.Info("中継の追跡を止めました", "理由", message)
	s.followEmit(FollowUpdate{On: false, Waiting: followWaitingOf(s.State()),
		Notice: &FollowNotice{Text: message, Kind: kind, Fade: kind == ""}})
	return done
}

// followAlive は回 gen がまだ続いているか。
func (s *StudyService) followAlive(gen int) bool {
	s.follow.mu.Lock()
	defer s.follow.mu.Unlock()
	return s.follow.on && s.follow.gen == gen
}

// followLoop は回 gen を止まるまで 1 周ずつ回す。⚠️ **1 周ずつ順番に回すこと**（間隔で撮ると、前の 1 枚を読んでいる最中に
// 次が積み上がる）。⚠️ **1 周の中でエラーが出たら止めて理由を出す**（設計原則3 は「落ちない」であって「黙る」ではない）。
func (s *StudyService) followLoop(gen int, done chan struct{}) {
	defer close(done)
	defer func() {
		if r := recover(); r != nil {
			log.Follow.Error("中継の追跡の 1 周で落ちました", "panic", r)
			s.followStop(gen, fmt.Sprintf("中継の追跡が止まりました（%v）", r), "error")
		}
	}()
	first, err := s.Camera.AnchorBoard()
	if err != nil {
		s.followStop(gen, err.Error(), "error")
		return
	}
	given := &first
	for s.followAlive(gen) {
		soon, err := s.followOnce(gen, given)
		given = nil
		if err != nil {
			s.followStop(gen, err.Error(), "error")
			return
		}
		wait := followTick
		if soon {
			wait = followSoon
		}
		time.Sleep(wait)
	}
}

// followOnce は 1 周ぶん。戻り値は「すぐ次の 1 周へ行くか」（手が増えた・控えた）。given は既に撮って読んだ 1 枚（最初の 1 周）。
func (s *StudyService) followOnce(gen int, given *followcam.Shot) (bool, error) {
	cam := s.Camera
	var shot followcam.Shot
	if given != nil {
		shot = *given
	} else {
		var err error
		if shot, err = cam.CaptureQuiet(); err != nil {
			return false, err
		}
	}
	if !s.followAlive(gen) {
		return false, nil
	}
	fs := &s.follow
	// cells は**この周の 1 枚の、比べる相手から変わったマス**（測っていなければ nil）。
	cells := shot.Cells
	var got FollowAuto
	switch {
	case shot.Skipped != "":
		// ⚠️ **読まなかった周は見送りとして数えない**（長考中はほとんどこれ）。
		fs.mu.Lock()
		pend := fs.awaiting
		if shot.Skipped != "unchanged" || !pend {
			fs.awaiting = false
		}
		pendCells := fs.awaitingCells
		fs.mu.Unlock()
		if shot.Skipped != "unchanged" || !pend {
			s.followPublish(gen, "", "", nil) // 読んでいないので前の状態のまま
			return false, nil
		}
		// **控えた 1 枚から変わっていない** ＝ 確かめが済んだ。読み直さずに足す。
		var err error
		if got, err = s.FollowConfirm(); err != nil {
			return false, err
		}
		// 変わっていないので、変わったマスも控えた 1 枚と同じ。
		cells = pendCells
	case shot.Fast:
		// **速い経路**（2026-10-07）。⚠️ **「盤が映っていない」と取り違えないこと**（SFEN が空なのは読んでいないから）。
		fast, err := s.FollowCells(shot.Cells, nil, nil)
		if err != nil {
			return false, err
		}
		if !fast.Applied && !fast.Pending && !fast.AtFrame && (len(fast.ReadCells) > 0 || len(fast.Compare) > 0) {
			// 成・不成や打った駒の種類で分かれた・行き先を確かめる。**頼まれたマスだけ読み、成・不成は見た目を比べて**
			// もう一度（81 マスは読まない）。⚠️ **1 回だけ**（決まらなければ下で 81 マスを読む）。
			var read []followcam.CellPiece
			if len(fast.ReadCells) > 0 {
				if read, err = cam.ReadCells(fast.ReadCells); err != nil {
					return false, err
				}
			}
			var promo []PromoEvidence
			if len(fast.Compare) > 0 {
				promo = s.followEvidence(fast.Compare)
			}
			if fast, err = s.FollowCells(shot.Cells, read, promo); err != nil {
				return false, err
			}
		}
		if fast.LooksOnly {
			// **変わったのは空きのマスだけで、駒は動いていない**（2026-10-08）。⚠️ **81 マスを読まない**・見送りに数えない。
			cam.CellsUnmoved()
			s.followSetPending(false, nil)
			s.followPublish(gen, followOk, "", nil)
			return false, nil
		}
		if fast.NoMoves {
			// **変わったマスを説明できる手順が 1 つも無い**（2026-10-08）。⚠️ **すぐ 81 マスを読まない**（組が変わるのを待つ）・
			// 見送りに数えない・⚠️ **控えは捨てる**（説明の付かない盤のまま、変わっていない次の 1 枚で控えた手を足さない）。
			cam.CellsUnclear()
			s.followSetPending(false, nil)
			s.followPublish(gen, "", "", nil)
			return false, nil
		}
		if fast.Applied || fast.Pending || fast.AtFrame {
			got = fast
			break
		}
		// 手が決まらなかった（成・不成・打った駒・手が被った 1 枚など）。**同じ 1 枚を 81 マスで読む。**
		again, err := cam.RecognizeQuiet()
		if err != nil {
			return false, err
		}
		read, ok, err := s.followRead(gen, again)
		if err != nil || !ok {
			s.followSetPending(false, nil)
			return false, err
		}
		got = read
	default:
		read, ok, err := s.followRead(gen, shot)
		if err != nil || !ok {
			s.followSetPending(false, nil)
			return false, err
		}
		got = read
	}
	if got.Pending {
		s.followSetPending(true, cells)
	} else {
		s.followSetPending(false, nil)
	}
	if got.Kind == FollowSame && !got.AtFrame && len(cells) > 0 {
		// **81 マスを読んだら何も指していなかったのに、変わったマスがある**（駒を掴んで行き先が光っている。2026-10-08）。
		// その組を覚えさせる。⚠️ **駒が空いて見えるマスがあれば覚えさせない**（`CellsLookUnmoved`）。
		if s.CellsLookUnmoved(cells) {
			cam.CellsUnmoved()
		}
	} else if got.Pending || got.Applied {
		// **手が見つかった**。「動いていない」と覚えた組なら忘れさせる。
		cam.ForgetUnmoved()
	}
	// ⚠️ **先端と合った 1 枚だけを、速い経路の比べる相手にする**。81 マスの読みでぴったり合った（`AtFrame`）か、
	// 比べる相手から変わったマスが足した手で説明できた（`FrameFits`。測った 1 枚なら空でも）とき。
	// ⚠️ **控えているあいだは替えない**（先端とこの 1 枚が食い違っている）。
	atFrame := got.AtFrame
	if !atFrame && !got.Pending && cells != nil {
		atFrame = s.FrameFits(cells)
	}
	if atFrame {
		cam.SetCellBase()
	}
	// ⚠️ **足したうえで残りを控えた周（`Applied` かつ `Pending`）は足した側として出す**（2026-10-06）。
	if got.Pending && !got.Applied {
		// ⚠️ **見送りとして数えない**（足せる手は見つかっている）。間を置かずに次の 1 枚で確かめる。
		s.followPublish(gen, "手を確かめています", "", nil)
		return true, nil
	}
	return s.followAfter(gen, got), nil
}

// followSetPending は控えの有無と、控えた 1 枚の変わったマスを覚える。
func (s *StudyService) followSetPending(on bool, cells []int) {
	s.follow.mu.Lock()
	s.follow.awaiting, s.follow.awaitingCells = on, cells
	s.follow.mu.Unlock()
}

// followRead は**読んだ 1 枚**を追従に渡す（別の盤・盤が無いなら見送って ok は偽）。
func (s *StudyService) followRead(gen int, shot followcam.Shot) (FollowAuto, bool, error) {
	cam := s.Camera
	waiting := followWaitingOf(s.State())
	// ⚠️ **別の盤を先に見ること**（2026-09-18）。盤の有無のふるいで落ちた周は読んでいないので SFEN が空で、
	// 順番が逆だと大盤が映っているのに「盤が映っていません」と出る。⚠️ **止めないこと**（大盤はすぐ本物へ戻る）。
	if shot.OffBoard {
		cam.SaveFollowMiss(waiting, "offboard")
		s.followPublish(gen, fmt.Sprintf("別の盤が映っています（%s）", shot.OffBoardReason), "", nil)
		return FollowAuto{}, false, nil
	}
	if shot.SFEN == "" {
		cam.SaveFollowMiss(waiting, "noboard")
		s.followPublish(gen, "盤が映っていません", "", nil)
		return FollowAuto{}, false, nil
	}
	// ⚠️ **訂正タブへ流さないこと**（2026-09-15 に実機で踏んだ。人が訂正タブで作業していると上書きされた）。
	if !s.followAlive(gen) {
		return FollowAuto{}, false, nil
	}
	got, err := s.FollowAuto(shot.SFEN, shot.CellConfidence, shot.CellHidden, nil)
	if err != nil {
		return got, false, err
	}
	// ⚠️ **成りも不成も指せる手なら、動く前と後の見た目を比べて同じ 1 枚でもう一度**（2026-10-07）。
	// ⚠️ **2 回目は証拠が空でも渡す**（nil だとまた頼まれる）。
	if len(got.Compare) > 0 {
		got, err = s.FollowAuto(shot.SFEN, shot.CellConfidence, shot.CellHidden, s.followEvidence(got.Compare))
		if err != nil {
			return got, false, err
		}
	}
	return got, true, nil
}

// followEvidence は**動く前と後の駒の見た目を測って**成・不成の証拠にする（2026-10-07）。⚠️ **nil を返さない**（空でも渡す）。
// ⚠️ **捨てるのは測れなかった組（ちょうど -1）だけ**（似ていない駒どうしは近さが負になりうる）。
// ⚠️ **`Same` を落とさないこと**（打った駒と盤上の駒の比べ。2026-10-09）。
func (s *StudyService) followEvidence(pairs []followcam.CellPair) []PromoEvidence {
	sims := s.Camera.ComparePieces(pairs)
	out := []PromoEvidence{}
	for i, p := range pairs {
		sim := -1.0
		if i < len(sims) {
			sim = sims[i]
		}
		if sim > -1 {
			out = append(out, PromoEvidence{From: p.From, To: p.To, Sim: sim, Same: p.Same})
		}
	}
	return out
}

// followAfter は追従の結果（足した・足せなかった）を帯と録画に出す。戻り値は「すぐ次の 1 周へ行くか」。
func (s *StudyService) followAfter(gen int, got FollowAuto) bool {
	cam := s.Camera
	fs := &s.follow
	waiting := followWaitingOf(s.State())
	if got.Applied {
		// ⚠️ **「足しました」をどこにも書かないこと**（足した手は手順に出る）。
		fs.mu.Lock()
		hadDrop := fs.dropKey != ""
		fs.dropKey = ""
		fs.lost = 0
		fs.mu.Unlock()
		var notice *FollowNotice
		if hadDrop {
			notice = &FollowNotice{} // 打った駒の候補を出していたら片付ける（別の 1 枚で決まった）
		}
		// **推測で足した末尾の手を差し替えた**ときは、何を何にしたかを帯に出す。⚠️ **黙って差し替えないこと**。
		if got.Rewound && got.Note != "" {
			notice = &FollowNotice{Text: got.Note, Fade: true}
		}
		// ⚠️ **手を決めた 1 枚だけを残す**（デバッグ用の録画）。手数は足したあとの「次の手数」から逆算する。
		cam.SaveFollowFrame(max(waiting-len(got.Moves), 0), got.Moves, got.Guess)
		s.followPublish(gen, followOk, "", notice)
		return true
	}
	// ⚠️ **盤が上下逆なら、それを言うのが先**（2026-09-15）。⚠️ **勝手に回さないこと**（盤の向きは局面の解釈そのもの）。
	if got.Flipped {
		s.followStop(gen, fmt.Sprintf("盤が上下逆に映っているようです（逆向きなら %s で繋がります）。", got.FlipMove)+
			"訂正タブの「目線」を『後手が手前』にして、撮り直して「この局面を解析する」からやり直してください", "warn")
		return false
	}
	// ⚠️ **何かが盤に被っている周も迷子ではない**（2026-10-06）。待つだけで「見失っています」に数えない。
	// ⚠️ **「変わっていません」と言わないこと** —— 次にすることは同じ（待つ）でも理由が違う。
	if got.Unseen > 0 {
		cam.SaveFollowMiss(waiting, "covered")
		s.followResetLost()
		s.followPublish(gen, fmt.Sprintf("盤に何か被っています（%d マス）", got.Unseen), "", nil)
		return false
	}
	// ⚠️ **「変わっていない」は迷子ではない**（長考中は毎周これ）。⚠️ **ここも残すこと**（認識が外して『変わっていない』に
	// 見えているときも同じ顔をする。残すのは変わり目だけ。撮る側が間引く）。
	if got.Kind == FollowSame || got.Kind == FollowUnreadable {
		cam.SaveFollowMiss(waiting, got.Kind)
		s.followResetLost()
		status := followOk
		if got.Kind == FollowUnreadable {
			status = "盤面として読めません"
		}
		s.followPublish(gen, status, "", nil)
		return false
	}
	// **打った駒の種類が決まらない**（2026-10-09）。帯に候補を並べて人に選んでもらう。⚠️ **見失いに数えない**・
	// ⚠️ **同じ候補を毎周出し直さない**（帯が伸び縮みする）。
	if len(got.DropChoices) > 0 {
		keys := make([]string, 0, len(got.DropChoices))
		choices := make([]FollowNoticeChoice, 0, len(got.DropChoices))
		for _, c := range got.DropChoices {
			keys = append(keys, strings.Join(c.Moves, ","))
			label := strings.Join(c.Text, " ")
			if label == "" {
				label = strings.Join(c.Moves, " ")
			}
			choices = append(choices, FollowNoticeChoice{Label: label, Moves: c.Moves})
		}
		key := strings.Join(keys, "|")
		fs.mu.Lock()
		fresh := key != fs.dropKey
		fs.dropKey = key
		fs.lost = 0
		fs.mu.Unlock()
		var notice *FollowNotice
		if fresh {
			cam.SaveFollowMiss(waiting, "drop")
			notice = &FollowNotice{Text: "打った駒の候補", Choices: choices}
		}
		s.followPublish(gen, "打った駒が決まりません", "warn", notice)
		return false
	}
	fs.mu.Lock()
	hadDrop := fs.dropKey != ""
	fs.dropKey = ""
	fs.lost++
	lost := fs.lost
	fs.mu.Unlock()
	var notice *FollowNotice
	if hadDrop {
		notice = &FollowNotice{} // 候補が出なくなった（別の 1 枚で決まった・盤が変わった）
	}
	// ⚠️ **繋げなかった周の画像を残す**（上限つき。採用した画像だけでは止まった原因が分からない）。
	kind := got.Kind
	if kind == "" {
		kind = "miss"
	}
	cam.SaveFollowMiss(waiting, kind)
	if lost >= followLostLimit {
		reason := got.Reason
		if reason == "" {
			reason = got.Kind
		}
		s.followPublish(gen, fmt.Sprintf("中継の手順を見失っています（%s）", reason), "warn", notice)
		return false
	}
	s.followPublish(gen, followOk, "", notice)
	return false
}

// followResetLost は見失いの数を 0 に戻す。
func (s *StudyService) followResetLost() {
	s.follow.mu.Lock()
	s.follow.lost = 0
	s.follow.mu.Unlock()
}

// followPublish は回 gen の様子を画面へ流す（`follow:update`）。status が空なら最後の状態の行のまま（読んでいない周）。
// notice が nil なら知らせは変えない。⚠️ **止めた回からは流さない**（古いループの結果が止めた画面を書き換えない）。
func (s *StudyService) followPublish(gen int, status, kind string, notice *FollowNotice) {
	fs := &s.follow
	fs.mu.Lock()
	if !fs.on || fs.gen != gen {
		fs.mu.Unlock()
		return
	}
	if status != "" {
		fs.status, fs.statusKind = status, kind
	}
	u := FollowUpdate{On: true, Status: fs.status, StatusKind: fs.statusKind, Notice: notice}
	fs.mu.Unlock()
	u.Waiting = followWaitingOf(s.State())
	s.followEmit(u)
}

// followEmit は `follow:update` を流す（`Emit` が無ければ何もしない。設計原則3）。
func (s *StudyService) followEmit(u FollowUpdate) {
	if s.Emit != nil {
		s.Emit("follow:update", u)
	}
}

// followWaitingOf は本譜の先端から**次に来るはずの手数**（棋譜の数え方）を割り出す。局面が無ければ 0。
//
// ⚠️ **今見ている局面の手数を使わないこと**（枝を読んでいると本譜の先端とずれる）。⚠️ **`First` を足すこと**
// （`Node.Number` は根からの手数）。
func followWaitingOf(st StudyState) int {
	if !st.Loaded {
		return 0
	}
	n := 0
	for _, node := range st.Nodes {
		if node.ID == st.MainTip {
			n = node.Number
			break
		}
	}
	return st.First + n + 1
}

package ikkyoku

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config は永続化する設定（ディスプレイ番号・領域・保存先）。
// CLI のフラグが指定された場合はフラグを優先し、Config はあくまで既定値の置き場所として使う。
type Config struct {
	Display *int    `json:"display,omitempty"`
	Region  *Region `json:"region,omitempty"`
	OutDir  string  `json:"outDir,omitempty"`
	Hotkey  string  `json:"hotkey,omitempty"`

	// SutemeDataDir は suteme の駒種推論器の学習データ
	// (training_data_v2.json / model_v2.json)を置いたディレクトリ。
	//
	// 空なら suteme 既定の探索(カレントディレクトリ → 実行ファイルのディレクトリ)に任せる。
	// 指定できるようにしてあるのは、**学習データが 3.5MB 級で、しかも育て続けるもの**
	// だから。実行ファイルの隣にコピーを置く運用にすると、更新のたびにコピーし直す必要が
	// あり、古いデータで認識してしまう事故が起きる。開発中は suteme のリポジトリを
	// 直接指しておけば、データを更新した結果がそのまま反映される。
	SutemeDataDir string `json:"sutemeDataDir,omitempty"`

	// SutemeSource は認識器(駒種推論器と盤の縁の帯の判定器)をどこから読むか。
	//
	// **exe 1 つで配れる形と、学習データを育てながら使う形の両方が要る**ので、
	// 読み込み元を選べるようにしてある。値は 3 つ:
	//
	//	"" / "auto" … SutemeDataDir が指定されていればそちら、駄目なら焼き込み、
	//	               それも無ければ suteme 既定の探索(既定)
	//	"dir"       … SutemeDataDir から読む。焼き込みがあっても使わない
	//	"embed"     … バイナリに焼き込んだものを読む(`-tags embedmodel` のビルドのみ)
	//
	// ⚠️ **焼き込みが入っていないビルドで "embed" にしても認識器は用意できない。**
	// その場合は dir へ落ちる(`recognize.EmbeddedAvailable`)。設計原則3(段階的に劣化する)
	// のとおり、**どれも読めなくてもアプリは動く**(認識結果が空になるだけ)。
	SutemeSource string `json:"sutemeSource,omitempty"`

	// FitOnStartup は起動時に盤面を探してガイド枠を合わせるか。
	//
	// **既定は false(探さない)。** 枠の位置はユーザーが手で合わせたものなので、
	// 起動のたびに勝手に動かすのを既定の挙動にはしない。有効にすると毎回の起動で
	// 一度だけ探す(GUI の設定タブから切り替える)。
	//
	// omitempty を付けていないのは、**切ってあること自体を設定ファイルに残す**ため。
	// このファイルは手で編集する前提でもあるので、キーが消えると存在に気づけない。
	FitOnStartup bool `json:"fitOnStartup"`

	// ClickThrough はガイド枠の内側のクリックを、後ろの画面へ素通しするか。
	//
	// **枠は中継の「上」に重ねる最前面のウィンドウ**（AlwaysOnTop）なので、
	// 合わせたあとは**その下にある中継のシークバーや再生ボタンが押せない**。
	// 押すには枠をどかすしかなく、どかすと位置合わせがやり直しになる。
	// これを有効にすると、**撮る範囲の内側だけ**がマウスに対して透明になる
	// （Windows の `WS_EX_LAYERED | WS_EX_TRANSPARENT`。⚠️ **TRANSPARENT だけでは
	// 全く効かない** —— 実測で確かめた。`_cmd/ikkyoku/clickthrough_windows.go`）。
	//
	// ⚠️ **素通しにするのは「撮る範囲の内側」だけで、枠ごとではない。**
	// ツールバー（撮る・□・✕・ドラッグ移動）と縁のリサイズ領域は押せるままにする
	// —— 枠ごと素通しにすると、**この設定を切るまで枠を動かすことも撮ることも
	// できなくなる**（切る口は設定タブにしか無い）。切り替えの実装は
	// `_cmd/ikkyoku/captureservice.go` の `watchCursor`。
	//
	// **既定は false。** 枠は「今どこを撮るか」を示す道具なので、押せると
	// 思って押したクリックが後ろへ抜けるのを既定の挙動にはしない。
	//
	// ⚠️ **Windows のみ。** 他 OS ではスタブが何もしない（設定は保存できる）。
	//
	// omitempty を付けていないのは FitOnStartup と同じ理由（切ってあること自体を残す）。
	ClickThrough bool `json:"clickThrough"`

	// EvalGraphDetached は評価値グラフを**別ウィンドウに切り離しているか**
	// （2026-09-08）。
	//
	// **ペインのままだと盤の大きさに効く**（`--board-size` がグラフの高さを
	// 引いている）ので、盤を好きな大きさにしたい人のために窓へ出せるようにした。
	// ⚠️ **切り離しているあいだ、メイン画面のペインは出さない**（同じ値を
	// 2 か所に描かない）。
	//
	// **起動のたびにドックへ戻さないため**に残す。⚠️ **枠の表示（残さない）とは
	// 扱いが違う** —— あちらは「撮るときだけ使う道具」だが、こちらは
	// **画面の組み方の好み**なので、次の起動でも同じ形で始まってほしい。
	//
	// omitempty を付けていないのは ClickThrough と同じ理由（切ってあること自体を残す）。
	EvalGraphDetached bool `json:"evalGraphDetached"`

	// StudyPaneDetached は**候補手の面**（警告・解析の行・エンジンのカード）を
	// 別ウィンドウに切り離しているか（2026-09-08。2026-09-12 に**手順を分けた**）。
	//
	// **盤の大きさに依存しない大きさで見たい**というのが切り離しの目的
	// （`--board-size` は右の列の幅を引いている）。⚠️ **評価値グラフとは別の設定**
	// —— 片方だけ切り離す使い方が普通なので、1 つにまとめない。
	//
	// ⚠️ **2026-09-12 に意味が狭まった。** 以前は右の列まるごとだったが、
	// **候補手と手順を別々の窓に出せる**ようにしたので、ここが持つのは
	// **候補手の面だけ**（手順は MovePaneDetached）。⚠️ **古い設定ファイルでは
	// 「右の列ごと切り離していた」が「候補手だけ切り離している」になる** ——
	// 手順はドックに残るので、**何も見えなくなることはない**（設計原則3）。
	//
	// ⚠️ **切り離しているあいだ、メイン画面の候補手の面は出さない**（同じ値を
	// 2 か所に描かない）。**起動のたびにドックへ戻さない**のも評価値グラフと同じ。
	StudyPaneDetached bool `json:"studyPaneDetached"`

	// MovePaneDetached は**手順の面**（手順の見出しの行・手順のツリー）を
	// 別ウィンドウに切り離しているか（2026-09-12）。
	//
	// ⚠️ **候補手（StudyPaneDetached）とは別の設定。1 つにまとめないこと** ——
	// 「盤と手順を並べて、候補手は大きな別窓で読む」「盤と候補手を並べて、
	// 手順だけ長い窓で辿る」のどちらも普通の使い方で、**どちらを外に出すかは
	// そのときの読み方で変わる**。
	//
	// ⚠️ **両方を切り離すと、メイン画面の右の列そのものが消える**（盤が
	// 目いっぱい広がる）。そのときも**解析を持っているのは候補手の面**なので、
	// 連続解析は向こうの窓が回す（手順の窓のボタンは持ち主へ頼むだけ）。
	MovePaneDetached bool `json:"movePaneDetached"`

	// HideWinRateBar は解析タブの**勝率バー（評価値バー）を隠しているか**
	// （2026-09-10）。切り替えるのは**解析タブの黒地の右クリック**（盤と駒台の上では
	// 掴んだ駒を離す操作が先に居るので出さない）。
	//
	// **中継を観ながら使うので「評価値を見たくない」場面がある** ——
	// 盤の真上の帯は目に入るのを避けようが無いので、消せる口を用意した。
	// ⚠️ **解析そのものは止めない**（帯を出さないだけで、候補手も評価値も
	// 右の列には出る）。**見たくないものだけを消す**のがこの設定の役目。
	//
	// ⚠️ **「隠しているか」で持つこと**（`ShowWinRateBar` にしない）。
	// bool のゼロ値は false なので、**設定ファイルに何も書かれていない状態＝表示**
	// になる。逆にすると、古い設定ファイルで開いたときに帯が消えたまま始まる。
	//
	// ⚠️ **画面の組み方の好みなので、次の起動でも同じ形で始める**
	// （EvalGraphDetached と同じ扱い）。
	HideWinRateBar bool `json:"hideWinRateBar"`

	// HidePlayerNames は解析タブの**対局者名を隠しているか**（2026-09-10）。
	// 切り替えるのは勝率バーと同じ**解析タブの黒地の右クリック**。
	//
	// ⚠️ **勝率バーとは別の設定**（切り離しの 2 つと同じ理由）。
	// **帯だけ消して名前は残す**（誰の対局かは見ていたい）も、
	// **両方消す**（盤を大きくしたい）も、どちらも普通の使い方。
	//
	// ⚠️ **両方隠したときだけ、盤の上の 1 行そのものが消える**
	// （`--winrate-h` を返して盤が 34px 大きくなる）。片方でも出ているなら
	// 行は残るので、**盤の大きさは変わらない。**
	//
	// ⚠️ `HideWinRateBar` と同じく**「隠しているか」で持つこと。**
	HidePlayerNames bool `json:"hidePlayerNames"`

	// Training は訂正した局面を suteme の学習用サーバへ送る設定。
	Training TrainingConfig `json:"training"`

	// AnalyzeSeconds は解析タブの「考える秒数」。
	//
	// ⚠️ **ポインタなのは 0 に意味があるから**（`0` = 無制限）。値で持って
	// `omitempty` を付けると、**「無制限」を選んだ設定がファイルから消えて、
	// 次の起動で既定（3 秒）に戻る**。nil が「まだ選んでいない」。
	//
	// **既定値はここに書かない**（解決は `Config.ThinkSeconds`）。
	//
	// ⚠️ **連続モードのチェックとは扱いが違う**（あちらは起動のたびに入で始まる
	// その場かぎりの操作）。秒数は**待ち時間を決める値**で、連続解析では
	// 「手数 × 秒数」がそのまま所要時間になるので、**選び直しを毎回やらせない。**
	AnalyzeSeconds *int `json:"analyzeSeconds,omitempty"`

	// PonanzaConstant は評価値を勝率に直すときの定数（解析タブの勝率バー）。
	//
	//	勝率(先手) = 1 / (1 + exp(-評価値 / この値))
	//
	// **0 なら既定の 1500**（既定値の解決は `analyze.PonanzaConstantOr` の 1 か所。
	// **ここに既定値を書かないこと**）。設定できるようにしてあるのは、
	// **エンジンによって評価値の尺度が違う**から（⚠️ 特に自作 `engine` の PST は
	// 手作り・未調整なので、そのままでは勝率が振り切れやすい）。
	PonanzaConstant float64 `json:"ponanzaConstant,omitempty"`

	// Engines は登録した USI エンジンの一覧。
	//
	// **1 つに絞らない**（2026-08-11）。検討ツールとして実用になるかは繋ぐエンジンの
	// 棋力で決まるが、**どのエンジンが正しいかは局面によって違う**。同じ局面を
	// 複数のエンジンに読ませて評価値を並べられることが「別の選択も一つの局として
	// 辿る」という構想に効く（`Enabled` を付けたものが同時に走る）。
	//
	// **空なら同梱のエンジン 1 つ**として扱う（`EngineList`）。設定ファイルを
	// 作っていない状態でも解析できる、という既定の挙動を変えないため。
	Engines []EngineEntry `json:"engines,omitempty"`

	// Engine は**旧形式**（単一エンジン）の設定。
	//
	// ⚠️ **読み込んだ時点で Engines へ移し、この欄は捨てる**（`LoadConfig`）。
	// 次に保存したときにファイルからも消える。**新しいコードはここを読まないこと。**
	Engine *EngineConfig `json:"engine,omitempty"`

	// PieceFonts は登録した駒フォント（＝端末のフォントから焼く駒の字）の一覧。
	//
	// **同梱フォントを配れる範囲が狭いのでこうなっている**（`piecefont` の
	// パッケージコメント）。登録は端末の中の話で、**焼いたフォントは
	// ディスクにも残さないし配りもしない。**
	//
	// ⚠️ **空なら同梱の駒フォント 1 つだけ**（`PieceFontList` は空を返し、
	// 画面は「同梱」を選んでいる状態になる）。エンジンと違って
	// **「登録が無いと動かない」ということが無い**ので、既定を差し込まない。
	PieceFonts []PieceFontEntry `json:"pieceFonts,omitempty"`

	// Gyoku は王を玉で書くか。**先後を選べる**（`GyokuNone` / `Black` / `White` / `Both`）。
	//
	// **玉は王将/玉将という駒そのものの呼び分け**（上位者が王）なので、
	// 片側だけがありうる。⚠️ **左馬と扱いを揃えないこと**（あちらは盤全体）。
	//
	// ⚠️ **知らない値は「王のまま」に倒す**（`NormalizeGyoku`）。
	// `<shogi-board>` の `gyoku` 属性は「値なし・知らない値なら両方」だが、
	// **あちらは属性が付いている時点で「玉を使う」と言っている**のに対し、
	// こちらは**使うかどうかも含めて表す欄**なので既定が違う。
	Gyoku string `json:"gyoku,omitempty"`

	// HidariUma は馬を左馬（馬の左右反転。縁起物の飾り駒の字）で書くか。
	//
	// ⚠️ **先後の区別は無い**（盤全体）。左馬は**盤の見た目の選択**なので、
	// 使うと決めたら両方そうなる（`core/web/README.md`）。
	HidariUma bool `json:"hidariUma,omitempty"`

	// PieceColor は駒の字の色（`#rrggbb`）。**空なら既定**（`DefaultPieceColor`）。
	//
	// ⚠️ **既定値の解決は `PieceInk` の 1 か所。** ここに書かないこと
	// （`PonanzaConstant` / 折れ線の色と同じ）。
	PieceColor string `json:"pieceColor,omitempty"`

	// PieceOpacity は駒の字の濃さ（0〜1）。**0 なら既定**（＝1.0、そのまま）。
	//
	// **字の強いフォント（太い明朝・毛筆）は少し薄いほうが盤に映える。**
	// 端末のフォントを選べるようにした以上、書体ごとに濃さを合わせたくなる。
	//
	// ⚠️ **色と別に持つのは設定ファイルの都合**（色を変えても濃さが消えない）。
	// **画面に渡すときは 1 つの rgba にまとめる**（`PieceInk`）——
	// 別々に配ると、HTML で駒を描く側が element の `opacity` を使うことになり
	// **背景ごと透ける。**
	PieceOpacity float64 `json:"pieceOpacity,omitempty"`

	// PieceFont は今使っている駒フォントの登録 ID。**空なら同梱。**
	//
	// ⚠️ **登録の中に「使う」印を持たせない**（エンジンの `Enabled` とは違う）。
	// 盤は 1 つしかなく、駒の字も同時に 1 つしか使えないので、
	// **どれを使うかは一覧の外に 1 つ持つのが正しい** ——
	// 印にすると「2 つに印が付いている」という表せてはいけない状態が作れる。
	PieceFont string `json:"pieceFont,omitempty"`

	// KifuDBPath は棋譜データベース（kicho の SQLite）のファイルパス。
	//
	// **空なら `os.UserConfigDir()/ikkyoku/kicho.db`**（既定値の解決は
	// `Config.KifuDB` の 1 か所。**ここにもフロントにも書かないこと**）。
	//
	// 指定できるようにしてあるのは、**kicho アプリの DB
	// （`%APPDATA%\kicho\kicho.db`）を共用したいことがある**ため。
	// kicho の UI は最終的に「テスト用のモック」になる予定で、
	// そのとき同じ棚を両方から見られると確かめやすい。
	//
	// ⚠️ **同じ DB を kicho アプリと同時に開くのは勧めない。** `store` の
	// `SetMaxOpenConns(1)` はプロセス内の直列化でしかない。プロセスをまたぐ
	// 競合には kicho 側で WAL と `busy_timeout` を入れてある（2026-09-07）ので
	// 即 `database is locked` にはならないが、**既定を分けてあるのは変えていない**
	// （共用は開発時の意図的な操作）。
	//
	// ⚠️ **kicho と ikkyoku は別バイナリなのでスキーマ版が食い違いうる。**
	// 片方だけ更新した状態で同じ DB を指すと `store.ErrSchemaTooNew` で開けない。
	//
	// ⚠️ **ここが読めなくてもアプリは動く**（設計原則3）。棋譜タブだけが
	// 理由を出して機能せず、撮った 1 局面と貼った棋譜の解析は今までどおり。
	KifuDBPath string `json:"kifuDbPath,omitempty"`
}

// PieceFontEntry は登録した駒フォント 1 つ。
//
// **持っているのは「元フォントのどれを使うか」だけ。** 焼いた TTF は
// 持たない（起動のたびに焼き直す。`piecefont` のパッケージコメント）。
type PieceFontEntry struct {
	// ID は一覧の中でこの登録を指す識別子。
	//
	// ⚠️ **パスを鍵にしないこと。** TTC は 1 ファイルに複数の書体が入っており
	// （`msmincho.ttc` の MS 明朝と MS P明朝）、パスだけでは指せない。
	// **CSS の family 名もこの ID から作る**（`PieceFontFamily`）。
	ID string `json:"id"`

	// Name は表示名。**空なら元フォントの名前**（`Source`）。
	//
	// ⚠️ **既定の名前を value に入れないこと**（エンジンの `Name` と同じ）。
	// 入れると、登録し直しても名前が追従しなくなる。
	Name string `json:"name,omitempty"`

	// Path / Index は元フォントの場所と、その中の書体番号。
	Path  string `json:"path"`
	Index int    `json:"index,omitempty"`

	// Source は登録したときの書体名。
	//
	// **フォントが消えた・入れ替わったときに「何だったか」が残る**ようにしてある。
	// ⚠️ **これを表示の正としないこと** —— 今そこにある書体の名前は読み直せば分かる。
	Source string `json:"source,omitempty"`
}

// DisplayName は画面に出す名前を返す。
//
// ⚠️ **「空なら元フォントの名前」の解決はここ 1 か所。**
// フロントにも呼び出し側にも書かないこと（`EngineEntry.DisplayName` と同じ）。
func (e PieceFontEntry) DisplayName() string {
	if n := strings.TrimSpace(e.Name); n != "" {
		return n
	}
	if s := strings.TrimSpace(e.Source); s != "" {
		return s
	}
	return filepath.Base(e.Path)
}

// BuiltinPieceFontName は同梱の駒フォントの表示名（`PieceFont` が空のとき）。
const BuiltinPieceFontName = "同梱（Noto Serif JP）"

// 王を玉で書くかの選択肢。
//
// ⚠️ **値は `<shogi-board>` の `gyoku` 属性に合わせてある**（SFEN の手番トークン）。
// **勝手に別の語彙にしないこと** —— そのまま属性に渡せるのが要点で、
// 変換表を挟むと片方だけ直したときに黙って食い違う。
const (
	// GyokuNone は王のまま（属性を付けない）。**既定。**
	GyokuNone = ""
	// GyokuBlack は先手だけ玉。
	GyokuBlack = "black"
	// GyokuWhite は後手だけ玉。
	GyokuWhite = "white"
	// GyokuBoth は先後とも玉。
	GyokuBoth = "both"
)

// 駒の字の色と濃さの既定。
//
// **`core/web` の `--shogi-piece-color` の既定と同じ値**（`shogi-board.js`）。
// ⚠️ **向こうが変わったらここも直すこと**（`CELL` / `MARGIN` と同じ約束）。
const (
	DefaultPieceColor   = "#1a1a1a"
	DefaultPieceOpacity = 1.0
	// MinPieceOpacity は薄くできる下限。**0 まで許さない**のは、
	// **駒が消えて盤が壊れたようにしか見えない**から（戻し方も分からなくなる）。
	MinPieceOpacity = 0.2
)

// PieceInk は駒の字の色を、**画面にそのまま当てられる 1 つの値**にして返す。
//
// ⚠️ **色と濃さを別々に渡さないこと。** `<shogi-board>` の中は SVG なので
// `fill-opacity` でも足りるが、**ikkyoku は HTML でも駒を描く**（駒台のチップ・
// 掴んだ駒の絵）。そちらで濃さを別に当てると element の `opacity` になり、
// **駒の背景（木地）ごと透ける。** alpha 込みの色なら両方で同じものが使える。
//
// ⚠️ **既定値の解決もここ 1 か所**（呼び出し側にもフロントにも書かない）。
func PieceInk(color string, opacity float64) string {
	r, g, b, ok := parseHexColor(color)
	if !ok {
		r, g, b, _ = parseHexColor(DefaultPieceColor)
	}
	a := NormalizePieceOpacity(opacity)
	if a >= 1 {
		// そのままの濃さなら 16 進で返す（設定ファイルにも画面にも読みやすい）。
		return fmt.Sprintf("#%02x%02x%02x", r, g, b)
	}
	return fmt.Sprintf("rgba(%d, %d, %d, %s)", r, g, b,
		strconv.FormatFloat(a, 'f', -1, 64))
}

// NormalizePieceOpacity は濃さを正規化する（**0 は「未設定」＝既定**）。
//
// ⚠️ **範囲外を弾かずに丸めること。** 設定ファイルは手で編集する前提でもあり、
// **打ち間違いで駒が消えるより、読める濃さに丸めるほうがまし**（設計原則3）。
func NormalizePieceOpacity(v float64) float64 {
	if v <= 0 {
		return DefaultPieceOpacity
	}
	if v < MinPieceOpacity {
		return MinPieceOpacity
	}
	if v > 1 {
		return 1
	}
	return v
}

// NormalizePieceColor は色を正規化する（**読めない値は空＝既定**）。
func NormalizePieceColor(v string) string {
	r, g, b, ok := parseHexColor(v)
	if !ok {
		return ""
	}
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

// parseHexColor は `#rrggbb` を読む（`#rgb` も受ける）。
func parseHexColor(s string) (r, g, b int, ok bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "#") {
		return 0, 0, 0, false
	}
	h := s[1:]
	if len(h) == 3 {
		// #rgb → #rrggbb（<input type="color"> は出さないが、手で書けてしまう）
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(v>>16) & 0xff, int(v>>8) & 0xff, int(v) & 0xff, true
}

// GyokuOption は「王/玉」の選択肢 1 つ（画面に出す）。
type GyokuOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// GyokuOptions は選べる書き方の一覧。
//
// ⚠️ **フロントにこの表を書かないこと**（`EngineColors` と同じ）。
// 値と文言を 2 か所に持つと、増やしたときに片方だけ古くなる。
var GyokuOptions = []GyokuOption{
	{Value: GyokuNone, Label: "どちらも王"},
	{Value: GyokuWhite, Label: "後手だけ玉"},
	{Value: GyokuBlack, Label: "先手だけ玉"},
	{Value: GyokuBoth, Label: "どちらも玉"},
}

// NormalizeGyoku は設定の値を正規化する（**知らない値は「王のまま」**）。
//
// ⚠️ **`<shogi-board>` の属性とは既定が違う**（あちらは知らない値なら両方）。
// 属性は付いている時点で「玉を使う」と言っているが、こちらは**使うかどうかも
// 含めて表す欄**なので、読めない値を「玉を使う」に倒すと、
// **設定ファイルの打ち間違いで盤の字が勝手に変わる。**
func NormalizeGyoku(v string) string {
	switch v {
	case GyokuBlack, GyokuWhite, GyokuBoth:
		return v
	default:
		return GyokuNone
	}
}

// GyokuFor は片側について、その駒を玉で書くかを返す。
//
// ⚠️ **この判定を呼び出し側に書かないこと。** 盤（`<shogi-board>` の属性）と
// ikkyoku が自分で描く駒（駒台・掴んだ駒の絵）で**別々に書くと食い違う**
// —— しかも「盤は玉なのに掴むと王」という、見ないと分からない壊れ方をする。
func GyokuFor(v string, black bool) bool {
	switch NormalizeGyoku(v) {
	case GyokuBoth:
		return true
	case GyokuBlack:
		return black
	case GyokuWhite:
		return !black
	default:
		return false
	}
}

// PieceFontFamily は登録 1 つ分の CSS の family 名を返す。
//
// ⚠️ **登録ごとに違う名前であること。** 同じ名前で複数登録すると、どれが当たるかが
// ブラウザ任せになって切り替えが効かなくなる（`core/web/README.md`）。
// ⚠️ **同梱の `ShogiSFEN` とも必ず違うこと** —— 同名で上書き登録すると、
// **同梱に戻せなくなる。**
func PieceFontFamily(id string) string { return "ShogiUser-" + id }

// PieceFontList は登録済みの駒フォント一覧を返す。
//
// ⚠️ **エンジンと違って、空のときに既定を差し込まない。**
// あちらは「登録が無いと解析できない」ので同梱を 1 つ返すが、駒の字は
// **登録が無くても同梱で描ける**（`PieceFont` が空 ＝ 同梱）。
func (c Config) PieceFontList() []PieceFontEntry {
	out := make([]PieceFontEntry, len(c.PieceFonts))
	copy(out, c.PieceFonts)
	return out
}

// CurrentPieceFont は今使っている登録を返す（同梱なら ok=false）。
//
// ⚠️ **消えた登録を指したままでも同梱に落ちるだけにすること**（エラーにしない）。
// 設定ファイルは手で編集する前提でもあるので、**指し先が無いだけで盤が
// 描けなくなるのは行き過ぎ。**
func (c Config) CurrentPieceFont() (PieceFontEntry, bool) {
	if c.PieceFont == "" {
		return PieceFontEntry{}, false
	}
	for _, e := range c.PieceFonts {
		if e.ID == c.PieceFont {
			return e, true
		}
	}
	return PieceFontEntry{}, false
}

// NextPieceFontID は既存の一覧とぶつからない ID を作る。
func NextPieceFontID(fonts []PieceFontEntry) string {
	used := make(map[string]bool, len(fonts))
	for _, f := range fonts {
		used[f.ID] = true
	}
	for i := 1; ; i++ {
		id := fmt.Sprintf("font-%d", i)
		if !used[id] {
			return id
		}
	}
}

// EngineEntry は登録した USI エンジン 1 つ。
//
// **繋ぎ先は「USI を話すプロセス」なら何でもよい**（やねうら王・水匠・prokishi.exe）。
// 検討ツールとして実用になるかは繋ぐエンジンの棋力で決まるので、そこを差し替え
// られるようにしてある（`_docs/phase4-engine-usi.md`）。
type EngineEntry struct {
	// ID は一覧の中でこのエンジンを指す識別子。**設定ファイルの中だけで通じればよい。**
	//
	// パスを鍵にしないのは、**同じ実行ファイルを別の option で 2 つ登録する**のが
	// 正当な使い方だから（置換表やスレッド数を変えて比べる）。
	ID string `json:"id"`

	// Name は画面に出す名前。空ならパスのファイル名（同梱なら「同梱エンジン」）。
	//
	// ⚠️ **エンジンが `id name` で名乗る名前とは別物。** あちらは繋いで初めて分かるので、
	// 繋いでいないあいだの表示と、同じ exe を 2 つ登録したときの区別にこちらが要る。
	Name string `json:"name,omitempty"`
	// EngineName はエンジンが `id name` で名乗った名前（繋いで初めて分かる）。
	//
	// **`Name` を付けていないときの既定の表示名がこれ**（`DisplayName`）。
	// exe のファイル名（`YaneuraOu_NNUE-tournament-clang++-avx2.exe`）より、
	// エンジン自身の名乗りのほうが読める。
	//
	// ⚠️ **`Name` を上書きしないこと。** 人が付けた名前は「そう呼びたくて
	// 付けたもの」で、**同じ exe を option 違いで 2 つ登録したときの区別**でもある
	// （名乗りは同じになるので、これで潰すと見分けが付かなくなる）。
	//
	// ⚠️ **控えるのは繋いだとき**（`AnalyzeService` が `CheckEngine` と解析の
	// 完了で書く）。**保存の操作では繋がない**という線引きは変えていない。
	//
	// ⚠️ **実行ファイルを差し替えたら捨てること**（別のエンジンの名乗りなので、
	// 残すと**違うエンジンの名前を出す**。`OptionSpecs` と同じ扱い）。
	EngineName string `json:"engineName,omitempty"`

	// Path は USI エンジンの実行ファイル。
	//
	// **空なら同梱のエンジンを使う。** 「外部エンジンを使うかどうか」の真偽値は
	// 別に持たない —— 2 つ持つと、パスが入っているのに無効、という食い違いが起きる。
	Path string `json:"path,omitempty"`

	// Options は接続時に `setoption` で送る値（option 名 → 値）。
	//
	// **画面から編集できる**（2026-08-15。設定タブのエンジンの行）。入力欄の形は
	// `OptionSpecs` に控えた宣言（型・既定値・範囲・選択肢）から組み立てる。
	// ⚠️ **設定ファイルを手で編集する経路も残す** —— 宣言に無い option を受け付ける
	// エンジンがあるので、**画面から選べる範囲と、受け付ける範囲は別物**
	// （折れ線の色と同じ扱い）。
	//
	// 例: `{"USI_Hash": "1024", "Threads": "4", "EvalDir": "eval"}`
	//
	// ⚠️ **既定値と同じ値は書き残さない**（`SettingsService.SetEngineOption` が消す）。
	// エンジンが宣言した option には、ここに書いていなくても既定値が送られる
	// （`core/usi/client.plannedOptions`）ので、**書き残すと「エンジンの既定に従う」
	// という指定ができなくなる**（バージョンが上がって既定が変わっても古い値で固まる）。
	//
	// ⚠️ **`isready` の前に送られる**（置換表の確保や評価関数の読み込みに間に合わせるため。
	// `core/usi/client.Open` の注記）。探索ごとに変えるもの（MultiPV）はここではない。
	Options map[string]string `json:"options,omitempty"`

	// OptionSpecs はエンジンが `usi` の応答で宣言した option（宣言順の控え）。
	//
	// **「接続を確認」で繋いだときに書き込む**（`AnalyzeService.CheckEngine`）。
	// ⚠️ **控えておくのが要点** —— 宣言はエンジンに繋がないと分からないので、
	// これが無いと**設定タブを開くたびにエンジンを起こす**ことになる
	// （NNUE の読み込みで数秒かかるものがある）。
	//
	// ⚠️ **これは「今の値」ではない**（値は `Options`）。宣言そのものなので、
	// **入力欄の作り方（型・範囲・選択肢）と、既定値に戻す先**がここから決まる。
	//
	// ⚠️ **実行ファイルを差し替えたら捨てる**（別のエンジンの宣言なので）。
	// **`Options` のほうは捨てない** —— 置き場所を移しただけのことがあるうえ、
	// 人が書いた値を黙って消さない（宣言に無い値は画面でもそう出す）。
	OptionSpecs []EngineOption `json:"optionSpecs,omitempty"`

	// Enabled は解析のときに使うか。**外した登録は消さずに残る**
	// （エンジンを入れ替えて比べる作業では、外したものをまた戻すことが多い）。
	//
	// omitempty を付けないのは、**外してあること自体を設定ファイルに残す**ため。
	Enabled bool `json:"enabled"`

	// Mate は**詰将棋エンジン**か（`go mate` で詰みを解かせる相手。2026-09-12）。
	//
	// ⚠️ **通常の解析には使わない。** 詰将棋エンジンは通常の `go` に答えないことが
	// あり（KomoringHeights は `bestmove resign` を返す。実測）、**混ぜると毎回
	// 「投了」が出る**。`EnabledEngines` から外し、`MateEngine` が拾う。
	//
	// ⚠️ **逆に、通常のエンジンを詰将棋用にしても解けない** —— やねうら王系は
	// **攻方の玉が無いと `go mate` にも答えない**（実測）。詰将棋エンジンは
	// 攻方の玉が無い局面を前提にしているものを選ぶこと（KomoringHeights など）。
	Mate bool `json:"mate,omitempty"`

	// Color は評価値グラフの折れ線の色（`#rrggbb`）。
	//
	// **エンジンが色を持つ**（2026-08-14。以前は「一覧の何番目か」で決まっていた）。
	// 複数のエンジンを並べて読むのがこの一覧の目的なので、**どの線がどのエンジンか**は
	// 見た目で覚えるもの。並べ替えたり 1 つ外したりするたびに色が入れ替わると、
	// **前に見ていた線と同じ色が別のエンジンを指す**ことになる。
	//
	// **空なら登録順の既定色**（`DefaultEngineColor`）。⚠️ **既定の解決を
	// 呼び出し側に書かないこと**（`DisplayName` と同じ）。
	Color string `json:"color,omitempty"`
}

// EngineOption はエンジンが `usi` の応答で宣言した option 1 つ。
//
// **`core/usi.Option` を写したもの。** ⚠️ **あちらを直接 config に埋めない** ——
// これは**設定ファイルに書き出す形**（JSON のキーが決まる）で、プロトコルの
// 語彙とは寿命が違う。写す場所は `_cmd/ikkyoku` の 1 か所だけ。
type EngineOption struct {
	// Name は option 名。**空白を含みうる**（"Book File" など）。
	Name string `json:"name"`
	// Type は "check" / "spin" / "combo" / "button" / "string" / "filename"。
	//
	// ⚠️ **button は値を持たない**（送ること自体が「押した」という動作）。
	// 設定できる対象ではないので、値を書き込まないこと。
	Type string `json:"type"`
	// Default はエンジンが宣言した既定値。**「値を消したときに戻る先」。**
	Default string `json:"default,omitempty"`
	// Min / Max は spin の範囲（Has* が false なら宣言が無かった）。
	Min    int  `json:"min,omitempty"`
	Max    int  `json:"max,omitempty"`
	HasMin bool `json:"hasMin,omitempty"`
	HasMax bool `json:"hasMax,omitempty"`
	// Vars は combo の選択肢。
	Vars []string `json:"vars,omitempty"`
}

// IsButton は押すだけの option か（値を持たない）。
func (o EngineOption) IsButton() bool { return o.Type == "button" }

// OptionSpec は宣言を名前で 1 つ引く。
func (e EngineEntry) OptionSpec(name string) (EngineOption, bool) {
	for _, o := range e.OptionSpecs {
		if o.Name == name {
			return o, true
		}
	}
	return EngineOption{}, false
}

// OptionValue は option の今の値と、それを人が決めたかを返す。
//
// **設定に無ければ宣言された既定値**（＝エンジンに送られるのもその値。
// `core/usi/client.plannedOptions` が宣言に既定値を送るため）。
// ⚠️ **「空なら既定」の解決を呼び出し側に書かないこと**（`DisplayName` と同じ）。
func (e EngineEntry) OptionValue(o EngineOption) (value string, custom bool) {
	if v, ok := e.Options[o.Name]; ok {
		return v, true
	}
	return o.Default, false
}

// 認識器の読み込み元（`Config.SutemeSource`）。**文字列を直に書かないこと** ——
// 設定ファイル・Go・フロントの 3 か所に散ると綴りの食い違いに気づけない。
const (
	// SutemeSourceAuto は「指定があればディレクトリ、無ければ焼き込み」（既定）。
	SutemeSourceAuto = "auto"
	// SutemeSourceDir はディレクトリ（`SutemeDataDir`）から読む。
	SutemeSourceDir = "dir"
	// SutemeSourceEmbed はバイナリに焼き込んだものを読む。
	SutemeSourceEmbed = "embed"
)

// SutemeSourceOr は認識器の読み込み元を正規化して返す。
//
// **空・未知の値は auto** として扱う（設定ファイルは手で編集する前提なので、
// 綴り間違いでアプリが認識できなくなるより既定へ倒す）。
// ⚠️ **これが「焼き込みが在るか」までは見ない。** 実際にどちらから読むかの解決は
// `recognize.EmbeddedAvailable` を見る側（`_cmd/ikkyoku` の `loadRecognizer`）の仕事。
func (c Config) SutemeSourceOr() string {
	switch c.SutemeSource {
	case SutemeSourceDir, SutemeSourceEmbed:
		return c.SutemeSource
	default:
		return SutemeSourceAuto
	}
}

// DefaultAnalyzeSeconds は「考える秒数」の既定（解析タブ）。
//
// ⚠️ **短すぎず、待たされすぎない線。** 連続解析では「手数 × 秒数」がそのまま
// 所要時間になるので（150 手なら 3 秒で 7 分半）、既定を伸ばすと**通しで解析する
// のが現実的でなくなる**。
const DefaultAnalyzeSeconds = 3

// ThinkSeconds は「考える秒数」を返す（**0 は無制限**）。
//
// ⚠️ **「未設定なら既定」の解決はここ 1 か所。** 呼び出し側にもフロントにも
// 書かないこと（既定を変えたときに食い違う。`DisplayName` / 折れ線の色と同じ）。
func (c Config) ThinkSeconds() int {
	if c.AnalyzeSeconds == nil {
		return DefaultAnalyzeSeconds
	}
	if *c.AnalyzeSeconds < 0 {
		// 手で書き換えて壊れている。**無制限として扱う**（解析できなくしない）。
		return 0
	}
	return *c.AnalyzeSeconds
}

// MultiPVOption は候補手の本数を決める USI option の名前。
//
// **将棋 UI と USI の共通の語彙**（エンジン側もこの名前で宣言する）。
const MultiPVOption = "MultiPV"

// DefaultMultiPV は候補手の本数の既定（**エンジンの宣言より優先する**）。
//
// ⚠️ **1 に戻さないこと**（2026-08-12 に 1 から 3 へ変えた）。「次善手を選んだら
// どう転ぶか」を辿るのが構想の中心なので、**最善手だけが出ている状態を既定にしない。**
// エンジンの宣言はたいてい 1 だが、**このアプリの既定はこちら**。
const DefaultMultiPV = 3

// MultiPV は候補手の本数を返す（設定に無ければ `DefaultMultiPV`）。
//
// ⚠️ **置き場所は `Options` の中**（`setoption name MultiPV`）。別の欄を作らないのは、
// **エンジンが宣言している option そのもの**だから —— 2 か所に持つと、設定タブで
// 書いた値と解析タブで選んだ値が食い違う。
//
// ⚠️ **これは探索ごとに送る option。** `isready` の前にしか効かないものと違って
// **繋ぎ直しが要らない**ので、`AnalyzeService.engineKey`（接続の指紋）からは
// 外してある。**指紋に入れると、本数を変えるたびにエンジンを起こし直すことになる。**
func (e EngineEntry) MultiPV() int {
	v, ok := e.Options[MultiPVOption]
	if !ok {
		return DefaultMultiPV
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 {
		// 手で書き換えて壊れている。**既定に倒す**（解析できなくしない）。
		return DefaultMultiPV
	}
	return n
}

// EngineConfig は**旧形式**の単一エンジン設定（`Config.Engine`）。
//
// ⚠️ **移行のためだけに残してある。** 今の設定は `Config.Engines`。
type EngineConfig struct {
	Path    string            `json:"path,omitempty"`
	Options map[string]string `json:"options,omitempty"`
}

// BuiltinEngineName は同梱エンジンの表示名（パスが空のエントリ）。
const BuiltinEngineName = "同梱エンジン"

// EngineColorOption は選べる折れ線の色 1 つ（画面の色見本）。
type EngineColorOption struct {
	// Value は `#rrggbb`（設定ファイルにもこの形で入る）。
	Value string `json:"value"`
	// Label は色の名前（画面に出す）。
	Label string `json:"label"`
}

// EngineColors は選べる色の一覧。**暗い地（#1b1d23）の上で読めるものだけ。**
//
// ⚠️ **評価値の色（青＝先手 `#6ad3ff` / 橙＝後手 `#ffc46b`）を先頭に置かないこと。**
// 折れ線の色は「どのエンジンか」であって形勢ではないので、形勢の色と紛れる並びにしない。
//
// ⚠️ **設定ファイルはこの一覧の外の色も受け付ける**（手で編集する前提。`#rrggbb` なら通る）。
// ここにあるのは**画面から選べるもの**で、色そのものの制限ではない。
var EngineColors = []EngineColorOption{
	{Value: "#7ddc8a", Label: "緑"},
	{Value: "#e0a3ff", Label: "紫"},
	{Value: "#ffd166", Label: "黄"},
	{Value: "#8ecae6", Label: "青"},
	{Value: "#ff8fa3", Label: "桃"},
	{Value: "#f2a25c", Label: "橙"},
	{Value: "#6fd3c7", Label: "青緑"},
	{Value: "#b8c0d0", Label: "灰"},
}

// DefaultEngineColor は登録順 i のエンジンの既定色を返す（一覧を超えたら回す）。
func DefaultEngineColor(i int) string {
	if i < 0 {
		i = 0
	}
	return EngineColors[i%len(EngineColors)].Value
}

// DisplayColor は画面に出す色を返す（Color が空なら登録順の既定色）。
//
// i は**一覧の中での位置**。⚠️ **「解析に使う」を外した登録も数に入れること** ——
// 詰めて数えると、チェックを外した瞬間に他のエンジンの既定色が入れ替わる。
func (e EngineEntry) DisplayColor(i int) string {
	if e.Color != "" {
		return e.Color
	}
	return DefaultEngineColor(i)
}

// DisplayName は画面に出す名前を返す。
//
// 人が付けた名前 → **エンジンが名乗った名前** → exe のファイル名 → 「同梱エンジン」。
//
// ⚠️ **名乗りを人が付けた名前より前に出さないこと。** 同じ exe を option 違いで
// 2 つ登録すると名乗りは同じになるので、**見分けが付くのは人が付けた名前だけ**。
//
// ⚠️ **「空なら既定」の解決をここ以外に書かないこと**（`ThinkSeconds` /
// 折れ線の色と同じ約束。2 か所に持つと既定を変えたときに食い違う）。
func (e EngineEntry) DisplayName() string {
	if e.Name != "" {
		return e.Name
	}
	if e.EngineName != "" {
		return e.EngineName
	}
	if e.Path == "" {
		return BuiltinEngineName
	}
	return filepath.Base(e.Path)
}

// EngineList は登録済みのエンジン一覧を返す。
//
// ⚠️ **空なら同梱エンジン 1 つを返す。** 設定ファイルを作っていない状態でも
// 解析できる、という既定の挙動をここで担保している。**呼び出し側が
// 「空だったら同梱」を書かないこと**（2 か所に散る）。
func (c Config) EngineList() []EngineEntry {
	if len(c.Engines) == 0 {
		return []EngineEntry{{ID: DefaultEngineID, Enabled: true}}
	}
	out := make([]EngineEntry, len(c.Engines))
	copy(out, c.Engines)
	return out
}

// EnabledEngines は解析に使うエンジンだけを返す（順番は登録順）。
//
// ⚠️ **詰将棋エンジンは含まない**（2026-09-12）。あちらは通常の `go` に答えない
// ことがあるので、混ぜると**評価値の代わりに「投了」が並ぶ**。拾うのは `MateEngine`。
func (c Config) EnabledEngines() []EngineEntry {
	var out []EngineEntry
	for _, e := range c.EngineList() {
		if e.Enabled && !e.Mate {
			out = append(out, e)
		}
	}
	return out
}

// MateEngine は詰み探索に使うエンジンを返す（**登録順で最初の 1 つ**）。
//
// ⚠️ **無ければ ok=false。** 詰将棋エンジンは同梱していないので、
// **入れていない環境が普通**。呼び出し側は「入れてください」と言うだけにして、
// **通常の解析を巻き込まないこと**（設計原則3）。
//
// ⚠️ **`Enabled` も見る**（一覧で外してあるものは使わない）。
func (c Config) MateEngine() (EngineEntry, bool) {
	for _, e := range c.EngineList() {
		if e.Enabled && e.Mate {
			return e, true
		}
	}
	return EngineEntry{}, false
}

// DefaultEngineID は同梱エンジンを既定で登録したときの ID。
const DefaultEngineID = "builtin"

// NextEngineID は既存の一覧とぶつからない ID を作る。
func NextEngineID(engines []EngineEntry) string {
	used := make(map[string]bool, len(engines))
	for _, e := range engines {
		used[e.ID] = true
	}
	for i := 1; ; i++ {
		id := fmt.Sprintf("engine-%d", i)
		if !used[id] {
			return id
		}
	}
}

// migrateEngines は旧形式（`engine`）の設定を `engines` へ移す。
//
// **読み込みの一度きり。** 移したら旧欄は捨てるので、次に保存した時点で
// ファイルからも消える。`engines` が既にあれば旧欄は無視する（手で両方書いた
// ときに、新しいほうを正とする）。
func migrateEngines(c *Config) {
	old := c.Engine
	c.Engine = nil
	if old == nil || len(c.Engines) > 0 {
		return
	}
	if old.Path == "" && len(old.Options) == 0 {
		return
	}
	c.Engines = []EngineEntry{{
		ID:      NextEngineID(nil),
		Path:    old.Path,
		Options: old.Options,
		Enabled: true,
	}}
}

// TrainingConfig は訂正済みの局面を suteme に登録するための接続設定。
//
// **既定は無効。** 訂正結果の還元は 2026-08-07 に決めた方針だが、
// **自動では送らない**（人間が直したのは 1 マスで残り 80 マスは推論結果のまま、
// という「自分の出力を正解として食う」形になるため）。設定で有効にしたうえで、
// 局面ごとにボタンを押したときだけ送る。
type TrainingConfig struct {
	// Enabled は「訂正盤面を suteme に登録する」を使うか。
	// **これは送信ボタンを出すかどうかであって、自動送信のスイッチではない。**
	//
	// omitempty を付けないのは FitOnStartup と同じ理由（切ってあること自体を残す）。
	Enabled bool `json:"enabled"`
	// Host は suteme の学習用サーバのホスト。空なら 127.0.0.1。
	Host string `json:"host,omitempty"`
	// Port は同ポート。0 なら 8080（suteme-training の既定）。
	Port int `json:"port,omitempty"`
	// Token は Bearer トークン。**同じマシンで動かすなら不要**
	// （suteme はループバックからのアクセスを認証免除にしている）。
	// 別のマシンへ送るときだけ、suteme の APIタブで発行したものを入れる。
	Token string `json:"token,omitempty"`
}

// DefaultKifuDBPath は既定の棋譜データベースのパスを返す
// （os.UserConfigDir()/ikkyoku/kicho.db）。
//
// ⚠️ **kicho アプリの既定（os.UserConfigDir()/kicho/kicho.db）とは別**にしてある。
// 同じ SQLite ファイルを 2 つのプロセスから書くと `database is locked` に
// なりうるので、**共用はユーザーが設定で指定したときだけ**にする。
func DefaultKifuDBPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("ikkyoku: 設定ディレクトリの取得に失敗しました: %w", err)
	}
	return filepath.Join(dir, "ikkyoku", "kicho.db"), nil
}

// KifuDB は実際に開く棋譜データベースのパスを返す。
//
// ⚠️ **「空なら既定」の解決はここ 1 か所。** 呼び出し側にもフロントにも
// 書かないこと（`ThinkSeconds` / `DisplayName` / 折れ線の色と同じ約束）。
func (c Config) KifuDB() (string, error) {
	if p := strings.TrimSpace(c.KifuDBPath); p != "" {
		return p, nil
	}
	return DefaultKifuDBPath()
}

// DefaultConfigPath は既定の設定ファイルパスを返す（os.UserConfigDir()/ikkyoku/config.json）。
func DefaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("ikkyoku: 設定ディレクトリの取得に失敗しました: %w", err)
	}
	return filepath.Join(dir, "ikkyoku", "config.json"), nil
}

// LoadConfig は path から設定を読み込む。ファイルが存在しない場合はゼロ値の Config を
// エラー無しで返す（初回起動時に設定ファイルが無いのは正常な状態のため）。
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("ikkyoku: 設定の読み込みに失敗しました: %w", err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("ikkyoku: 設定の解析に失敗しました: %w", err)
	}
	// 旧形式（単一エンジン）をここで吸収する。**読み込みの入口 1 か所だけ**で行い、
	// これより上のコードは新しい形（Engines）しか知らない。
	migrateEngines(&c)
	return c, nil
}

// SaveConfig は設定を path に保存する。親ディレクトリが無ければ作成する。
func SaveConfig(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ikkyoku: 設定ディレクトリの作成に失敗しました: %w", err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("ikkyoku: 設定のエンコードに失敗しました: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("ikkyoku: 設定の書き込みに失敗しました: %w", err)
	}
	return nil
}

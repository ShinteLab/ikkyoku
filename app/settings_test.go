package app

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	ikkyoku "github.com/ShinteLab/ikkyoku"
)

// newTestSettings は保存先を一時ディレクトリに向けた SettingsService を作る。
//
// **NewSettingsService は使えない**（あちらは os.UserConfigDir を見るので、
// テストが本物の設定ファイルを書き換えてしまう）。
//
// ⚠️ **ファイルにも書いておくこと。** `save` は**保存の前にファイルを読み直す**ので、
// メモリ上の cfg だけ用意しても書き換えの土台にならない（登録が空とみなされ、
// 同梱エンジン 1 件に実体化される）。
func newTestSettings(t *testing.T, engines []ikkyoku.EngineEntry) *SettingsService {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := ikkyoku.Config{Engines: engines}
	if err := ikkyoku.SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	return &SettingsService{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		path:   path,
		cfg:    cfg,
	}
}

func engineIDs(s AppSettings) []string {
	ids := make([]string, 0, len(s.Engines))
	for _, e := range s.Engines {
		ids = append(ids, e.ID)
	}
	return ids
}

// TestMoveEngine は**エンジンの並べ替え**を固定する。
//
// 並び順は表示の順序そのもの（解析タブのエンジンごとの結果・評価値グラフの折れ線・
// 勝率バーが最初に出すエンジン）なので、**動かした結果が保存されること**と、
// ⚠️ **端で回り込まないこと**（一番上を上げたら一番下へ飛ぶ、では驚く）を見る。
func TestMoveEngine(t *testing.T) {
	list := []ikkyoku.EngineEntry{
		{ID: "a", Enabled: true},
		{ID: "b", Enabled: false},
		{ID: "c", Enabled: true},
	}

	t.Run("下へ動かす", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		got, err := s.MoveEngine("a", 1)
		if err != nil {
			t.Fatalf("MoveEngine: %v", err)
		}
		want := []string{"b", "a", "c"}
		if ids := engineIDs(got); !equalStrings(ids, want) {
			t.Errorf("並び = %v, want %v", ids, want)
		}
		// 保存されていること（読み直しても同じ順であること）。
		cfg, err := ikkyoku.LoadConfig(s.path)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if len(cfg.Engines) != 3 || cfg.Engines[0].ID != "b" || cfg.Engines[1].ID != "a" {
			t.Errorf("保存された並び = %v", cfg.Engines)
		}
	})

	t.Run("上へ動かす", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		got, _ := s.MoveEngine("c", -1)
		want := []string{"a", "c", "b"}
		if ids := engineIDs(got); !equalStrings(ids, want) {
			t.Errorf("並び = %v, want %v", ids, want)
		}
	})

	// ⚠️ **端では動かない。回り込ませない。**
	t.Run("端では動かない", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		got, _ := s.MoveEngine("a", -1)
		want := []string{"a", "b", "c"}
		if ids := engineIDs(got); !equalStrings(ids, want) {
			t.Errorf("先頭を上へ: 並び = %v, want %v", ids, want)
		}
		got, _ = s.MoveEngine("c", 1)
		if ids := engineIDs(got); !equalStrings(ids, want) {
			t.Errorf("末尾を下へ: 並び = %v, want %v", ids, want)
		}
	})

	// ⚠️ **「解析に使う」を外した登録も数に入れる。** 走るものだけを詰めて数えると、
	// チェックを外した瞬間に見えている順番と食い違う。
	t.Run("解析に使わない登録も飛ばさない", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		got, _ := s.MoveEngine("c", -1) // b（無効）と入れ替わる
		want := []string{"a", "c", "b"}
		if ids := engineIDs(got); !equalStrings(ids, want) {
			t.Errorf("並び = %v, want %v", ids, want)
		}
	})

	t.Run("知らない ID では何も変えない", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		got, _ := s.MoveEngine("zzz", 1)
		want := []string{"a", "b", "c"}
		if ids := engineIDs(got); !equalStrings(ids, want) {
			t.Errorf("並び = %v, want %v", ids, want)
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSetEngineColor は**エンジンの色**を固定する（2026-08-14）。
//
// 色は**エンジンの登録に紐づく**（並び順ではない）。複数のエンジンを並べて読むのが
// この一覧の目的なので、**どの線がどのエンジンか**は見た目で覚えるもの。
// ⚠️ **並べ替えたり 1 つ外したりして色が入れ替わると、前に見ていた線と同じ色が
// 別のエンジンを指す**ことになるので、そこを見ている。
func TestSetEngineColor(t *testing.T) {
	list := []ikkyoku.EngineEntry{
		{ID: "a", Enabled: true},
		{ID: "b", Enabled: true},
	}

	t.Run("色が保存され、返る設定にも入る", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		got, err := s.SetEngineColor("b", "#FF8FA3") // 大文字でも受ける
		if err != nil {
			t.Fatalf("SetEngineColor: %v", err)
		}
		if got.Engines[1].Color != "#ff8fa3" {
			t.Errorf("色 = %q, want #ff8fa3", got.Engines[1].Color)
		}
		cfg, err := ikkyoku.LoadConfig(s.path)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.Engines[1].Color != "#ff8fa3" {
			t.Errorf("保存された色 = %q", cfg.Engines[1].Color)
		}
	})

	// ⚠️ **未設定なら登録順の既定色**（解決するのは Go 側。フロントに書かない）。
	t.Run("未設定は登録順の既定色", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		got := s.Settings()
		for i, e := range got.Engines {
			if want := ikkyoku.DefaultEngineColor(i); e.Color != want {
				t.Errorf("engines[%d].Color = %q, want %q", i, e.Color, want)
			}
		}
	})

	// ⚠️ **並べ替えても、色を付けたエンジンの色は動かない。**
	t.Run("並べ替えても色は付いてくる", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		if _, err := s.SetEngineColor("a", "#6fd3c7"); err != nil {
			t.Fatalf("SetEngineColor: %v", err)
		}
		got, err := s.MoveEngine("a", 1)
		if err != nil {
			t.Fatalf("MoveEngine: %v", err)
		}
		if got.Engines[1].ID != "a" || got.Engines[1].Color != "#6fd3c7" {
			t.Errorf("並べ替え後 = %+v", got.Engines[1])
		}
	})

	t.Run("空にすると既定へ戻る", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		if _, err := s.SetEngineColor("a", "#6fd3c7"); err != nil {
			t.Fatalf("SetEngineColor: %v", err)
		}
		got, err := s.SetEngineColor("a", "")
		if err != nil {
			t.Fatalf("SetEngineColor(空): %v", err)
		}
		if want := ikkyoku.DefaultEngineColor(0); got.Engines[0].Color != want {
			t.Errorf("色 = %q, want %q（既定）", got.Engines[0].Color, want)
		}
	})

	// 壊れた値は断る。**そのとき今の設定は変えない**（画面が食い違ったままにならない）。
	t.Run("色の形が違えば断る", func(t *testing.T) {
		s := newTestSettings(t, append([]ikkyoku.EngineEntry(nil), list...))
		got, err := s.SetEngineColor("a", "赤")
		if err == nil {
			t.Fatal("エラーになるべき")
		}
		if want := ikkyoku.DefaultEngineColor(0); got.Engines[0].Color != want {
			t.Errorf("断ったのに色が変わった: %q", got.Engines[0].Color)
		}
	})
}

// engineWithSpecs は option を宣言済みのエンジン 1 件（「接続を確認」を通った状態）。
func engineWithSpecs() []ikkyoku.EngineEntry {
	return []ikkyoku.EngineEntry{{
		ID: "a", Path: "C:/shogi/YaneuraOu.exe", Enabled: true,
		OptionSpecs: []ikkyoku.EngineOption{
			{Name: "USI_Hash", Type: "spin", Default: "256", Min: 1, Max: 4096, HasMin: true, HasMax: true},
			{Name: "USI_Ponder", Type: "check", Default: "false"},
			{Name: "EvalDir", Type: "string", Default: "eval"},
			{Name: "BookMoves", Type: "combo", Default: "no_book", Vars: []string{"no_book", "standard_book"}},
			{Name: "Clear Hash", Type: "button"},
		},
	}}
}

func optionOf(s AppSettings, i int, name string) (EngineOptionSettings, bool) {
	for _, o := range s.Engines[i].Options {
		if o.Name == name {
			return o, true
		}
	}
	return EngineOptionSettings{}, false
}

// TestSetEngineOption は**エンジンの設定値**を固定する（2026-08-15）。
//
// 送るのは `engines[].options`（`isready` の前の `setoption`）で、⚠️ **応答が
// 返らないので、間違った値を送っても画面では気づけない**。だから入れた時点で断る、
// というのがここの要点。
func TestSetEngineOption(t *testing.T) {
	t.Run("値が保存され、返る設定にも入る", func(t *testing.T) {
		s := newTestSettings(t, engineWithSpecs())
		got, err := s.SetEngineOption("a", "USI_Hash", "1024")
		if err != nil {
			t.Fatalf("SetEngineOption: %v", err)
		}
		o, ok := optionOf(got, 0, "USI_Hash")
		if !ok || o.Value != "1024" || !o.Custom {
			t.Errorf("USI_Hash = %+v, want 1024（人が決めた値）", o)
		}
		cfg, err := ikkyoku.LoadConfig(s.path)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.Engines[0].Options["USI_Hash"] != "1024" {
			t.Errorf("保存された値 = %+v", cfg.Engines[0].Options)
		}
	})

	// ⚠️ **既定と同じ値は書き残さない。** 書き残すと「エンジンの既定に従う」という
	// 指定ができなくなり、**バージョンが上がって既定が変わっても古い値で固まる**。
	t.Run("既定と同じ値は書き残さない", func(t *testing.T) {
		s := newTestSettings(t, engineWithSpecs())
		got, err := s.SetEngineOption("a", "USI_Hash", "256")
		if err != nil {
			t.Fatalf("SetEngineOption: %v", err)
		}
		o, _ := optionOf(got, 0, "USI_Hash")
		if o.Value != "256" || o.Custom {
			t.Errorf("USI_Hash = %+v, want 既定のまま（custom=false）", o)
		}
		cfg, _ := ikkyoku.LoadConfig(s.path)
		if _, ok := cfg.Engines[0].Options["USI_Hash"]; ok {
			t.Errorf("既定と同じ値が書き残された: %+v", cfg.Engines[0].Options)
		}
	})

	// 空にすると設定から消えて、宣言された既定値に戻る（＝送られるのも既定値）。
	t.Run("空にすると既定へ戻る", func(t *testing.T) {
		s := newTestSettings(t, engineWithSpecs())
		if _, err := s.SetEngineOption("a", "EvalDir", "eval_nnue"); err != nil {
			t.Fatalf("SetEngineOption: %v", err)
		}
		got, err := s.SetEngineOption("a", "EvalDir", "")
		if err != nil {
			t.Fatalf("SetEngineOption(空): %v", err)
		}
		o, _ := optionOf(got, 0, "EvalDir")
		if o.Value != "eval" || o.Custom {
			t.Errorf("EvalDir = %+v, want 既定の eval", o)
		}
	})

	// 型ごとの検分。**エンジンが必ず断る値だけ**を弾く。
	t.Run("型に合わない値は断る", func(t *testing.T) {
		for _, tt := range []struct{ name, value string }{
			{"USI_Hash", "たくさん"}, // spin に数字でない
			{"USI_Hash", "0"},    // min 未満
			{"USI_Hash", "9999"}, // max 超え
			{"USI_Ponder", "はい"}, // check に true/false 以外
			{"BookMoves", "my_book"},
			{"Clear Hash", "1"}, // button は値を持たない
		} {
			s := newTestSettings(t, engineWithSpecs())
			got, err := s.SetEngineOption("a", tt.name, tt.value)
			if err == nil {
				t.Errorf("%s = %q: エラーになるべき", tt.name, tt.value)
			}
			// ⚠️ **断ったときに今の設定を変えないこと**（画面が食い違ったままになる）。
			if o, _ := optionOf(got, 0, tt.name); o.Custom {
				t.Errorf("%s = %q: 断ったのに値が入った", tt.name, tt.value)
			}
		}
	})

	// ⚠️ **宣言に無い名前も受ける。** 宣言していない option を受け付けるエンジンが
	// あり、設定ファイルを手で編集する経路も残してある（値はそのまま送られる）。
	t.Run("宣言に無い名前も受ける", func(t *testing.T) {
		s := newTestSettings(t, engineWithSpecs())
		got, err := s.SetEngineOption("a", "SomeHiddenOption", "42")
		if err != nil {
			t.Fatalf("SetEngineOption: %v", err)
		}
		o, ok := optionOf(got, 0, "SomeHiddenOption")
		if !ok || o.Value != "42" || o.Known {
			t.Errorf("SomeHiddenOption = %+v, want 値 42・known=false", o)
		}
	})

	t.Run("知らないエンジンでは断る", func(t *testing.T) {
		s := newTestSettings(t, engineWithSpecs())
		if _, err := s.SetEngineOption("zzz", "USI_Hash", "512"); err == nil {
			t.Error("エラーになるべき")
		}
	})
}

// TestEngineOptionSettings は**画面に出す形**を固定する。
//
// ⚠️ 見ているのは 2 つ: **宣言の順を並べ替えないこと**（前の option が後の option の
// 意味を変えるエンジンがある）と、**「まだ繋いでいない」と「宣言が 1 つも無い」を
// 区別できること**（`OptionsKnown`。画面に出す文言が違う）。
func TestEngineOptionSettings(t *testing.T) {
	s := newTestSettings(t, engineWithSpecs())
	got := s.Settings()
	if !got.Engines[0].OptionsKnown {
		t.Error("OptionsKnown = false, want true（宣言を控えてある）")
	}
	want := []string{"USI_Hash", "USI_Ponder", "EvalDir", "BookMoves", "Clear Hash"}
	names := make([]string, 0, len(got.Engines[0].Options))
	for _, o := range got.Engines[0].Options {
		names = append(names, o.Name)
	}
	if !equalStrings(names, want) {
		t.Errorf("並び = %v, want %v（宣言順）", names, want)
	}

	// 繋いでいない登録は「未取得」。**宣言が無いのと同じに見せないこと。**
	s2 := newTestSettings(t, []ikkyoku.EngineEntry{{ID: "a", Enabled: true}})
	if s2.Settings().Engines[0].OptionsKnown {
		t.Error("繋いでいない登録が OptionsKnown = true になっている")
	}
}

// TestResetEngineOptions は「全部を既定に戻す」を固定する。
//
// ⚠️ **宣言（OptionSpecs）は捨てない** —— 捨てると入力欄が作れなくなり、
// 戻す先そのものが画面から消える。
func TestResetEngineOptions(t *testing.T) {
	s := newTestSettings(t, engineWithSpecs())
	if _, err := s.SetEngineOption("a", "USI_Hash", "1024"); err != nil {
		t.Fatalf("SetEngineOption: %v", err)
	}
	got, err := s.ResetEngineOptions("a")
	if err != nil {
		t.Fatalf("ResetEngineOptions: %v", err)
	}
	if got.Engines[0].OptionCount != 0 {
		t.Errorf("OptionCount = %d, want 0", got.Engines[0].OptionCount)
	}
	if !got.Engines[0].OptionsKnown {
		t.Error("宣言まで捨てている（入力欄が作れなくなる）")
	}
	if o, _ := optionOf(got, 0, "USI_Hash"); o.Value != "256" || o.Custom {
		t.Errorf("USI_Hash = %+v, want 既定の 256", o)
	}
}

// TestSetEnginePathDropsOptionSpecs は**実行ファイルを差し替えたら宣言を捨てる**
// ことを固定する。
//
// 別のエンジンの宣言をそのまま出すと、**違うエンジンの入力欄**を出すことになる。
// ⚠️ **値のほうは捨てない**（置き場所を移しただけのことがあり、人が書いた値を
// 黙って消さない）。
func TestSetEnginePathDropsOptionSpecs(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "other.exe")
	if err := os.WriteFile(other, []byte("dummy"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	s := newTestSettings(t, engineWithSpecs())
	if _, err := s.SetEngineOption("a", "USI_Hash", "1024"); err != nil {
		t.Fatalf("SetEngineOption: %v", err)
	}
	got, err := s.SetEnginePath("a", other)
	if err != nil {
		t.Fatalf("SetEnginePath: %v", err)
	}
	if got.Engines[0].OptionsKnown {
		t.Error("実行ファイルを差し替えたのに、前のエンジンの宣言が残っている")
	}
	o, ok := optionOf(got, 0, "USI_Hash")
	if !ok || o.Value != "1024" || o.Known {
		t.Errorf("USI_Hash = %+v, want 値は残り、宣言に無い扱い", o)
	}
}

// TestSetEngineMultiPV は**候補手の本数がエンジンごと**であることを固定する
// （2026-08-15。以前は解析の行に全エンジン共通の欄が 1 つあった）。
//
// ⚠️ 見ているのは 3 つ: **設定が別々に残ること**、**上限を宣言しているエンジンで
// それを超える値を断ること**（`setoption` には応答が返らないので、送ってからでは
// 気づけない）、そして**設定タブの option の一覧には出さないこと**（入口は
// 解析タブのカード。**同じ値の編集口を 2 つ持たない**）。
func TestSetEngineMultiPV(t *testing.T) {
	list := func() []ikkyoku.EngineEntry {
		return []ikkyoku.EngineEntry{
			{ID: "a", Enabled: true, OptionSpecs: []ikkyoku.EngineOption{
				{Name: ikkyoku.MultiPVOption, Type: "spin", Default: "1", Min: 1, Max: 8,
					HasMin: true, HasMax: true},
			}},
			{ID: "b", Enabled: true},
		}
	}

	t.Run("エンジンごとに別々に持てる", func(t *testing.T) {
		s := newTestSettings(t, list())
		if _, err := s.SetEngineMultiPV("a", 5); err != nil {
			t.Fatalf("SetEngineMultiPV: %v", err)
		}
		got, err := s.SetEngineMultiPV("b", 1)
		if err != nil {
			t.Fatalf("SetEngineMultiPV: %v", err)
		}
		if got.Engines[0].MultiPV != 5 || got.Engines[1].MultiPV != 1 {
			t.Errorf("MultiPV = %d, %d, want 5, 1", got.Engines[0].MultiPV, got.Engines[1].MultiPV)
		}
		cfg, err := ikkyoku.LoadConfig(s.path)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.Engines[0].Options[ikkyoku.MultiPVOption] != "5" {
			t.Errorf("保存された値 = %+v", cfg.Engines[0].Options)
		}
	})

	// ⚠️ **未設定はアプリの既定（3）。エンジンの宣言（たいてい 1）ではない。**
	// 「次善手を選んだらどう転ぶか」を辿るのが構想の中心なので、
	// **最善手だけが出ている状態を既定にしない。**
	t.Run("未設定はアプリの既定", func(t *testing.T) {
		s := newTestSettings(t, list())
		got := s.Settings()
		for i, e := range got.Engines {
			if e.MultiPV != ikkyoku.DefaultMultiPV {
				t.Errorf("engines[%d].MultiPV = %d, want %d", i, e.MultiPV, ikkyoku.DefaultMultiPV)
			}
		}
	})

	t.Run("エンジンの上限を超える値は断る", func(t *testing.T) {
		s := newTestSettings(t, list())
		got, err := s.SetEngineMultiPV("a", 10) // 宣言は max 8
		if err == nil {
			t.Fatal("エラーになるべき")
		}
		if got.Engines[0].MultiPV != ikkyoku.DefaultMultiPV {
			t.Errorf("断ったのに値が入った: %d", got.Engines[0].MultiPV)
		}
		// 宣言していないエンジンには上限が無い（宣言が無いだけで、正当な値）。
		if _, err := s.SetEngineMultiPV("b", 10); err != nil {
			t.Errorf("宣言の無いエンジンで断られた: %v", err)
		}
	})

	t.Run("0 以下は断る", func(t *testing.T) {
		s := newTestSettings(t, list())
		if _, err := s.SetEngineMultiPV("a", 0); err == nil {
			t.Error("エラーになるべき")
		}
	})

	// ⚠️ **設定タブの option の一覧には出さない**（入口は解析タブのカード）。
	t.Run("option の一覧には出さない", func(t *testing.T) {
		s := newTestSettings(t, list())
		if _, err := s.SetEngineMultiPV("a", 5); err != nil {
			t.Fatalf("SetEngineMultiPV: %v", err)
		}
		got := s.Settings()
		if _, ok := optionOf(got, 0, ikkyoku.MultiPVOption); ok {
			t.Error("MultiPV が option の一覧に出ている（編集口が 2 つになる）")
		}
		// 「n 件を変更中」にも数えない（一覧に出ていないものを数に入れない）。
		if got.Engines[0].OptionCount != 0 {
			t.Errorf("OptionCount = %d, want 0", got.Engines[0].OptionCount)
		}
	})

	// ⚠️ **「全部を既定に戻す」で巻き添えにしない。** あのボタンが並んでいるのは
	// option の一覧で、そこに MultiPV は出ていない。
	t.Run("全部を既定に戻しても残る", func(t *testing.T) {
		s := newTestSettings(t, list())
		if _, err := s.SetEngineMultiPV("a", 5); err != nil {
			t.Fatalf("SetEngineMultiPV: %v", err)
		}
		got, err := s.ResetEngineOptions("a")
		if err != nil {
			t.Fatalf("ResetEngineOptions: %v", err)
		}
		if got.Engines[0].MultiPV != 5 {
			t.Errorf("MultiPV = %d, want 5（巻き添えで戻っている）", got.Engines[0].MultiPV)
		}
	})
}

// TestSetClickThrough は「枠の内側で後ろの画面を操作する」を固定する。
//
// ⚠️ **一番の要点は「保存されたうえで、その場で効くこと」。** これは
// 中継を触りたくなったその瞬間に切り替える設定なので、次の起動まで待たせない
// （「起動時に盤面を探す」とはそこが違う）。効かせる相手は枠の HWND を持っている
// `CaptureService` なので、間にフックが 1 本入っている。
func TestSetClickThrough(t *testing.T) {
	s := newTestSettings(t, nil)
	var got []bool
	s.OnClickThrough = func(v bool) { got = append(got, v) }

	st, err := s.SetClickThrough(true)
	if err != nil {
		t.Fatalf("SetClickThrough: %v", err)
	}
	if !st.ClickThrough {
		t.Errorf("ClickThrough = false, want true")
	}
	if len(got) != 1 || !got[0] {
		t.Errorf("フックの呼ばれ方 = %v, want [true]", got)
	}

	// **切ったことも設定ファイルに残ること**（キーが消えると、手で編集する側から
	// 存在に気づけない。FitOnStartup と同じ扱い）。
	if _, err := s.SetClickThrough(false); err != nil {
		t.Fatalf("SetClickThrough(false): %v", err)
	}
	cfg, err := ikkyoku.LoadConfig(s.path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ClickThrough {
		t.Errorf("保存された ClickThrough = true, want false")
	}
	if len(got) != 2 || got[1] {
		t.Errorf("フックの呼ばれ方 = %v, want [true false]", got)
	}
}

// TestSetEvalGraphDetached は評価値グラフの切り離しを固定する（2026-09-08）。
//
// ⚠️ **一番の要点は「入口が 2 つあっても食い違わないこと」。** 解析タブのトグル
// （切り離す）と**グラフ窓を閉じる操作**（戻す）の両方がここを通るので、
// **設定と窓の状態がずれない。**
//
// ⚠️ **残すこと自体も要点。** 枠の表示（残さない）とは扱いが違い、こちらは
// **画面の組み方の好み**なので、次の起動でも同じ形で始まってほしい。
func TestSetEvalGraphDetached(t *testing.T) {
	s := newTestSettings(t, nil)
	var got []bool
	s.OnEvalGraphDetached = func(v bool) { got = append(got, v) }

	st, err := s.SetEvalGraphDetached(true)
	if err != nil {
		t.Fatalf("SetEvalGraphDetached: %v", err)
	}
	if !st.EvalGraphDetached {
		t.Errorf("EvalGraphDetached = false, want true")
	}
	if len(got) != 1 || !got[0] {
		t.Errorf("フックの呼ばれ方 = %v, want [true]", got)
	}
	cfg, err := ikkyoku.LoadConfig(s.path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.EvalGraphDetached {
		t.Error("保存されていません（次の起動でドックに戻ってしまう）")
	}

	// **戻したことも残ること**（キーが消えると、手で編集する側から存在に気づけない）。
	if _, err := s.SetEvalGraphDetached(false); err != nil {
		t.Fatalf("SetEvalGraphDetached(false): %v", err)
	}
	cfg, err = ikkyoku.LoadConfig(s.path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.EvalGraphDetached {
		t.Errorf("保存された EvalGraphDetached = true, want false")
	}
	if len(got) != 2 || got[1] {
		t.Errorf("フックの呼ばれ方 = %v, want [true false]", got)
	}
}

// TestSetStudyPaneDetached は**盤の右の列**の切り離しを固定する（2026-09-08）。
//
// ⚠️ **評価値グラフとは別の設定であること。** 片方だけ切り離す使い方が普通なので、
// **一方を切り替えたときにもう一方が巻き添えにならない**ことを見ている。
func TestSetStudyPaneDetached(t *testing.T) {
	s := newTestSettings(t, nil)
	var got []bool
	s.OnStudyPaneDetached = func(v bool) { got = append(got, v) }

	if _, err := s.SetEvalGraphDetached(true); err != nil {
		t.Fatalf("SetEvalGraphDetached: %v", err)
	}
	st, err := s.SetStudyPaneDetached(true)
	if err != nil {
		t.Fatalf("SetStudyPaneDetached: %v", err)
	}
	if !st.StudyPaneDetached || !st.EvalGraphDetached {
		t.Errorf("両方立っているはずです: study=%v graph=%v",
			st.StudyPaneDetached, st.EvalGraphDetached)
	}
	if len(got) != 1 || !got[0] {
		t.Errorf("フックの呼ばれ方 = %v, want [true]", got)
	}

	// ⚠️ **片方を戻してももう一方は残ること。**
	st, err = s.SetStudyPaneDetached(false)
	if err != nil {
		t.Fatalf("SetStudyPaneDetached(false): %v", err)
	}
	if st.StudyPaneDetached {
		t.Error("戻っていません")
	}
	if !st.EvalGraphDetached {
		t.Error("評価値グラフまで戻っています（別の設定であること）")
	}
	cfg, err := ikkyoku.LoadConfig(s.path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.StudyPaneDetached || !cfg.EvalGraphDetached {
		t.Errorf("保存された値が違います: study=%v graph=%v",
			cfg.StudyPaneDetached, cfg.EvalGraphDetached)
	}
}

// TestSetHideWinRateBar は勝率バー（評価値バー）の表示を固定する（2026-09-10）。
//
// ⚠️ **一番の要点は「既定が表示であること」。** `Show...` で持つと、bool の
// ゼロ値（false）が「隠す」になり、**この項目を知らない古い設定ファイルで
// 開いたときに帯が消えたまま始まる。**
//
// ⚠️ **残すこと自体も要点**（切り離しと同じ。画面の組み方の好みなので、
// 次の起動でも同じ形で始まってほしい）。
func TestSetHideWinRateBar(t *testing.T) {
	s := newTestSettings(t, nil)

	// 何もしていない状態は**表示**。
	if s.Settings().HideWinRateBar {
		t.Error("既定が「隠す」になっています（設定を触っていないのに帯が出ません）")
	}

	st, err := s.SetHideWinRateBar(true)
	if err != nil {
		t.Fatalf("SetHideWinRateBar: %v", err)
	}
	if !st.HideWinRateBar {
		t.Errorf("HideWinRateBar = false, want true")
	}
	cfg, err := ikkyoku.LoadConfig(s.path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.HideWinRateBar {
		t.Error("保存されていません（次の起動で帯が戻ってしまう）")
	}

	// 戻せること（**帯を消したまま戻せないと詰む**）。
	st, err = s.SetHideWinRateBar(false)
	if err != nil {
		t.Fatalf("SetHideWinRateBar(false): %v", err)
	}
	if st.HideWinRateBar {
		t.Error("戻っていません")
	}
	cfg, err = ikkyoku.LoadConfig(s.path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.HideWinRateBar {
		t.Errorf("保存された HideWinRateBar = true, want false")
	}
}

// TestSetHidePlayerNames は対局者名の表示を固定する（2026-09-10）。
//
// ⚠️ **一番の要点は「勝率バーとは別の設定であること」。** 帯だけ消して名前は
// 残す使い方も、両方消して盤を大きくする使い方も普通にあるので、
// **片方を切り替えたときにもう片方を巻き添えにしない。**
func TestSetHidePlayerNames(t *testing.T) {
	s := newTestSettings(t, nil)

	if s.Settings().HidePlayerNames {
		t.Error("既定が「隠す」になっています（設定を触っていないのに名前が出ません）")
	}

	if _, err := s.SetHideWinRateBar(true); err != nil {
		t.Fatalf("SetHideWinRateBar: %v", err)
	}
	st, err := s.SetHidePlayerNames(true)
	if err != nil {
		t.Fatalf("SetHidePlayerNames: %v", err)
	}
	if !st.HidePlayerNames || !st.HideWinRateBar {
		t.Errorf("両方立っているはずです: names=%v bar=%v",
			st.HidePlayerNames, st.HideWinRateBar)
	}

	// ⚠️ **片方を戻してももう一方は残ること。**
	st, err = s.SetHidePlayerNames(false)
	if err != nil {
		t.Fatalf("SetHidePlayerNames(false): %v", err)
	}
	if st.HidePlayerNames {
		t.Error("戻っていません")
	}
	if !st.HideWinRateBar {
		t.Error("勝率バーまで戻っています（別の設定であること）")
	}
	cfg, err := ikkyoku.LoadConfig(s.path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.HidePlayerNames || !cfg.HideWinRateBar {
		t.Errorf("保存された値が違います: names=%v bar=%v",
			cfg.HidePlayerNames, cfg.HideWinRateBar)
	}
}

// ⚠️ **エンジンが名乗った名前は設定ファイルに残すこと。**
//
// 名乗りは**繋がないと分からない**ので、メモリだけで持つとアプリを閉じた時点で
// 消え、次の起動では設定タブも解析タブも exe のファイル名に戻る。
//
// ⚠️ **人が付けた名前は上書きしない**（同じ exe を option 違いで 2 つ登録すると
// 名乗りは同じになるので、潰すと見分けが付かなくなる）。
func TestRememberEngineName(t *testing.T) {
	exe := testEnginePath(t)
	s := newTestSettings(t, []ikkyoku.EngineEntry{
		{ID: "engine-1", Path: exe, Enabled: true},
		{ID: "engine-2", Path: exe, Name: "水匠5(D12)", Enabled: true},
	})

	s.rememberEngineName("engine-1", "Suisho5")
	s.rememberEngineName("engine-2", "Suisho5")

	e1, _ := s.engineEntry("engine-1")
	if e1.EngineName != "Suisho5" || e1.DisplayName() != "Suisho5" {
		t.Errorf("名乗りが既定の表示名になっていません: %+v / %q", e1, e1.DisplayName())
	}
	e2, _ := s.engineEntry("engine-2")
	if e2.DisplayName() != "水匠5(D12)" {
		t.Errorf("人が付けた名前を上書きしています: %q", e2.DisplayName())
	}

	// ⚠️ **ファイルに残ること**（メモリだけでは次の起動で消える）。
	reloaded, err := ikkyoku.LoadConfig(s.path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if reloaded.EngineList()[0].EngineName != "Suisho5" {
		t.Errorf("設定ファイルに残っていません: %+v", reloaded.EngineList()[0])
	}

	// ⚠️ **変わっていなければ書かないこと。** 解析が終わるたびに呼ばれるので、
	// 素通しにすると**1 手ごとに設定ファイルを書く**（連続解析では手数ぶん）。
	// ファイルに目印を入れて、同じ名前で呼んでも残ることで確かめる。
	marked := reloaded
	marked.SutemeDataDir = "書き換えられたら消える目印"
	if err := ikkyoku.SaveConfig(s.path, marked); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	s.rememberEngineName("engine-1", "Suisho5")
	after, err := ikkyoku.LoadConfig(s.path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if after.SutemeDataDir != marked.SutemeDataDir {
		t.Error("同じ名前なのに設定ファイルを書き直しています")
	}
}

// ⚠️ **実行ファイルを差し替えたら名乗りは捨てること**（別のエンジンの名乗りなので、
// 残すと違うエンジンの名前を出す）。**人が付けた名前は捨てない。**
func TestSetEnginePathDropsReportedName(t *testing.T) {
	s := newTestSettings(t, []ikkyoku.EngineEntry{
		{ID: "engine-1", Path: testEnginePath(t), Name: "手で付けた", EngineName: "Suisho5", Enabled: true},
	})

	other := filepath.Join(t.TempDir(), "Nagisa.exe")
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEnginePath("engine-1", other); err != nil {
		t.Fatalf("SetEnginePath: %v", err)
	}
	e, _ := s.engineEntry("engine-1")
	if e.EngineName != "" {
		t.Errorf("前のエンジンの名乗りが残っています: %q", e.EngineName)
	}
	if e.Name != "手で付けた" {
		t.Errorf("人が付けた名前まで捨てています: %q", e.Name)
	}
}

// testEnginePath は存在する実行ファイルを 1 つ作る（`checkEnginePath` が実在を見る）。
func testEnginePath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "YaneuraOu.exe")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

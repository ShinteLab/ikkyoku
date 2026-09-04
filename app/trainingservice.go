package app

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"time"

	"github.com/ShinteLab/ikkyoku/training"
)

// TrainingService は訂正した局面を suteme の学習用サーバへ登録する Service。
//
// **状態を持たない。** 「今どの画像・どの局面か」はフロントが持っている
// （撮った結果と EditState）ので、送るときにまとめて渡してもらう。ここが
// 「直近の 1 枚」を覚えると、撮り直しとの食い違いを自分で管理することになる。
//
// **自動では送らない**（2026-08-07 の決定）。人間が直したのは 1 マスで残り 80 マスは
// 推論結果のまま、という「自分の出力を正解として食う」形になり、間違いが固定される。
// 送るのは局面ごとにボタンを押したときだけ。
//
// ⚠️ **画面に見えていない駒を知識で補った訂正は送らないこと**（テロップで盤が
// 隠れている等）。ラベルが画素と一致しなくなる。これも自動送信にしない理由。
type TrainingService struct {
	logger   *slog.Logger
	settings *SettingsService
}

func NewTrainingService(logger *slog.Logger, settings *SettingsService) *TrainingService {
	return &TrainingService{logger: logger, settings: settings}
}

// TrainingStatus は「今このサーバに送ってよいか」の問い合わせ結果。
//
// **エラーも値として返す**（error にしない）。設定タブに出す情報であって、
// 呼び出しが失敗したわけではない。相手が起動していないのは普通の状態。
type TrainingStatus struct {
	// Configured は「訂正盤面を suteme に登録する」が有効か。
	Configured bool `json:"configured"`
	// Target は問い合わせ先("http://host:port")。
	Target string `json:"target"`
	// Reachable は応答があったか。
	Reachable bool `json:"reachable"`
	// Error は応答が無い・受け付けられない場合の理由（日本語）。
	Error string `json:"error"`
	// Accepting は suteme 側で登録受付が有効になっているか。
	//
	// ⚠️ **false でも、同じマシン（ループバック）からは送れる。** suteme は
	// ループバックを認証・受付判定の手前で素通しにしている。**送信の可否として
	// 使わないこと**（送れるのに送れないと表示することになる）。表示に留める。
	Accepting bool `json:"accepting"`
	// Detailed 以下は、相手が素性を明かした場合だけ入る（ループバックか、
	// 正しいトークンを持っている場合）。
	Detailed    bool   `json:"detailed"`
	Entries     int    `json:"entries"`
	Capacity    int    `json:"capacity"`
	DataVersion string `json:"dataVersion"`
	// Note は「そのまま送れるとは限らない」ことを伝える補足（空なら無し）。
	Note string `json:"note"`
}

// Status は suteme が登録を受け付けられる状態かを問い合わせる（設定タブの「確認」）。
func (s *TrainingService) Status() TrainingStatus {
	cfg := s.settings.trainingConfig()
	st := TrainingStatus{Configured: cfg.Enabled}
	target, err := training.ParseBase(cfg.Host, cfg.Port)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Target = target

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	c := training.New(cfg.Host, cfg.Port, cfg.Token)
	res, err := c.Status(ctx)
	if err != nil {
		st.Error = err.Error()
		s.logger.Info("suteme の状態を取れませんでした", "target", target, "error", err)
		return st
	}
	st.Reachable = true
	st.Accepting = res.Enabled
	st.Detailed = res.Detailed
	st.Entries = res.Entries
	st.Capacity = res.Capacity
	st.DataVersion = res.DataVersion
	switch {
	case !res.Detailed:
		// 素性を返さない = ループバックでもトークンも合っていない相手。
		st.Note = "このサーバは詳細を返しません（トークンが要る設定かもしれません）"
	case res.Capacity > 0 && res.Entries >= res.Capacity:
		st.Note = "履歴が上限です。suteme 側で不要な局面を削除するまで登録できません"
	case !res.Enabled:
		st.Note = "登録受付は無効ですが、同じマシンからなら送れます（suteme はローカルを素通しにします）"
	}
	return st
}

// TrainingSendResult は登録の結果。
type TrainingSendResult struct {
	ID string `json:"id"`
	// Duplicate は同じ画像が既に登録されていたか。**失敗ではない**
	// （suteme は画像のハッシュで再送を弾き、既存の ID を返す）。
	Duplicate bool `json:"duplicate"`
	Entries   int  `json:"entries"`
}

// Send は訂正した局面を学習データとして登録する。
//
// 渡すのは撮った PNG のパス・正解の SFEN・**画像の中での盤面の矩形**の 3 点。
// これが suteme の学習サンプルの単位そのもので、81 マスの切り出しは向こうが行う
// （**ikkyoku がサンプルの形式を知らないでいられる**のが要点）。
//
// ⚠️ **矩形は認識結果の Debug.Region をそのまま渡すこと。** 盤の位置が分からないと
// suteme は DetectBoard に頼ることになり、ずらしによる水増しも作れないため
// 学習価値が落ちる（向こうは座標なしを 400 で断る）。
func (s *TrainingService) Send(path, sfen string, x1, y1, x2, y2 int) (TrainingSendResult, error) {
	cfg := s.settings.trainingConfig()
	if !cfg.Enabled {
		return TrainingSendResult{}, fmt.Errorf("設定タブで「訂正盤面を suteme に登録する」を有効にしてください")
	}
	if path == "" {
		return TrainingSendResult{}, fmt.Errorf("送る画像がありません")
	}

	// 送るのは 3.5MB 級の PNG になりうるので、状態の問い合わせより長めに取る。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := training.New(cfg.Host, cfg.Port, cfg.Token)
	res, err := c.Register(ctx, training.Sample{
		ImagePath: path,
		SFEN:      sfen,
		Bounds:    image.Rect(x1, y1, x2, y2),
	})
	if err != nil {
		s.logger.Warn("訂正データを登録できませんでした", "target", c.BaseURL, "path", path, "error", err)
		return TrainingSendResult{}, err
	}
	s.logger.Info("訂正データを登録しました",
		"target", c.BaseURL, "id", res.ID, "duplicate", res.Duplicate, "path", path)
	return TrainingSendResult{ID: res.ID, Duplicate: res.Duplicate, Entries: res.Entries}, nil
}

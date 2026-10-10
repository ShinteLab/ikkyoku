// Package log は ikkyoku のログ。**ikkyoku のコードはここの関数でログを出す。**
//
//	log.Info("棋譜を読みました", "url", u, "moves", n)   // slog と同じ「メッセージ + キーと値」
//	log.Debugf("候補 %d 手", len(moves))                  // printf の形
//
// 出口は slog.Default()。ここの関数はそれへ書くだけで、どこへ出すか（ファイル・コンソール）と
// どのレベルまで出すか（パッケージごと）は Init が決める（setup.go）。
// **Init を呼ばなければ slog の既定（標準エラー）に出る**ので、テストでは何もしなくてよい。
//
// ⚠️ **Logger を引数で配らないこと。** 呼び出し元のパッケージは記録の位置（PC）から分かり、
// レベルはパッケージごとに Init の設定で絞れる。Service に *slog.Logger を持たせる必要は無い。
//
// ⚠️ **ikkyoku を使う側に、このパッケージの型を求めないこと。** レベルは slog.Level のまま
// （ここの Level* は wenteasy/log の値をそのまま出しているだけ）、出口は slog.Default()。
// 使う側は標準の slog だけで ikkyoku のログを扱える。
package log

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"time"

	wlog "github.com/wenteasy/log"
)

// slog の段階（Debug / Info / Warn / Error）に Trace と Notice を足したもの。
const (
	LevelTrace  = wlog.LevelTrace
	LevelDebug  = wlog.LevelDebug
	LevelInfo   = wlog.LevelInfo
	LevelNotice = wlog.LevelNotice
	LevelWarn   = wlog.LevelWarn
	LevelError  = wlog.LevelError
)

// Enabled はそのレベルが出るかを返す（呼び出し元のパッケージの設定は見ない。足切りだけ）。
// 引数を組み立てるのが重いときに先に確かめるためのもの。
func Enabled(l slog.Level) bool {
	return slog.Default().Enabled(context.Background(), l)
}

func Trace(msg string, args ...any)  { output("", LevelTrace, msg, "", nil, args) }
func Debug(msg string, args ...any)  { output("", LevelDebug, msg, "", nil, args) }
func Info(msg string, args ...any)   { output("", LevelInfo, msg, "", nil, args) }
func Notice(msg string, args ...any) { output("", LevelNotice, msg, "", nil, args) }
func Warn(msg string, args ...any)   { output("", LevelWarn, msg, "", nil, args) }
func Error(msg string, args ...any)  { output("", LevelError, msg, "", nil, args) }

func Tracef(format string, args ...any)  { output("", LevelTrace, "", format, args, nil) }
func Debugf(format string, args ...any)  { output("", LevelDebug, "", format, args, nil) }
func Infof(format string, args ...any)   { output("", LevelInfo, "", format, args, nil) }
func Noticef(format string, args ...any) { output("", LevelNotice, "", format, args, nil) }
func Warnf(format string, args ...any)   { output("", LevelWarn, "", format, args, nil) }
func Errorf(format string, args ...any)  { output("", LevelError, "", format, args, nil) }

// Topic は**本体のログとは別のファイルへ出す話題**（2026-10-10）。log.Follow.Info(...) のように使う。
// 話題のログは Init が話題ごとのファイル（Dir/<FilePrefix>-<話題>_<日付>.log）へ出し、
// **コンソールには出さない**。⚠️ **Warn 以上は本体（ファイルとコンソール）にも出す**（見落とさないため）。
// レベルは話題に関係なく呼び出し元のパッケージで決まる（LevelsFile）。
type Topic struct{ name string }

// Follow は中継を追う（Phase 6 の追従）ログ。1 周ごとに何行も出るので、本体から分けた
// （中継中にコンソールが埋まって目につく。2026-10-10）。
var Follow = Topic{"follow"}

func (t Topic) Trace(msg string, args ...any)  { output(t.name, LevelTrace, msg, "", nil, args) }
func (t Topic) Debug(msg string, args ...any)  { output(t.name, LevelDebug, msg, "", nil, args) }
func (t Topic) Info(msg string, args ...any)   { output(t.name, LevelInfo, msg, "", nil, args) }
func (t Topic) Notice(msg string, args ...any) { output(t.name, LevelNotice, msg, "", nil, args) }
func (t Topic) Warn(msg string, args ...any)   { output(t.name, LevelWarn, msg, "", nil, args) }
func (t Topic) Error(msg string, args ...any)  { output(t.name, LevelError, msg, "", nil, args) }

func (t Topic) Tracef(format string, args ...any) { output(t.name, LevelTrace, "", format, args, nil) }
func (t Topic) Debugf(format string, args ...any) { output(t.name, LevelDebug, "", format, args, nil) }
func (t Topic) Infof(format string, args ...any)  { output(t.name, LevelInfo, "", format, args, nil) }
func (t Topic) Noticef(format string, args ...any) {
	output(t.name, LevelNotice, "", format, args, nil)
}
func (t Topic) Warnf(format string, args ...any)  { output(t.name, LevelWarn, "", format, args, nil) }
func (t Topic) Errorf(format string, args ...any) { output(t.name, LevelError, "", format, args, nil) }

// topicKey は話題を出口（setup.go の topicHandler）へ伝える context の鍵。
// Record の属性にしないのは、ファイルの 1 行に余計な topic=... を載せないため。
type topicKey struct{}

func topicOf(ctx context.Context) string {
	s, _ := ctx.Value(topicKey{}).(string)
	return s
}

// callerSkip は runtime.Callers → output → 公開関数（Topic のメソッドも同じ段） → 呼び出し元 の段数。
// ⚠️ **output は公開関数から直接呼ぶこと。** 間に関数を挟むと、記録される位置（と、
// パッケージごとのレベルの判定）がこのパッケージになる。wenteasy/log の Info などを
// 包まないのも同じ理由（包むと位置がここになる）。
const callerSkip = 3

func output(topic string, l slog.Level, msg, format string, fargs, attrs []any) {
	ctx := context.Background()
	if topic != "" {
		ctx = context.WithValue(ctx, topicKey{}, topic)
	}
	logger := slog.Default()
	if !logger.Enabled(ctx, l) {
		return
	}
	if format != "" {
		msg = fmt.Sprintf(format, fargs...)
	}
	var pcs [1]uintptr
	runtime.Callers(callerSkip, pcs[:])
	r := slog.NewRecord(time.Now(), l, msg, pcs[0])
	r.Add(attrs...)
	_ = logger.Handler().Handle(ctx, r)
}

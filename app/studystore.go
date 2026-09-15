package app

// 検討の控えをディスクに置く側（2026-09-16。Step 1）。
//
// **1 セッション 1 ファイル**（`<検討の id>.json`）。⚠️ **一覧 UI も管理機能も
// 作らない** —— 索引は棋譜タブ（棚）が持つ予定で、ここは
// **「前回の続きを戻す」**のためだけの置き場（CLAUDE.md「永続化は前提にしない」）。
//
// ⚠️ **控えはおまけ。** 書けなくても読めなくても**解析は今までどおり動く**
// （設計原則3）。**エラーで起動を止めないこと。**

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// studyStoreDebounce は控えを書くまでの間（**間引き**）。
//
// ⚠️ **1 回ごとに書かないこと。** `analyze:info` は**深さが進むたびに、しかも
// エンジンの数だけ**届くので、そのまま書くと 1 手のあいだに何十回も書く。
// ⚠️ **長くしすぎないこと** —— ここが「落ちたら失う幅」そのもの。
const studyStoreDebounce = 3 * time.Second

// studyStoreKeep は残す控えの数（古いものから消す）。
//
// ⚠️ **管理機能ではなく、溜まりすぎないための上限。** 撮って解析するたびに
// 1 ファイル増えるので、上限が無いと開発中だけで数千になる。
const studyStoreKeep = 200

// StudyStore は検討の控えの読み書き。
//
// ⚠️ **`StudyService` の公開メソッドを増やさないために別の型にしてある。**
// Service の公開メソッドは**そのままフロントの API**（bindings）になるので、
// 控えの出し入れのような**フロントが呼ばないもの**を生やせない。
type StudyStore struct {
	logger *slog.Logger
	dir    string
	svc    *StudyService

	wake   chan struct{}
	done   chan struct{}
	closed chan struct{}
	once   sync.Once

	// last は最後に書いた中身（**控えた時刻を除いたもの**）。
	//
	// ⚠️ **時刻を含めて比べないこと** —— 毎回違う値になるので、
	// **何も変わっていなくても書き続ける**ことになる。
	last []byte

	// mu は索引を守る（**`StudyService` のロックとは別物**）。
	mu sync.Mutex
	// games は棚の棋譜 id → 一番新しい控えのファイル名（2026-09-16。Step 2）。
	//
	// **棋譜タブの「解析する」で前の検討を開くときに引く。**
	// ⚠️ **棚に入っていない検討は載らない**（`GameID` が空）。**それが普通**で、
	// 棚に入っているほうが特別。
	games map[string]string
	// indexed は索引を組んだか（**一度だけ全部読む**）。
	indexed bool
}

// NewStudyStore は控えの置き場を用意する（**ディレクトリはまだ作らない**）。
func NewStudyStore(logger *slog.Logger, dir string, svc *StudyService) *StudyStore {
	return &StudyStore{
		logger: logger,
		dir:    dir,
		svc:    svc,
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
		closed: make(chan struct{}),
	}
}

// Start は自動保存を始める（`StudyService` に 2 つの口を差す）。
//
// ⚠️ **`Restore` のあとに呼ぶこと。** 先に始めると、**復元する前の空の状態を
// 控えに書いてしまう**（そのまま前回の控えが消える、ではないが余計な 1 本が増える）。
func (t *StudyStore) Start() {
	t.svc.mu.Lock()
	t.svc.dirty = t.mark
	// ⚠️ **今すぐ書かせる口も差すこと。** セッションを入れ替える前にこれを
	// 通さないと、**間引きの幅（3 秒）のぶんが前のセッションから落ちる。**
	t.svc.save = t.flush
	t.svc.mu.Unlock()
	go t.loop()
}

// mark は「書き直す必要がある」を受ける口。
//
// ⚠️ **待たないこと**（`StudyService` のロックを持ったまま呼ばれる）。
// 詰まっているなら既に知らせは立っているので、落としてよい。
func (t *StudyStore) mark() {
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// Close は**最後にもう 1 回書いて**終わる。
//
// ⚠️ **ここで書かないと、最後の数秒が落ちる**（間引いている幅のぶん）。
func (t *StudyStore) Close() error {
	t.once.Do(func() {
		close(t.done)
		<-t.closed
	})
	return nil
}

func (t *StudyStore) loop() {
	defer close(t.closed)
	for {
		select {
		case <-t.done:
			t.flush()
			return
		case <-t.wake:
			timer := time.NewTimer(studyStoreDebounce)
			select {
			case <-timer.C:
			case <-t.done:
				timer.Stop()
				t.flush()
				return
			}
			// ⚠️ **待っているあいだに来た知らせはここで落とす**（これから書く
			// ぶんに含まれる）。**書いている最中に来たものは残る**ので、
			// 次の周でもう 1 回書かれる。
			select {
			case <-t.wake:
			default:
			}
			t.flush()
		}
	}
}

// flush は今の検討を 1 ファイルに書く（**変わっていなければ書かない**）。
func (t *StudyStore) flush() {
	rec, ok := t.svc.sessionRecord()
	if !ok {
		return
	}
	// ⚠️ **時刻を抜いて比べること**（毎回違う値なので、含めると必ず「変わった」）。
	rec.SavedAt = 0
	body, err := json.Marshal(rec)
	if err != nil {
		t.logger.Warn("検討の控えを組み立てられませんでした", "error", err)
		return
	}
	if string(body) == string(t.last) {
		return
	}
	rec.SavedAt = time.Now().Unix()
	out, err := json.Marshal(rec)
	if err != nil {
		t.logger.Warn("検討の控えを組み立てられませんでした", "error", err)
		return
	}
	if err := t.write(rec.ID, out); err != nil {
		// ⚠️ **止めないこと**（設計原則3）。控えが書けなくても解析は続く。
		t.logger.Warn("検討を控えられませんでした", "id", rec.ID, "dir", t.dir, "error", err)
		return
	}
	t.last = body
	t.remember(rec.GameID, rec.ID)
	t.prune()
}

// write は差し替えで書く（**途中で落ちても壊れた JSON が残らない**）。
func (t *StudyStore) write(id string, body []byte) error {
	if err := os.MkdirAll(t.dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(t.dir, id+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Restore は**一番新しい控え**を戻す（戻せたなら true）。
//
// ⚠️ **棚の棋譜から開く側は `RestoreGame`**（あちらは今の検討と入れ替える）。
//
// ⚠️ **1 つしか試さない。** 読めなかったときに古いものへ遡ると、
// **何日も前の検討が黙って開く**ことになる（何が起きたのか画面から読めない）。
// ⚠️ **戻せなくても起動は通ること**（設計原則3）。
func (t *StudyStore) Restore() (bool, error) {
	names, err := t.list()
	if err != nil || len(names) == 0 {
		return false, err
	}
	path := filepath.Join(t.dir, names[len(names)-1])
	body, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var rec StudyRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		return false, fmt.Errorf("ikkyoku/app: 控えが読めません（%s）: %w", path, err)
	}
	if err := t.svc.restoreSession(rec); err != nil {
		return false, err
	}
	// ⚠️ **戻した中身を `last` に入れること。** 入れないと、**復元した直後に
	// 同じ内容をもう 1 回書く**（変わっていないのに書く）。
	rec.SavedAt = 0
	if b, err := json.Marshal(rec); err == nil {
		t.last = b
	}
	t.logger.Info("前回の検討を戻しました", "id", rec.ID, "path", path)
	return true, nil
}

// list は控えのファイル名を**古い順**に返す。
//
// ⚠️ **名前で並べてよい。** 検討の id は日時が頭に付いている（`newSessionID`）。
func (t *StudyStore) list() ([]string, error) {
	entries, err := os.ReadDir(t.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names := []string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// prune は古い控えを消す（**上限を超えたぶんだけ**）。
func (t *StudyStore) prune() {
	names, err := t.list()
	if err != nil || len(names) <= studyStoreKeep {
		return
	}
	for _, n := range names[:len(names)-studyStoreKeep] {
		if err := os.Remove(filepath.Join(t.dir, n)); err != nil {
			t.logger.Warn("古い控えを消せませんでした", "name", n, "error", err)
		}
	}
}

// ---- 棚の棋譜と結ぶ（2026-09-16。Step 2）--------------------------------
//
// **棋譜タブから開いた棋譜には `GameID` が付く**ので、次に同じ棋譜を開いたときに
// 前の検討（枝も評価値も）がそのまま戻る。
//
// ⚠️ **検討の一覧 UI は作らない。** 索引は**棋譜タブ（棚）が既に持っている** ——
// ここが答えるのは「この棋譜に控えがあるか」「あるならどれか」だけ。

// index は控えを一度だけ全部読んで、棚の棋譜 id ごとに**一番新しいもの**を覚える。
//
// ⚠️ **一度だけにすること**（以後は書いたときに足すだけ。`remember`）。
// 控えは 1 セッション 1 ファイルなので、**引くたびにディレクトリを舐めると
// 「解析する」を押すたびに待たされる。**
func (t *StudyStore) index() {
	if t.indexed {
		return
	}
	t.indexed = true
	t.games = map[string]string{}
	names, err := t.list()
	if err != nil {
		t.logger.Warn("控えの索引を作れませんでした", "dir", t.dir, "error", err)
		return
	}
	// ⚠️ **古い順に読むこと**（`list` がそう返す）。同じ棋譜の控えが複数あったら
	// **後から書いたほうが勝つ**（あとの行が上書きする）。
	for _, n := range names {
		body, err := os.ReadFile(filepath.Join(t.dir, n))
		if err != nil {
			continue
		}
		// ⚠️ **`gameId` だけ読めればよい**（木も評価値も要らない）。
		var head struct {
			GameID string `json:"gameId"`
		}
		if err := json.Unmarshal(body, &head); err != nil || head.GameID == "" {
			continue
		}
		t.games[head.GameID] = n
	}
}

// remember は索引を更新する（書いたとき）。
func (t *StudyStore) remember(gameID, sessionID string) {
	if gameID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.index()
	t.games[gameID] = sessionID + ".json"
}

// RestoreGame は棚の棋譜に紐づく控えを開く（**棋譜タブの「解析する」**）。
//
// ⚠️ **今の検討と入れ替える**（`adoptSession`）。`Restore`（起動時）と違って
// **人が「この棋譜を解析する」と言っている**ので、門番は要らない。
//
// ⚠️ **戻せなくてもエラーにしないこと**（false を返すだけ）。呼び出し側は
// **棋譜を普通に読み込む**へ落ちればよく、**控えが読めないことで
// 「解析する」が押せなくなってはいけない**（設計原則3）。
func (t *StudyStore) RestoreGame(gameID string) (StudyState, bool) {
	if gameID == "" {
		return StudyState{}, false
	}
	t.mu.Lock()
	t.index()
	name := t.games[gameID]
	t.mu.Unlock()
	if name == "" {
		return StudyState{}, false
	}
	path := filepath.Join(t.dir, name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.logger.Warn("控えが読めませんでした", "path", path, "error", err)
		return StudyState{}, false
	}
	var rec StudyRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		t.logger.Warn("控えが読めませんでした", "path", path, "error", err)
		return StudyState{}, false
	}
	st, err := t.svc.adoptSession(rec)
	if err != nil {
		t.logger.Warn("控えを開けませんでした", "path", path, "error", err)
		return StudyState{}, false
	}
	// ⚠️ **開いた中身を `last` に入れること**（復元直後に同じ内容を書き直さない）。
	rec.SavedAt = 0
	if b, err := json.Marshal(rec); err == nil {
		t.last = b
	}
	t.logger.Info("この棋譜の前の検討を開きました", "gameId", gameID, "path", path)
	return st, true
}

package app

import "sync"

// IssueService は「起動はできたが、足りないもの・できないこと」を集めて画面に出す
// （2026-10-04）。メイン画面のツールバーの ⚠ が、これを一覧にする。
//
// **狙いは「黙って動かない」を無くすこと。** 認識器が無い・棋譜データベースを
// 開けない・設定ファイルが壊れている、のどれも**起動は止めない**（設計原則3）が、
// それまではログに 1 行出るだけで、画面からは何も分からなかった。
//
// ⚠️ **起動を止めるものはここに来ない。** 画面を出す前に終わるもの（WebView2 が無い・
// Wails の致命的なエラー）は、画面が無いので `_cmd/ikkyoku/fatal.go` が
// OS のメッセージボックスで出す。
//
// ⚠️ **その場で失敗を返せる操作の失敗はここに積まない。** 外部エンジンが起動できない
// のは解析したときに `analyze:failed` で出るし、棋譜の取得の失敗はその場で出る。
// ここは**操作する前から分かっていて、操作しないと気づけないもの**の置き場所。
//
// ⚠️ **問題は鍵（Key）ごとに 1 つ。** 直ったら `Clear` で消す（認識器を指し直した・
// 棋譜データベースを開き直せた）。**積みっぱなしにしないこと** —— 直したのに ⚠ が
// 残ると、ほかの問題が増えても気づかれなくなる。
type IssueService struct {
	// Emit は一覧が変わったことをフロントへ流す口（`issues:changed`）。
	// **`_cmd/ikkyoku` が差し込む。** nil なら捨てる（画面は開いたときに `Report` で読む）。
	Emit EventEmitter

	mu     sync.Mutex
	issues []Issue
	logDir string
}

// Issue は問題 1 つ。
type Issue struct {
	// Key は同じ問題を上書き・取り消すための鍵（"recognizer" など。画面には出さない）。
	Key string `json:"key"`
	// Level は IssueError（その機能が使えない）か IssueWarn（使えるが欠けている）。
	Level string `json:"level"`
	// Title は何が起きているか（1 行）。
	Title string `json:"title"`
	// Effect はそのせいで何ができないか（何はできるか）。空でもよい。
	Effect string `json:"effect"`
	// Detail は元のエラーや場所（ログに出したものと同じ）。空でもよい。
	Detail string `json:"detail"`
}

// IssueReport は画面に渡す一覧。
type IssueReport struct {
	Issues []Issue `json:"issues"`
	// LogDir はログの置き場所（書けていなければ空）。**問題を追うときに次に開く場所**なので
	// 一覧と一緒に出す。
	LogDir string `json:"logDir"`
}

const (
	// IssueError はその機能が使えない。
	IssueError = "error"
	// IssueWarn は使えるが欠けている（精度が落ちる・記録が残らない など）。
	IssueWarn = "warn"
)

// IssuesChanged は一覧が変わったときに流すイベントの名前（ペイロードは IssueReport）。
const IssuesChanged = "issues:changed"

// NewIssueService は空の一覧を作る。
func NewIssueService() *IssueService {
	return &IssueService{}
}

// Report は今の一覧を返す（画面が開いたときに読む）。
func (s *IssueService) Report() IssueReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reportLocked()
}

// Set は問題を足す。同じ Key があれば**その場所で**差し替える（並びを動かさない）。
//
//wails:ignore
func (s *IssueService) Set(issue Issue) {
	s.mu.Lock()
	replaced := false
	for i := range s.issues {
		if s.issues[i].Key == issue.Key {
			if s.issues[i] == issue {
				s.mu.Unlock()
				return
			}
			s.issues[i] = issue
			replaced = true
			break
		}
	}
	if !replaced {
		s.issues = append(s.issues, issue)
	}
	r := s.reportLocked()
	s.mu.Unlock()
	s.emit(r)
}

// Clear は問題を取り消す。無ければ何もしない（イベントも出さない）。
//
//wails:ignore
func (s *IssueService) Clear(key string) {
	s.mu.Lock()
	for i := range s.issues {
		if s.issues[i].Key == key {
			s.issues = append(s.issues[:i], s.issues[i+1:]...)
			r := s.reportLocked()
			s.mu.Unlock()
			s.emit(r)
			return
		}
	}
	s.mu.Unlock()
}

// SetLogDir はログの置き場所を覚える（起動時に 1 回）。
//
//wails:ignore
func (s *IssueService) SetLogDir(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logDir = dir
}

func (s *IssueService) reportLocked() IssueReport {
	issues := make([]Issue, len(s.issues))
	copy(issues, s.issues)
	return IssueReport{Issues: issues, LogDir: s.logDir}
}

// emit は**ロックの外で**呼ぶこと（Emit の先がこの Service を読みに来ても詰まらないように）。
func (s *IssueService) emit(r IssueReport) {
	if s.Emit == nil {
		return
	}
	s.Emit(IssuesChanged, r)
}

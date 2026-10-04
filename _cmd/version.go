// version.go は ikkyoku のバージョンを各ファイルへ伝播させる。
//
// **唯一の正は `_cmd/ikkyoku/version`**（テキスト 1 行）。アプリは `//go:embed version` で
// これを焼き込む（`_cmd/ikkyoku/main.go`）。ここが書き換えるのは次の 2 つ:
//
//   - `_cmd/ikkyoku/build/config.yml` の `info.version`
//   - `_cmd/ikkyoku/frontend/package.json` の `version`
//
// ⚠️ **`build/windows/info.json` と `build/darwin/Info.plist` は自前で書かない。**
// 書き換えたあとに `wails3 update build-assets` で作り直す（スキル `ikkyoku-build`）。
//
// **リポジトリのルートで実行する**（パスはルートからの相対）:
//
//	go run _cmd/version.go 1.2.3   指定したバージョンを全ファイルに設定
//	go run _cmd/version.go -bump   patch / minor / major を対話で選ぶ（Enter = patch）
//	go run _cmd/version.go         version ファイルの値で他のファイルを揃え直す
//	go run _cmd/version.go -print  今のバージョンを表示するだけ（何も書き換えない）
//	go run _cmd/version.go -auto   `v`+今のバージョンのタグがあれば patch を上げ、無ければ揃え直す
//
// `_cmd` はアンダースコア始まりなので `go build ./...` の対象外。標準ライブラリだけで書く
// （ルートのモジュールで `go run` するため、依存を足すと go.mod に余計な require が載る）。
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const (
	versionFile = "./_cmd/ikkyoku/version"
	configYml   = "./_cmd/ikkyoku/build/config.yml"
	packJsn     = "./_cmd/ikkyoku/frontend/package.json"
)

// 値の部分だけを置き換え、インデントや行末コメントは残す。
// ⚠️ **行頭を空白だけに限ること。** config.yml には `#   version: "0.0.1"`（ios の記入例）という
// コメント行があり、行頭を縛らないとそちらにも当たって `info` のキーが重複し、YAML が壊れる。
var (
	configRg = regexp.MustCompile(`^(\s*version:\s*")[0-9]+\.[0-9]+\.[0-9]+(".*)$`)
	packRg   = regexp.MustCompile(`^(\s*"version":\s*")[0-9]+\.[0-9]+\.[0-9]+(".*)$`)
)

const inquiry = `
Now Version: %s

  Enter   -> Patch Version
  1:Patch -> %s
  2:Minor -> %s
  3:Major -> %s
  Other   -> Cancel

Please select the upgrade version(1-3)[1]:`

type ver struct {
	major int
	minor int
	patch int
}

func parseVer(v string) *ver {
	rtn := ver{-1, -1, -1}
	vals := strings.Split(v, ".")
	if len(vals) == 3 {
		rtn.major = parseInt(vals[0])
		rtn.minor = parseInt(vals[1])
		rtn.patch = parseInt(vals[2])
	}
	return &rtn
}

func parseInt(v string) int {
	val, err := strconv.Atoi(v)
	if err != nil || val < 0 {
		return -1
	}
	return val
}

func (v ver) addMajor() *ver { return &ver{v.major + 1, 0, 0} }
func (v ver) addMinor() *ver { return &ver{v.major, v.minor + 1, 0} }
func (v ver) addPatch() *ver { return &ver{v.major, v.minor, v.patch + 1} }
func (v ver) String() string { return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch) }
func (v *ver) isError() bool {
	return v == nil || v.major < 0 || v.minor < 0 || v.patch < 0
}

var (
	bump     bool
	auto     bool
	printVer bool
)

func main() {
	flag.BoolVar(&bump, "bump", false, "対話的にバージョンを選択して更新")
	flag.BoolVar(&auto, "auto", false, "現在のバージョンのタグがあれば patch を上げる")
	flag.BoolVar(&printVer, "print", false, "現在のバージョンを表示")
	flag.Parse()

	// -print: 他のファイルには触れず今の値を出すだけ（スクリプトから参照する用）
	if printVer {
		v, err := parseVersion()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %+v\n", err)
			os.Exit(1)
		}
		fmt.Println(v)
		return
	}

	if err := run(flag.Args()); err != nil {
		fmt.Fprintf(os.Stderr, "run() error: %+v\n", err)
		os.Exit(1)
	}
	fmt.Println("Success")
}

func run(args []string) error {
	now, err := parseVersion()
	if err != nil {
		return err
	}

	var rtn *ver
	if auto {
		// タグがある = そのバージョンはデプロイ済みなのに version が上がっていない
		rtn, err = autoVersion(now)
		if err != nil {
			return err
		}
		if rtn == nil {
			fmt.Println("Version:", now)
			return write(now)
		}
	} else if len(args) == 0 && !bump {
		// 引数もフラグも無い: 今の値で揃え直す
		fmt.Println("Version:", now)
		return write(now)
	} else if bump {
		rtn = inquiryVersion(now)
	} else {
		rtn = parseVer(args[0])
	}
	if rtn.isError() {
		return fmt.Errorf("input version error")
	}

	fmt.Println("Version:", rtn)

	// 他のファイルを先に書き換え、全部成功してから正である version ファイルを書く
	if err := write(rtn); err != nil {
		return err
	}
	if err := os.WriteFile(versionFile, []byte(rtn.String()+"\n"), 0644); err != nil {
		return err
	}
	fmt.Println("Write:", versionFile)
	return nil
}

func inquiryVersion(now *ver) *ver {
	major := now.addMajor()
	minor := now.addMinor()
	patch := now.addPatch()
	fmt.Fprintf(os.Stdout, inquiry, now, patch, minor, major)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	switch strings.TrimSpace(scanner.Text()) {
	case "", "1":
		return patch
	case "2":
		return minor
	case "3":
		return major
	}
	return &ver{-1, -1, -1}
}

// autoVersion は今のバージョンのタグがあれば patch を上げたバージョンを返す。
// タグが無ければ nil（そのまま使う）。
// 上げた先のタグもすでにあるときは、version とタグがずれているので止める
// （そのまま進めると古いコミットを指すタグが残り、新しいビルドにタグが付かない）。
func autoVersion(now *ver) (*ver, error) {
	exists, err := tagExists(now)
	if err != nil || !exists {
		return nil, err
	}
	next := now.addPatch()
	fmt.Printf("Tag v%s exists: bump to %s\n", now, next)

	exists, err = tagExists(next)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("tag v%s already exists: fix %s by hand", next, versionFile)
	}
	return next, nil
}

func tagExists(v *ver) (bool, error) {
	tag := "v" + v.String()
	out, err := exec.Command("git", "tag", "--list", tag).Output()
	if err != nil {
		return false, fmt.Errorf("git tag --list %s: %w", tag, err)
	}
	return strings.TrimSpace(string(out)) == tag, nil
}

func parseVersion() (*ver, error) {
	data, err := os.ReadFile(versionFile)
	if err != nil {
		return nil, err
	}
	v := parseVer(strings.TrimSpace(string(data)))
	if v.isError() {
		return nil, fmt.Errorf("invalid version in %s: %q", versionFile, strings.TrimSpace(string(data)))
	}
	return v, nil
}

type op struct {
	path string
	rg   *regexp.Regexp
}

func write(v *ver) error {
	ops := []op{
		{configYml, configRg},
		{packJsn, packRg},
	}

	// 全ファイルの置換結果を用意してから書き込む（途中で失敗しても中途半端に残さない）
	outs := make([][]byte, len(ops))
	for i, o := range ops {
		out, err := replaceVersion(o, v)
		if err != nil {
			return err
		}
		outs[i] = out
	}
	for i, o := range ops {
		if err := os.WriteFile(o.path, outs[i], 0644); err != nil {
			return err
		}
		fmt.Println("Write:", o.path)
	}
	return nil
}

// replaceVersion は最初に当たった 1 行だけを置き換える。
// 改行コード（CRLF / LF）は元のファイルのまま保つ。
func replaceVersion(o op, v *ver) ([]byte, error) {
	data, err := os.ReadFile(o.path)
	if err != nil {
		return nil, err
	}
	lines := strings.SplitAfter(string(data), "\n")
	for i, line := range lines {
		body := strings.TrimRight(line, "\r\n")
		if !o.rg.MatchString(body) {
			continue
		}
		eol := line[len(body):]
		lines[i] = o.rg.ReplaceAllString(body, "${1}"+v.String()+"${2}") + eol
		return []byte(strings.Join(lines, "")), nil
	}
	return nil, fmt.Errorf("version line not found: %s", o.path)
}

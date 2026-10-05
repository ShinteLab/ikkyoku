#!/usr/bin/env bash
# クラウド（Claude Code on the web の Linux コンテナ）でだけ使う、準備と検査の 1 コマンド。
#
#   bash _docs/skills/ikkyoku-cloud/cloud.sh setup   # wails3・bindings・npm（済んでいる段は飛ばす）
#   bash _docs/skills/ikkyoku-cloud/cloud.sh test    # go test ./...（Linux で組めない hotkey を差し替えて）
#   bash _docs/skills/ikkyoku-cloud/cloud.sh check   # test + Windows 向けの build/vet + Wails アプリ + tsc
#
# ⚠️ **出力は最後の 1 行だけ**（成功なら OK、失敗なら落ちた段とログの末尾）。中身はログファイルへ。
# 会話に入る出力がそのまま使用量になるので、黙らせてある。**ただし失敗は必ず言う**
# （黙って失敗すると、原因を探す手数のほうが高くつく）。
# ⚠️ **クラウド以外では何もしない**（`CLAUDE_CODE_REMOTE` が true でなければ 1 行出して終わる）。
set -u

if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  echo "ikkyoku-cloud: クラウドではないので何もしません"
  exit 0
fi

root=$(cd "$(dirname "$0")/../../.." && pwd)
work=${TMPDIR:-/tmp}/ikkyoku-cloud
mkdir -p "$work"
log=$work/cloud.log
: >"$log"
wails3=$(go env GOPATH)/bin/wails3

# step は 1 段を走らせ、失敗したら段の名前とログの末尾を出して終わる。
step() {
  local name=$1
  shift
  echo "== $name" >>"$log"
  if ! "$@" >>"$log" 2>&1; then
    echo "NG: $name（ログ: $log）"
    tail -25 "$log"
    exit 1
  fi
}

# overlay は Linux で組めない hotkey.go / hotkey_test.go の写しを作る（リポジトリは触らない）。
# golang.design/x/hotkey の Linux（X11）版には ModAlt / ModWin が無いので Mod1 / Mod4 に置き換える。
overlay() {
  local f out=$work/overlay.json sep=""
  printf '{"Replace":{' >"$out"
  for f in hotkey.go hotkey_test.go; do
    sed -e 's/hotkey\.ModAlt/hotkey.Mod1/g; s/hotkey\.ModWin/hotkey.Mod4/g' "$root/$f" >"$work/$f"
    printf '%s"%s":"%s"' "$sep" "$root/$f" "$work/$f" >>"$out"
    sep=","
  done
  printf '}}\n' >>"$out"
}

setup() {
  if [ ! -x "$wails3" ]; then
    # ⚠️ **CGO_ENABLED=0**。付けないと Linux の GTK / WebKit を探して落ちる（bindings の生成には要らない）。
    step wails3 env CGO_ENABLED=0 go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.26
  fi
  step bindings sh -c "cd '$root/_cmd/ikkyoku' && GOOS=windows '$wails3' generate bindings -ts -i"
  if [ ! -d "$root/_cmd/ikkyoku/frontend/node_modules" ]; then
    step npm sh -c "cd '$root/_cmd/ikkyoku/frontend' && npm ci --no-audit --no-fund"
  fi
}

gotest() {
  step overlay overlay
  # ⚠️ **xvfb-run を通すこと**。hotkey の X11 版は init で画面に繋ぎに行き、無いと panic する。
  # ⚠️ **ルートパッケージ（ikkyoku）は外す**。hotkey_test（キーコード）と config_test（パス区切り）が
  # Windows 前提で、Linux では落ちる（不具合ではない）。ルートの build/vet は check が Windows 向けで見る。
  step "go test" sh -c "cd '$root' && xvfb-run -a go test -overlay '$work/overlay.json' \$(go list ./... | grep -v '^github.com/ShinteLab/ikkyoku\$')"
}

check() {
  gotest
  step "go vet (windows)" sh -c "cd '$root' && GOOS=windows go build ./... && GOOS=windows go vet ./..."
  # frontend/dist が無いと embed で止まるので、無ければ空のまま一時的に作る。
  local dist=$root/_cmd/ikkyoku/frontend/dist made=""
  [ -d "$dist" ] || { mkdir -p "$dist" && touch "$dist/.keep" && made=1; }
  echo "== cmd (windows)" >>"$log"
  (cd "$root/_cmd/ikkyoku" && GOOS=windows go build -o /dev/null . && GOOS=windows go vet .) >>"$log" 2>&1
  local rc=$?
  [ -n "$made" ] && rm -rf "$dist"
  if [ $rc -ne 0 ]; then
    echo "NG: cmd (windows)（ログ: $log）"
    tail -25 "$log"
    exit 1
  fi
  setup
  step tsc sh -c "cd '$root/_cmd/ikkyoku/frontend' && npx tsc --noEmit"
}

case "${1:-}" in
  setup) setup ;;
  test) gotest ;;
  check) check ;;
  *)
    echo "使い方: cloud.sh setup|test|check"
    exit 2
    ;;
esac
echo "ikkyoku-cloud: ${1} OK（ログ: $log）"

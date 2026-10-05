---
name: ikkyoku-cloud
description: ikkyoku（一局）をクラウド（Claude Code on the web の Linux コンテナ。環境変数 CLAUDE_CODE_REMOTE=true）で触るときの準備と検査。Windows 向けのアプリを Linux で組む・テストする手順（hotkey の差し替え・xvfb-run・GOOS=windows・wails3 の bindings・tsc）を 1 コマンドにした cloud.sh と、使用量を抑えるための約束。「クラウドでテストしたい」「Linux で go test が通らない」「hotkey.ModAlt が undefined」「bindings が無い」ときに使う。⚠️ 手元（Windows）では使わない。
---

# ikkyoku-cloud — クラウドで触るとき

⚠️ **クラウドで動いているときだけ読む**（`CLAUDE_CODE_REMOTE=true`）。手元（Windows）での
ビルドと確認はスキル `ikkyoku-build` / `ikkyoku-verify`。

クラウドのコンテナは**Linux で、セッションごとに作り直される**（入れたものは次のセッションに残らない）。
ikkyoku は Windows のアプリなので、**そのままでは組めない・テストできないところ**がある。
それを埋める手順を `cloud.sh` にまとめた（2026-10-06）。

## 使い方（リポジトリ直下で）

```bash
bash _docs/skills/ikkyoku-cloud/cloud.sh test    # go test（ルートパッケージ以外）。数秒〜
bash _docs/skills/ikkyoku-cloud/cloud.sh check   # test + Windows 向け build/vet + Wails アプリ + bindings + tsc。初回は数分
bash _docs/skills/ikkyoku-cloud/cloud.sh setup   # wails3・bindings・npm だけ（済んでいる段は飛ばす）
```

- ⚠️ **出力は最後の 1 行だけ**（`… OK` か `NG: <段>` + ログの末尾）。中身は `/tmp/ikkyoku-cloud/cloud.log`。
  **失敗したときだけログを読むこと**（成功したログを読み返さない）
- ⚠️ **クラウド以外では何もしない**（1 行出して終わる）
- **リポジトリは触らない**（差し替えは `-overlay` の写し、`frontend/dist` は無ければ一時的に作って消す。
  bindings と node_modules は .gitignore 済み）
- ⚠️ **`cloud.sh` を CRLF で保存しないこと**（クラウドの bash が `$'\r': command not found` で動かない）。
  `.gitattributes` で固定する案は、`.gitignore` が `.*` を無視しているので入れていない

## なぜ使用量が減るのか

**会話に入る出力と、ツールを呼ぶ回数がそのまま使用量になる**（コマンドを打つたびに、
それまでの会話も含めて処理し直すため）。インストールにかかる時間そのものはほとんど効かない。

- ⚠️ **手探りで準備しないこと** —— 2026-10-06 のセッションでは、hotkey の差し替え・xvfb・
  wails3 の入れ方を探して十数回コマンドを打ち、`go mod tidy` の `go: downloading …` も何十行も会話に入った。
  `cloud.sh` なら 1 回・1 行
- ⚠️ **ただし黙って失敗させないこと** —— 原因を探す手数のほうが高くつく。だから「最後の 1 行 + 失敗時だけ末尾」
- **要らない段は回さない**（Go だけ触ったなら `test`。フロントや Service の引数を触ったら `check`）
- もっと減らすなら、環境の **Setup script**（タイトルバーの環境メニュー → Edit → Setup script）で
  `cloud.sh setup` を呼ぶ。Setup script の出力は会話に入らない。⚠️ **Setup script が走る時点で
  リポジトリが clone 済みか・どこにあるかは確かめていない**（試すなら、動かなければ外すだけ）。
  ⚠️ **リポジトリに `.claude/`（SessionStart hook）は置かない**（ルートの `AGENTS.md`）

## Linux で組めない・通らないもの（`cloud.sh` がやっていること）

| 何が | なぜ | どうしているか |
|---|---|---|
| ルートパッケージ（`ikkyoku`）と、それを import する `app` などが組めない | `golang.design/x/hotkey` の Linux（X11）版に `ModAlt` / `ModWin` が無い | `hotkey.go` / `hotkey_test.go` を `Mod1` / `Mod4` に置き換えた写しを `go test -overlay` で使う |
| 組めても `app` のテストが panic する | X11 版の hotkey は init で画面に繋ぎに行く | `xvfb-run -a`（仮想ディスプレイ）の上で回す |
| ルートパッケージのテストが落ちる | `hotkey_test`（キーコード）と `config_test`（`C:\` のパス区切り）が **Windows 前提** | ⚠️ **`test` から外している**（不具合ではない）。build/vet は Windows 向けに見る |
| `go build ./...` が Linux で落ちる | 上の hotkey | `GOOS=windows` で組む（PureGo なのでクロスで組める） |
| Wails アプリ（`_cmd/ikkyoku`）が embed で止まる | `frontend/dist` が無い | 無ければ空のフォルダを一時的に作る |
| `wails3` が入らない | 普通に入れると Linux の GTK / WebKit（cgo）を探す | `CGO_ENABLED=0 go install …@v3.0.0-beta.26`（bindings の生成には要らない） |
| bindings の生成 | — | `GOOS=windows wails3 generate bindings -ts -i` |

## クラウドではできないこと

- **アプリを起動して触ること**（画面キャプチャ・ホットキー・Wails のウィンドウ）。実機の確認は
  `ikkyoku-verify` の `checklist.md` に足して、手元で見てもらう
- **`_cmd/ikkyoku` のテスト**（Wails の Linux 版は cgo が要る）。`check` は build/vet まで
- **兄弟のリポジトリへの push**（suteme などは読むだけ。タグは手元で打ってもらう。
  ⚠️ **打ってもらったら、引く前にタグの位置を確かめること** —— スキル `ikkyoku-build` の「タグを付け直さない」）

//go:build !production

package main

// consoleLog はログを標準エラーにも出すか。
//
// 配るビルド（wails3 build / task build:embed）は -tags production を付ける。付いていない
// ＝ wails3 dev か go run / go build で動かしているので、ターミナルにも出す。
// ⚠️ **判定をビルドタグでしていること**（環境変数や引数では見ない）。配る exe は
// -H windowsgui で標準エラーの行き先が無いので、そちらで立てても意味が無い。
const consoleLog = true

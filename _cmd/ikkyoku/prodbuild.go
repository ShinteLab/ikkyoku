//go:build production

package main

// consoleLog は devbuild.go を参照。配るビルドはファイルにだけ出す。
const consoleLog = false

// devBuild は devbuild.go を参照。配るビルドは `version` ファイルの版をそのまま名乗る。
const devBuild = false

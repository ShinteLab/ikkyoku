//go:build embedmodel

package recognize

import _ "embed"

// 配布ビルド（`-tags embedmodel`）でだけ認識器を焼き込む。
//
// ⚠️ **ファイルが無いとビルドが通らない。** それが狙いで、手元のモデル（`task model:update`）を
// 入れないまま配布ビルドを作れてしまうより、ここで止まったほうがよい。

//go:embed model/predictor.bin.gz
var embeddedPredictorGZ []byte

//go:embed model/strip.bin.gz
var embeddedStripGZ []byte

//go:embed model/source.txt
var embeddedSource string

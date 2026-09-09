// 解析のイベントのペイロード（Go 側 `AnalyzeService` が流すもの）。
//
// **bindings から取れない。** これはメソッドの戻り値ではなくイベントのペイロードなので
// 生成の対象外（`CaptureResult` と同じ事情）。**Go 側を変えたらここも直すこと。**
//
// ⚠️ **2026-09-08 に `mainscreen.ts` から切り出した。** 解析の列を別ウィンドウへ
// 出せるようにしたので、**メイン画面と側の列の両方が同じ型を読む**必要がある。

// 解析の途中経過（Go 側 AnalyzeProgress / analyze.Progress）。
//
// **bindings から取れない。** これはメソッドの戻り値ではなくイベントのペイロードなので、
// 生成の対象外（CaptureResult と同じ事情）。**Go 側を変えたらここも直すこと。**
//
// ⚠️ **score.cp / score.mate は先手視点で、label は Go 側が組み立てた文字列。**
// フロントで符号をいじったり書式を作り直したりしないこと（2 か所に散る）。
//
// ⚠️ **lines は候補手の配列**（MultiPV）。**「最善手 1 個」に畳まないこと** ——
// 次善手を辿るのが構想の中心で、ここが複数本になれることが Phase 5 の前提。
// 今の自作 engine は MultiPV を持たないので 1 本しか来ないが、**それを異常扱いしない**
// （engine/TODO.md の 1 が入れば増える）。
export interface AnalyzeLine {
  rank: number;
  // depth はこの候補が届いたときの深さ。
  //
  // ⚠️ **候補ごとに違うことがある。** MultiPV では順位ごとに別々の info が来て、
  // 進み方も揃わない（見出しの「深さ」は一番深いところ）。
  depth: number;
  // winRate は**先手の勝率**（0.0〜1.0）。勝率バーが読む値。
  //
  // ⚠️ **cp から計算し直さないこと。** 式（1/(1+exp(-cp/定数))）も定数も
  // Go 側が持っている（`analyze.WinRate` / 設定タブのポナンザ定数）ので、
  // ここで計算すると**定数を変えたときに片方だけ古い値で描く**。
  score: { cp: number; mate: number; label: string; winRate: number };
  // moves は USI 表記（"8h2b+"）。**手を辿るのに使うのはこちら。**
  moves: string[];
  // text は日本語表記（"▲２二角成"）。**画面に出すのはこちら。**
  //
  // ⚠️ **フロントで組み立て直さないこと。** USI の手には駒種が書いていない
  // （"8h2b+" のどこにも「角」が無い）ので、盤と突き合わせないと作れない。
  // 変換は Go 側（core/kifu）が解析した局面から 1 手ずつ盤を進めて行っている。
  // **moves と同じ長さ**で、変換できなかった手はその USI がそのまま入る。
  text: string[];
}

export interface AnalyzeProgress {
  seq: number;
  // ⚠️ **どのエンジンが喋ったか。** 複数のエンジンが同時に走るので、
  // **seq だけでは行き先を決められない**（seq は解析の世代であって、エンジンの区別ではない）。
  engineId: string;
  // engineName はそのエンジンが名乗った名前（`id name`）。
  engineName: string;
  done: boolean;
  progress: {
    depth: number;
    nodes: number;
    elapsedMs: number;
    lines: AnalyzeLine[];
  };
}

// 解析の結末（analyze:done のみ）。**起動にかかった時間は done でしか分からない。**
export interface AnalyzeDone extends AnalyzeProgress {
  startupMs: number;
  // interrupted は**考える時間を使い切る前に外から止められた**か。
  //
  // ⚠️ **「打ち切られた」一般ではない。** 時間切れも `stop` を送って終わるので、
  // それと区別できるように Go 側が**頼んだ秒数に届いたか**で判断している
  // （`AnalyzeProgress.Interrupted`）。**フロントで計算し直さないこと。**
  //
  // ⚠️ **連続解析はこれを見て止まる。** 打ち切られた解析も done を出す
  // （設計原則3）ので、これを見ないと**「1 手ぶん終わった」と読んで次へ進む。**
  interrupted: boolean;
  // ⚠️ **接続を使い回したか。** `startupMs` が 0 のとき、「起動が速かった」のか
  // 「払っていない」のかはこれでしか区別できない。
  reused: boolean;
}

// ⚠️ **1 つのエンジンが落ちても、他のエンジンの解析は続く**（設計原則3）。
// **解析全体の失敗として扱わないこと。**
export interface AnalyzeFailure {
  seq: number;
  engineId: string;
  error: string;
}


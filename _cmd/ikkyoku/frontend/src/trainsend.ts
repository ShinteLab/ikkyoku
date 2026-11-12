// 訂正データの送信（suteme への還元。2026-11-13 に `mainscreen.ts` から切り出した）。
//
// **確定してから出す。** 訂正の途中の盤面を送る意味が無いので、訂正モードを
// 抜けたときだけボタンが現れる。設定で有効にしていなければ行ごと出さない。
//
// 送るのに要るのは 3 点組（撮った PNG のパス・正解 SFEN・**画像の中での盤面の矩形**）。
// 矩形は認識結果の `Debug.Region` そのもの。**認識に失敗した画像は送れない**
// （盤の位置が分からないと suteme が学習サンプルを切り出せない）。
//
// ⚠️ **送るものの持ち主は `mainscreen.ts`**（訂正タブの局面と撮った 1 枚）。ここは
// `current()` で読むだけで、**写しを持たない**（持つと片方だけ古くなる）。
// ⚠️ **自動では送らない**（2026-08-07 の決定。人が直した 1 マス以外は推論結果のままなので、
// 自分の出力を正解として食う）。送るのはボタンを押したときだけ。
import { TrainingService } from "../bindings/github.com/ShinteLab/ikkyoku/app";

// 撮った PNG の座標系での盤面矩形（`Debug.Region` から `ImageBounds.Min` を引いたもの）。
// **使い道は 2 つ**——suteme へ送る学習サンプルの切り出し位置と、訂正タブの参照画像の
// 切り取り。**同じ 1 つの値から両方を出すこと**（別々に持つと片方だけずれる）。
export type ShotRegion = { x1: number; y1: number; x2: number; y2: number };

// TrainSendSource は送るもの。**呼ばれるたびに今の値を返すこと**（控えない）。
export interface TrainSendSource {
  // loaded は訂正タブに局面が載っているか。
  loaded: boolean;
  // path は撮った PNG のフルパス（空なら送れない）。
  path: string;
  // sfen は送る SFEN（`EditState.labelSfen`）。
  sfen: string;
  // notes は「送る SFEN のために妥協した点」（手番を先手にした・先後未決の持ち駒を落とした）。
  notes: string[];
  // region は撮ったときの盤面矩形（無ければ送れない）。
  region: ShotRegion | null;
}

export interface TrainSendOptions {
  // root は訂正タブの「訂正データを送信」の行（`#train-row`）。
  root: HTMLElement;
  current(): TrainSendSource;
}

export interface TrainSendHandle {
  // setEnabled は設定「訂正盤面を suteme に登録する」（⚠️ **既定の解決は Go 側**。来た値を入れるだけ）。
  setEnabled(on: boolean): void;
  // sync は行を出すかどうかを決め直す（局面・撮った 1 枚・設定のどれかが変わったら呼ぶ）。
  sync(): void;
  // clearStatus は送った結果の文を消す（撮った 1 枚が替わったとき）。
  clearStatus(): void;
}

export function mountTrainSend(opts: TrainSendOptions): TrainSendHandle {
  const { root, current } = opts;
  const trainRow = root;
  const trainSend = root.querySelector<HTMLButtonElement>("#train-send")!;
  const trainNote = root.querySelector<HTMLElement>("#train-note")!;
  const trainSendStatus = root.querySelector<HTMLElement>("#train-send-status")!;
  let trainEnabled = false;

  const syncTrain = () => {
    const c = current();
    const ready = c.loaded && !!c.path && !!c.region;
    trainRow.hidden = !trainEnabled || !ready;
    if (trainRow.hidden) {
      return;
    }
    // 送れる状態でも、手番や駒台の先後が未決のことはある。**止めない**
    // （学習に使うのは盤面部分なので、それでも価値がある。設計原則3）。
    // ただし**何が落ちるかは先に出す**（持ち駒が黙って落ちるのを一度やっている）。
    trainSend.disabled = false;
    trainNote.textContent = c.notes.join(" / ");
  };

  const sendTraining = async () => {
    const c = current();
    const lastRegion = c.region;
    if (!lastRegion) {
      return;
    }
    trainSend.disabled = true;
    trainSendStatus.classList.remove("is-error");
    trainSendStatus.textContent = "送信しています…";
    try {
      const r = await TrainingService.Send(
        c.path,
        c.sfen,
        lastRegion.x1,
        lastRegion.y1,
        lastRegion.x2,
        lastRegion.y2,
      );
      // **重複は失敗ではない**（suteme は画像のハッシュで再送を弾き、既存の ID を返す）。
      trainSendStatus.textContent = r.duplicate
        ? `この画像は登録済みです (${r.id})`
        : `登録しました (${r.id})。suteme の解析タブで確認すると学習に使われます`;
    } catch (err) {
      trainSendStatus.textContent = `登録できませんでした: ${String(err instanceof Error ? err.message : err)}`;
      trainSendStatus.classList.add("is-error");
    } finally {
      trainSend.disabled = false;
    }
  };

  trainSend.addEventListener("click", () => {
    void sendTraining();
  });

  return {
    setEnabled: (on) => {
      trainEnabled = on;
    },
    sync: syncTrain,
    clearStatus: () => {
      trainSendStatus.textContent = "";
      trainSendStatus.classList.remove("is-error");
    },
  };
}

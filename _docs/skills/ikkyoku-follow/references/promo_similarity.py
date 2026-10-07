"""成・不成を見た目で決めるしきい値を、録画から計り直す（2026-10-07）。

追従の録画（<captures>/follow/<日時>/）の、手を足した 1 枚どうしを並べ、指した駒の
「動く前のマス」（前の 1 枚）と「動いた先のマス」（その手の 1 枚）の見た目の近さを測る。
アプリと同じ量（明るさの正規化相互相関。マスの内側＝各辺 12% を削る・マスの幅の 6% までずらして
いちばん高い値。`ikkyoku.PieceSimilarity`）を出すので、成った手と成っていない手の分かれ目から
`app/followstudy.go` の `promoChangedBelow` / `promoSameFrom` を決め直せる。

⚠️ マス割りはログの盤の矩形（「追う盤を決めました board=(x0,y0)-(x1,y1)」）を 9 等分したもの。
アプリは suteme のマス割りを使うので、値は少しずれる（外周のマスで低めに出ることがある）。
⚠️ 録画の手の名前（`042-7g7f.png` / `030-P@5e.png` / `029-2f2c_P@8f-guess.png`）が正しい前提。
読み違えた成駒は成っていない手として記録されている（それを見つけるのも目的の 1 つ）。

使い方（numpy と Pillow が要る）:

    python promo_similarity.py --logs "<ログのある場所>/ikkyoku_20261007.log" [--logs ...]
                               [--follow "<captures>/follow"] [--csv out.csv]

    --logs    ikkyoku_<日付>.log（何個でも。glob も可）。録画のディレクトリ名と盤の矩形・目線をここから引く
    --follow  録画の親ディレクトリ。既定は環境変数 IKKYOKU_FOLLOW_DIR、無ければ %APPDATA%/ikkyoku/captures/follow
    --csv     1 手 1 行で書き出す（近さ・成ったか・録画・ファイル・手）

出力は近さの低い順。成った手（PROMO）が上に固まり、成っていない手が 0.9 前後より上に来るのが期待どおり。
"""

import argparse
import csv
import glob
import os
import re
import sys

import numpy as np
from PIL import Image

TRIM = 0.12   # `ikkyoku.PieceTrim`
SHIFT = 0.06  # `ikkyoku.PieceShift`

BOARD_RE = re.compile(r"追う盤を決めました board=\((\d+),(\d+)\)-\((\d+),(\d+)\).*follow.(\S+)")
ROT_RE = re.compile(r"rotated=(true|false)")


def sessions(log_files):
    """ログから録画ごとの盤の矩形と目線を拾う。"""
    out, cur = {}, None
    for lf in log_files:
        with open(lf, encoding="utf-8", errors="replace") as f:
            for line in f:
                m = BOARD_RE.search(line)
                if m:
                    cur = os.path.basename(m.group(5).strip().rstrip('"'))
                    out[cur] = {"rect": tuple(map(int, m.group(1, 2, 3, 4))), "rot": None}
                    continue
                if cur and out[cur]["rot"] is None:
                    m = ROT_RE.search(line)
                    if m:
                        out[cur]["rot"] = m.group(1) == "true"
    return out


_cache = {}


def gray(path):
    if path not in _cache:
        _cache[path] = np.asarray(Image.open(path).convert("L"), dtype=float)
    return _cache[path]


def cell(path, sq, rect, rot, margin=0):
    """USI のマス（"7g"。解析の向き）の内側を切り出す。rot なら撮った画像は上下逆。"""
    x0, y0, x1, y1 = rect
    cw, ch = (x1 - x0) / 9, (y1 - y0) / 9
    f, r = int(sq[0]), "abcdefghi".index(sq[1])
    if rot:
        f, r = 10 - f, 8 - r
    col = 9 - f
    a, b = int(x0 + col * cw), int(y0 + r * ch)
    px, py = int(cw * TRIM), int(ch * TRIM)
    return gray(path)[b + py - margin:b + int(ch) - py + margin, a + px - margin:a + int(cw) - px + margin]


def ncc(p, q):
    p = p - p.mean()
    q = q - q.mean()
    d = np.sqrt((p * p).sum() * (q * q).sum())
    return (p * q).sum() / d if d > 1e-9 else None


def best(pa, sa, pb, sb, rect, rot, shift):
    a = cell(pa, sa, rect, rot)
    b = cell(pb, sb, rect, rot, shift)
    h, w = a.shape
    top = None
    for dy in range(2 * shift + 1):
        for dx in range(2 * shift + 1):
            bc = b[dy:dy + h, dx:dx + w]
            if bc.shape == a.shape:
                v = ncc(a, bc)
                if v is not None and (top is None or v > top):
                    top = v
    return top


def main():
    # Windows の端末は cp932 なので、日本語が化けないように UTF-8 で書く。
    for stream in (sys.stdout, sys.stderr):
        try:
            stream.reconfigure(encoding="utf-8")
        except (AttributeError, ValueError):
            pass
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--logs", action="append", required=True, help="ikkyoku_<日付>.log（glob 可。何個でも）")
    ap.add_argument("--follow", default=None, help="録画の親ディレクトリ（<captures>/follow）")
    ap.add_argument("--csv", default=None, help="1 手 1 行で書き出す先")
    args = ap.parse_args()

    logs = sorted({p for pat in args.logs for p in glob.glob(pat)})
    if not logs:
        sys.exit("ログが見つかりません: " + ", ".join(args.logs))
    follow = args.follow or os.environ.get("IKKYOKU_FOLLOW_DIR") or os.path.join(
        os.environ.get("APPDATA", ""), "ikkyoku", "captures", "follow")
    if not os.path.isdir(follow):
        sys.exit("録画のディレクトリがありません: " + follow)

    rows = []
    for name, info in sessions(logs).items():
        d = os.path.join(follow, name)
        if not os.path.isdir(d) or info["rot"] is None:
            continue
        rect, rot = info["rect"], info["rot"]
        shift = max(1, round((rect[2] - rect[0]) / 9 * SHIFT))
        files = sorted(f for f in os.listdir(d) if re.match(r"\d{3}-", f))
        prev = os.path.join(d, "master.png")
        for f in files:
            body = re.sub(r"^\d{3}-", "", f[:-4]).replace("-guess", "")
            moves = body.split("_")
            path = os.path.join(d, f)
            for j, mv in enumerate(moves):
                # 打つ手・前の 1 枚が無い手は比べられない
                if len(mv) < 4 or mv[1] == "@" or not os.path.exists(prev):
                    continue
                fr, to = mv[0:2], mv[2:4]
                # 動く前のマスを先に別の手が動かした・動いた先のマスを後の手が動かした手は比べられない
                # （アプリの `followPromoPairs` と同じ条件）
                if any(fr in (m2[0:2], m2[2:4]) for m2 in moves[:j]):
                    continue
                if any(to in (m2[0:2], m2[2:4]) for m2 in moves[j + 1:]):
                    continue
                try:
                    # 前の 1 枚と今の 1 枚で、動く前のマスが変わっていない（＝録画の間が空きすぎて
                    # 前の 1 枚にまだ駒が来ていない）組は外す
                    same = best(prev, fr, path, fr, rect, rot, 2)
                    if same is not None and same > 0.9:
                        continue
                    s = best(prev, fr, path, to, rect, rot, shift)
                except (OSError, ValueError, IndexError):
                    continue
                if s is not None:
                    rows.append((s, mv.endswith("+"), name, f, mv))
            prev = path

    rows.sort()
    for s, promo, n, f, mv in rows:
        print(f"{s:.3f} {'PROMO' if promo else '     '} {n} {f} {mv}")
    if args.csv:
        with open(args.csv, "w", newline="", encoding="utf-8") as out:
            w = csv.writer(out)
            w.writerow(["sim", "promoted", "session", "file", "move"])
            for s, promo, n, f, mv in rows:
                w.writerow([f"{s:.4f}", int(promo), n, f, mv])
    p = [r[0] for r in rows if r[1]]
    q = [r[0] for r in rows if not r[1]]
    if p:
        print(f"成った手 {len(p)}: {min(p):.3f}〜{max(p):.3f}", file=sys.stderr)
    if q:
        print(f"成っていない手 {len(q)}: {min(q):.3f}〜{max(q):.3f}（0.9 以上 {sum(v >= 0.9 for v in q)}）", file=sys.stderr)


if __name__ == "__main__":
    main()

"""追従のログを手ごとに集計する（使い捨て）。

python follow_report.py <ikkyoku_YYYYMMDD.log>
最後の「追う盤を決めました」から後を見る。
各「撮った盤面を本譜の先に繋ぎました」について:
  - 動き出した時刻（直前の追加より後の、最初の「動いているので止まるのを待っています」）
  - 足した時刻・遅れ（秒。ログは秒単位）
  - 経路（速い経路 / 81 マス）と手
"""
import re
import sys
import datetime

lines = open(sys.argv[1], encoding='utf-8').read().splitlines()
start = max(i for i, l in enumerate(lines) if '追う盤を決めました' in l)
lines = lines[start:]


def t(l):
    return datetime.datetime.fromisoformat(l[:25])


appear = None
fast_moves = None
full_moves = None
fallbacks = 0
rows = []
for l in lines:
    if '動いているので止まるのを待っています' in l and appear is None:
        appear = t(l)
    m = re.search(r'変わったマスで手を割り出しました moves=\[([^\]]*)\]', l)
    if m:
        fast_moves = m.group(1)
    if '変わったマスでは手が決まりません' in l or '変わったマスが多いので' in l:
        fallbacks += 1
    m = re.search(r'候補を並べました .* moves=\[([^\]]*)\]', l)
    if m and m.group(1):
        full_moves = m.group(1)
    m = re.search(r'撮った盤面を本譜の先に繋ぎました moves=(\d+)', l)
    if m:
        at = t(l)
        path = '速い' if fast_moves else '81マス'
        mv = fast_moves or full_moves or '?'
        lag = (at - appear).total_seconds() if appear else None
        rows.append((at.strftime('%H:%M:%S'), path, mv, int(m.group(1)), lag, fallbacks))
        appear, fast_moves, full_moves, fallbacks = None, None, None, 0

print(f'{"足した時刻":<10} {"経路":<6} {"手":<22} {"手数":>4} {"遅れ(秒)":>8} {"落ちた回数":>8}')
for r in rows:
    lag = '-' if r[4] is None else f'{r[4]:.0f}'
    print(f'{r[0]:<10} {r[1]:<6} {r[2]:<22} {r[3]:>4} {lag:>8} {r[5]:>8}')
lags = [r[4] for r in rows if r[4] is not None]
fast = sum(1 for r in rows if r[1] == '速い')
if rows:
    print(f'\n追加 {len(rows)} 回（速い経路 {fast} 回）・足した手 {sum(r[3] for r in rows)}')
if lags:
    lags.sort()
    print(f'遅れ: 中央値 {lags[len(lags)//2]:.0f} 秒・最大 {lags[-1]:.0f} 秒')

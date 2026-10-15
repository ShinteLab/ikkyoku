package main

import (
	"fmt"
	"io"
	"time"
)

// progressEvery は進み具合の行を書き換える間隔（これより速く書き換えても読めない）。
const progressEvery = 500 * time.Millisecond

// progress は進み具合を 1 行で書き換えながら出す（標準エラー。2026-10-11）。
//
// 長い録画では 1 段目も 2 段目も何分もかかるので、黙っていると止まっているのか分からない。`\r` で同じ行を書き換え、
// ほかの表示（盤を決めた・⚠ など）を出すときは先に改行する（`logf` / `breakLine`）。
type progress struct {
	w     io.Writer
	label string
	// total は全体の量、done は済んだ量（1 段目はコマ数、2 段目は秒）。
	total, done float64
	// detail は % のあとに添える文（済んだ量・変化の数など）。
	detail func() string
	last   time.Time
	// active は行の途中か（書き換え中の行があるか）。
	active bool
}

func newProgress(w io.Writer, label string, total float64, detail func() string) *progress {
	return &progress{w: w, label: label, total: total, detail: detail}
}

// set は済んだ量を done にし、間隔が空いていれば行を書き換える。
func (p *progress) set(done float64) {
	p.done = done
	if time.Since(p.last) >= progressEvery {
		p.print()
	}
}

// percent は済んだ割合（0〜100。全体が分からなければ 0）。
func (p *progress) percent() float64 {
	if p.total <= 0 {
		return 0
	}
	return min(100, p.done/p.total*100)
}

func (p *progress) print() {
	d := ""
	if p.detail != nil {
		d = p.detail()
	}
	fmt.Fprintf(p.w, "\r%s %5.1f%%%s", p.label, p.percent(), d)
	p.active, p.last = true, time.Now()
}

// breakLine は書き換え中の行があれば改行する（このあと別の行を出すため）。
func (p *progress) breakLine() {
	if p != nil && p.active {
		fmt.Fprintln(p.w)
		p.active = false
	}
}

// logf は書き換え中の行を改行してから 1 行出す（次の set でまた進み具合の行を書く）。
func (p *progress) logf(format string, args ...any) {
	p.breakLine()
	fmt.Fprintf(p.w, format, args...)
}

// finish は済んだ量を全体にして最後の行を出し、改行する。
func (p *progress) finish() {
	p.done = p.total
	p.print()
	p.breakLine()
}

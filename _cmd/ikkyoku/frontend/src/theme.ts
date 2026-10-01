// 画面の配色（ダーク / ライト。2026-10-02）。
//
// **配色の中身は `style.css` の冒頭のトークン**にあり、ここは `<html data-theme>` を
// 入れるだけ（"dark" / "light"）。**どの窓も同じことをする**ので、`main.ts` が
// 窓の種類によらず一度だけ呼ぶ（枠・メイン画面・切り離した 3 つの窓）。
//
// ⚠️ **選ばれている値は Go 側（設定 `theme`）が持つ。** 既定の解決（知らない値・空は
// ダーク）も Go 側の `NormalizeTheme` で、ここに既定を書かないこと。
// ⚠️ **"system" をライトかダークに決めるのはここだけ** —— OS の設定は WebView の
// `prefers-color-scheme` でしか読めない。OS 側で切り替えたら、その場で追随する。
// ⚠️ **ほかの窓へは `theme:changed` で知らせる**（設定タブで変えた窓が出す）。
// `settings:changed` に乗せないのは、あちらを受けた窓は設定を丸ごと読み直すため。

import { Events } from "@wailsio/runtime";
import { SettingsService } from "../bindings/github.com/ShinteLab/ikkyoku/app";

const prefersLight = window.matchMedia("(prefers-color-scheme: light)");

// 前回の配色を覚えておく鍵。**起動直後のちらつきを消すためだけの控え**で、
// 正しい値はすぐに Go から届いて上書きされる（読めなくても困らない）。
const CACHE_KEY = "ikkyoku.theme";

let chosen = "";

const resolve = (theme: string): string => {
  if (theme === "system") {
    return prefersLight.matches ? "light" : "dark";
  }
  return theme;
};

// applyTheme は配色を当てる（Go から返った値をそのまま渡すこと）。
export const applyTheme = (theme: string): void => {
  if (!theme) {
    return;
  }
  chosen = theme;
  document.documentElement.dataset.theme = resolve(theme);
  try {
    localStorage.setItem(CACHE_KEY, theme);
  } catch {
    /* 控えが書けなくても、次の起動で一瞬ダークが見えるだけ。 */
  }
};

// mountTheme は窓が開いたときに一度だけ呼ぶ。
export const mountTheme = (): void => {
  try {
    const cached = localStorage.getItem(CACHE_KEY);
    if (cached) {
      applyTheme(cached);
    }
  } catch {
    /* 読めなければ Go からの値を待つ。 */
  }
  prefersLight.addEventListener("change", () => {
    if (chosen === "system") {
      applyTheme(chosen);
    }
  });
  Events.On("theme:changed", (event: { data: string }) => applyTheme(event.data));
  void SettingsService.Settings()
    .then((s) => applyTheme(s.theme))
    .catch(() => {
      /* 読めなければ今の配色のまま（設計原則3）。 */
    });
};

// NekoDrop theme switcher.
//
// A tiny, dependency-free colour-scheme layer shared by every page. The visitor
// can pick "auto", "dark" or "light"; "auto" follows the operating system's
// preference and live-updates when that preference changes. The chosen mode is
// remembered in a cookie. The effective theme is exposed as a data-theme
// attribute on <html>, which the stylesheet keys its colour variables off.
(function () {
  "use strict";

  const COOKIE_KEY = "nekodrop_theme";
  const COOKIE_MAX_AGE = 60 * 60 * 24 * 365; // one year, in seconds
  const MODES = { auto: true, dark: true, light: true };

  function readCookie(name) {
    const match = document.cookie.match(
      new RegExp("(?:^|; )" + name + "=([^;]*)")
    );
    return match ? decodeURIComponent(match[1]) : null;
  }

  function writeCookie(name, value) {
    try {
      document.cookie =
        name +
        "=" +
        encodeURIComponent(value) +
        "; path=/; max-age=" +
        COOKIE_MAX_AGE +
        "; SameSite=Lax";
    } catch (e) {
      /* ignore */
    }
  }

  const media =
    window.matchMedia && window.matchMedia("(prefers-color-scheme: light)");

  // The visitor's preference: "auto" (default), "dark" or "light".
  let mode = readCookie(COOKIE_KEY);
  if (!MODES[mode]) mode = "auto";

  // Resolve the preference to the concrete theme actually applied.
  function effective() {
    if (mode === "light" || mode === "dark") return mode;
    return media && media.matches ? "light" : "dark";
  }

  function apply() {
    document.documentElement.setAttribute("data-theme", effective());
  }

  // Apply immediately (before first paint when loaded from <head>).
  apply();

  function setTheme(next) {
    if (!MODES[next]) return;
    mode = next;
    writeCookie(COOKIE_KEY, next);
    apply();
    syncSelectors();
    document.dispatchEvent(
      new CustomEvent("nekodrop:themechange", { detail: { mode: mode, theme: effective() } })
    );
  }

  // Track OS changes so "auto" stays in sync without a reload.
  if (media) {
    const onChange = function () {
      if (mode === "auto") apply();
    };
    if (media.addEventListener) media.addEventListener("change", onChange);
    else if (media.addListener) media.addListener(onChange);
  }

  function syncSelectors() {
    document.querySelectorAll("select.theme-select").forEach((sel) => {
      sel.value = mode;
    });
  }

  function bindSelectors() {
    document.querySelectorAll("select.theme-select").forEach((sel) => {
      sel.value = mode;
      sel.addEventListener("change", () => setTheme(sel.value));
    });
  }

  window.NekoTheme = {
    setTheme: setTheme,
    apply: apply,
    get mode() {
      return mode;
    },
    get theme() {
      return effective();
    },
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", bindSelectors);
  } else {
    bindSelectors();
  }
})();

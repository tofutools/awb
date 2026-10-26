// A classic script restores appearance before rendering; keep the key and accepted
// values aligned with preferences.ts. Storage may be forbidden by the browser.
(() => {
  try {
    const theme = localStorage.getItem("awb.theme");
    if (theme === "light" || theme === "dark") {
      document.documentElement.dataset.theme = theme;
    }
  } catch {
    // CSS follows the browser when storage is unavailable.
  }
})();

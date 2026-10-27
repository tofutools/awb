// A classic script restores appearance before rendering; keep the key and accepted
// values aligned with preferences.ts. Storage may be forbidden by the browser.
(() => {
  try {
    const theme = localStorage.getItem("awb.theme");
    if (theme === "light" || theme === "dark") {
      document.documentElement.dataset.theme = theme;
      for (const meta of document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]')) {
        meta.media = meta.dataset.theme === theme ? "all" : "not all";
      }
    }
  } catch {
    // CSS follows the browser when storage is unavailable.
  }
})();

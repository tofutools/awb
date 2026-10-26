import { applyTheme, preferenceStorage, readTheme } from "./preferences.js";

// Restore the browser-local appearance independently of application/API loading.
applyTheme(readTheme(preferenceStorage(window)));

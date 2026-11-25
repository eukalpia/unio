// Runs before the app paints so the chosen theme is applied without a flash.
// It is an external file because the Content Security Policy forbids inline scripts.
(function () {
  try {
    var stored = window.localStorage.getItem("unio-theme");
    var dark =
      stored === "dark" ||
      (stored !== "light" && window.matchMedia("(prefers-color-scheme: dark)").matches);
    document.documentElement.classList.toggle("dark", dark);
    document.documentElement.style.colorScheme = dark ? "dark" : "light";
  } catch (error) {
    void error;
  }
})();

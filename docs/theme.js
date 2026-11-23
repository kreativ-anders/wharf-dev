// Light or dark; with no choice stored, the system's (dev/design-principles.md §4).
// INFO: loaded without defer in <head>, so a stored choice applies before the
// first paint instead of flashing the system theme.
(function () {
  var root = document.documentElement;
  var key = "theme";
  var dark = window.matchMedia("(prefers-color-scheme: dark)");

  function system() { return dark.matches ? "dark" : "light"; }

  function stored() {
    try {
      var v = localStorage.getItem(key);
      return v === "light" || v === "dark" ? v : null;
    } catch (e) { return null; }
  }

  function current() { return stored() || system(); }

  // INFO: the browser bar's colour is picked by each meta's media query, which
  // only sees the system theme — so a chosen theme gives both metas its colour.
  var bars = {};
  document.querySelectorAll('meta[name="theme-color"]').forEach(function (m) {
    var scheme = /dark/.test(m.media) ? "dark" : "light";
    bars[scheme] = m.content;
    m.setAttribute("data-scheme", scheme);
  });

  function apply(choice) {
    if (choice) root.setAttribute("data-theme", choice);
    else root.removeAttribute("data-theme");
    document.querySelectorAll('meta[name="theme-color"]').forEach(function (m) {
      m.content = bars[choice || m.getAttribute("data-scheme")];
    });
  }

  apply(stored());

  document.addEventListener("DOMContentLoaded", function () {
    var group = document.querySelector(".theme");
    if (!group) return;
    var buttons = group.querySelectorAll("button");

    function mark() {
      var choice = current();
      buttons.forEach(function (b) { b.setAttribute("aria-pressed", String(b.value === choice)); });
    }

    buttons.forEach(function (b) {
      b.addEventListener("click", function () {
        // INFO: picking the theme the system already shows forgets the choice,
        // so the page follows the system again instead of pinning it.
        var choice = b.value === system() ? null : b.value;
        try {
          if (choice) localStorage.setItem(key, choice);
          else localStorage.removeItem(key);
        } catch (e) {}
        apply(choice);
        mark();
      });
    });

    // INFO: while nothing is stored the page follows the system, so the
    // pressed button has to follow it too.
    dark.addEventListener("change", mark);

    mark();
    // INFO: without script the switch could do nothing, so it stays hidden.
    group.hidden = false;
  });
})();

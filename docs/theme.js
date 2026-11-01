// System, light or dark, like the app's own setting (dev/design-principles.md §4).
// INFO: loaded without defer in <head>, so a stored choice applies before the
// first paint instead of flashing the system theme.
(function () {
  var root = document.documentElement;
  var key = "theme";

  function stored() {
    try { return localStorage.getItem(key) || "system"; } catch (e) { return "system"; }
  }

  // INFO: the browser bar's colour is picked by each meta's media query, which
  // only sees the system theme — so a chosen theme gives both metas its colour.
  var bars = {};
  document.querySelectorAll('meta[name="theme-color"]').forEach(function (m) {
    var scheme = /dark/.test(m.media) ? "dark" : "light";
    bars[scheme] = m.content;
    m.setAttribute("data-scheme", scheme);
  });

  function apply(choice) {
    var explicit = choice === "light" || choice === "dark";
    if (explicit) root.setAttribute("data-theme", choice);
    else root.removeAttribute("data-theme");
    document.querySelectorAll('meta[name="theme-color"]').forEach(function (m) {
      m.content = bars[explicit ? choice : m.getAttribute("data-scheme")];
    });
  }

  apply(stored());

  document.addEventListener("DOMContentLoaded", function () {
    var group = document.querySelector(".theme");
    if (!group) return;
    var buttons = group.querySelectorAll("button");

    function mark(choice) {
      buttons.forEach(function (b) { b.setAttribute("aria-pressed", String(b.value === choice)); });
    }

    buttons.forEach(function (b) {
      b.addEventListener("click", function () {
        try {
          if (b.value === "system") localStorage.removeItem(key);
          else localStorage.setItem(key, b.value);
        } catch (e) {}
        apply(b.value);
        mark(b.value);
      });
    });

    mark(stored());
    // INFO: without script the switch could do nothing, so it stays hidden.
    group.hidden = false;
  });
})();

// System, light or dark, like the app's own setting (dev/design-principles.md §4).
// INFO: loaded without defer in <head>, so a stored choice applies before the
// first paint instead of flashing the system theme.
(function () {
  var root = document.documentElement;
  var key = "theme";

  function stored() {
    try { return localStorage.getItem(key) || "system"; } catch (e) { return "system"; }
  }

  function apply(choice) {
    if (choice === "light" || choice === "dark") root.setAttribute("data-theme", choice);
    else root.removeAttribute("data-theme");
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

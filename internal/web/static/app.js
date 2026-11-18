(function () {
  var body = document.body;

  document.addEventListener("click", function (event) {
    var toggle = event.target.closest("[data-nav-toggle]");
    if (toggle) {
      var open = body.classList.toggle("nav-open");
      toggle.setAttribute("aria-expanded", open ? "true" : "false");
      return;
    }
    if (body.classList.contains("nav-open") && !event.target.closest("#sidebar")) {
      body.classList.remove("nav-open");
    }

    var copy = event.target.closest("[data-copy]");
    if (copy) {
      var text = copy.getAttribute("data-copy");
      var done = function () {
        var label = copy.querySelector("[data-copy-label]");
        var previous = label ? label.textContent : "";
        if (label) label.textContent = "Copiado";
        copy.classList.add("copied");
        setTimeout(function () {
          if (label) label.textContent = previous;
          copy.classList.remove("copied");
        }, 1600);
      };
      if (navigator.clipboard && window.isSecureContext) {
        navigator.clipboard.writeText(text).then(done);
      } else {
        var area = document.createElement("textarea");
        area.value = text;
        area.setAttribute("readonly", "");
        area.style.position = "absolute";
        area.style.left = "-9999px";
        document.body.appendChild(area);
        area.select();
        try { document.execCommand("copy"); done(); } catch (e) {}
        document.body.removeChild(area);
      }
    }
  });

  document.addEventListener("submit", function (event) {
    var form = event.target;
    var confirmText = form.getAttribute("data-confirm");
    if (confirmText && !window.confirm(confirmText)) {
      event.preventDefault();
      return;
    }
    var button = form.querySelector("button[type=submit]:not([data-no-loading])");
    if (button && !event.defaultPrevented) {
      setTimeout(function () { button.classList.add("is-loading"); button.setAttribute("aria-busy", "true"); }, 0);
    }
  });

  window.addEventListener("pageshow", function () {
    document.querySelectorAll(".is-loading").forEach(function (el) { el.classList.remove("is-loading"); el.removeAttribute("aria-busy"); });
  });

  document.querySelectorAll("[data-auto-submit]").forEach(function (el) {
    el.addEventListener("change", function () { el.form.submit(); });
  });

  document.querySelectorAll("[data-refresh-url]").forEach(function (box) {
    var url = box.getAttribute("data-refresh-url");
    var seconds = parseInt(box.getAttribute("data-refresh-seconds") || "4", 10);
    var onlyWhile = box.getAttribute("data-refresh-while");
    var timer;

    function openIds() {
      var ids = [];
      box.querySelectorAll("details[open][data-id]").forEach(function (d) { ids.push(d.getAttribute("data-id")); });
      return ids;
    }

    function tick() {
      if (document.hidden) return schedule();
      fetch(url, { headers: { "X-Requested-With": "fetch" }, credentials: "same-origin" })
        .then(function (res) { return res.ok ? res.text() : Promise.reject(res.status); })
        .then(function (html) {
          var open = openIds();
          box.innerHTML = html;
          open.forEach(function (id) {
            var d = box.querySelector('details[data-id="' + id + '"]');
            if (d) d.setAttribute("open", "");
          });
          if (onlyWhile && !box.querySelector(onlyWhile)) return;
          schedule();
        })
        .catch(schedule);
    }

    function schedule() { timer = setTimeout(tick, seconds * 1000); }

    if (!onlyWhile || box.querySelector(onlyWhile)) schedule();
    document.addEventListener("visibilitychange", function () {
      if (!document.hidden) { clearTimeout(timer); tick(); }
    });
  });
})();

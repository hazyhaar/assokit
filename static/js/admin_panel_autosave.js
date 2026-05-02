// admin_panel_autosave.js — Auto-save débounce 500ms pour les champs data-autosave
(function () {
  'use strict';

  var timers = {};

  function updateProgress() {
    fetch('/admin/panel/progress')
      .then(function (r) { return r.json(); })
      .then(function (data) {
        var total = data.required_total || 0;
        var filled = data.required_filled || 0;
        var pct = total > 0 ? Math.round((filled / total) * 100) : 0;
        var fill = document.querySelector('.progress-bar-fill');
        if (fill) { fill.style.width = pct + '%'; }
        var label = document.querySelector('.progress-label');
        if (label) { label.textContent = filled + '/' + total + ' champs obligatoires remplis'; }
      })
      .catch(function () {});
  }

  function saveField(key, value) {
    var fd = new FormData();
    fd.append('key', key);
    fd.append('value', value);

    fetch('/admin/panel/save-field', { method: 'POST', body: fd })
      .then(function (r) {
        return r.text().then(function (html) {
          var badge = document.getElementById('badge-' + key);
          if (badge) {
            badge.outerHTML = html;
          }
          updateProgress();
        });
      })
      .catch(function () {});
  }

  function onInput(evt) {
    var el = evt.target;
    var key = el.getAttribute('data-autosave');
    if (!key) { return; }

    clearTimeout(timers[key]);
    timers[key] = setTimeout(function () {
      saveField(key, el.value);
    }, 500);
  }

  // Attacher les listeners sur tous les éléments data-autosave
  document.querySelectorAll('[data-autosave]').forEach(function (el) {
    el.addEventListener('input', onInput);
    el.addEventListener('change', onInput); // pour input[type=color]
  });
})();

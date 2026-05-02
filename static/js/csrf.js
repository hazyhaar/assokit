// CLAUDE:SUMMARY Auto-injection du token CSRF (cookie nps_csrf) dans :
//  1. tous les <form method="POST"> avant submit (hidden field _csrf).
//  2. toutes les requêtes htmx (header X-CSRF-Token via hx-headers global).
// Compatible avec le middleware double-submit cookie pattern (middleware.go::CSRF).
(function () {
  function readCookie(name) {
    var m = document.cookie.match(new RegExp('(?:^|; )' + name + '=([^;]*)'));
    return m ? decodeURIComponent(m[1]) : '';
  }

  function injectField(form, token) {
    if (!token) return;
    if (form.querySelector('input[name="_csrf"]')) return;
    var input = document.createElement('input');
    input.type = 'hidden';
    input.name = '_csrf';
    input.value = token;
    form.appendChild(input);
  }

  // 1. Forms classiques : on injecte avant submit.
  document.addEventListener('submit', function (ev) {
    var form = ev.target;
    if (!(form instanceof HTMLFormElement)) return;
    var method = (form.getAttribute('method') || 'GET').toUpperCase();
    if (method === 'GET' || method === 'HEAD') return;
    injectField(form, readCookie('nps_csrf'));
  }, true);

  // 2. htmx requests : ajoute header X-CSRF-Token.
  document.addEventListener('htmx:configRequest', function (ev) {
    var token = readCookie('nps_csrf');
    if (token && ev.detail && ev.detail.headers) {
      ev.detail.headers['X-CSRF-Token'] = token;
    }
  });
})();

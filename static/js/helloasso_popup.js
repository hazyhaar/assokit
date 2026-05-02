// helloasso_popup.js — Vanilla JS ≤80 LOC. Popup HelloAsso CSP-compatible.
// Usage : <a class="donate-palier" data-amount="30" href="https://helloasso.com/...">30 €</a>
// Au clic : ouvre popup window.open au lieu de nouvel onglet, fallback target=_blank si JS off.
'use strict';

(function () {
  const buttons = document.querySelectorAll('.donate-palier');
  if (!buttons.length) return;

  buttons.forEach(function (btn) {
    btn.addEventListener('click', function (ev) {
      const url = btn.getAttribute('href');
      if (!url || url === '#') return;
      const amount = btn.dataset.amount || '';
      const target = appendAmount(url, amount);
      const win = window.open(
        target,
        'helloasso_donate',
        'width=480,height=720,scrollbars=yes,noopener,noreferrer'
      );
      if (win) {
        ev.preventDefault();
        win.focus();
      }
      // Si window.open bloqué (popup blocker) → fallback target=_blank du <a>.
    });
  });

  function appendAmount(url, amount) {
    if (!amount || amount === '0') return url;
    const sep = url.indexOf('?') >= 0 ? '&' : '?';
    return url + sep + 'amount=' + encodeURIComponent(amount);
  }
})();

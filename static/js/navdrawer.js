// navdrawer.js — tiroir de navigation mobile (data-nav-drawer-*).
// CSP-sûr (aucun eval, servi depuis 'self') ; délégation document pour
// survivre aux swaps hx-boost. Le repli au passage desktop est synchronisé
// via matchMedia — la visibilité burger vs user-menu desktop reste en CSS pur
// (media query 40rem dans le shell).
(function () {
	'use strict';

	var mq = window.matchMedia('(min-width: 40rem)');

	function expanded(el, open) {
		if (el) el.setAttribute('aria-expanded', open ? 'true' : 'false');
	}

	function setDrawerOpen(root, open) {
		var trigger = root.querySelector('[data-nav-drawer-trigger]');
		var overlay = root.querySelector('[data-nav-drawer-overlay]');
		var panel = root.querySelector('[data-nav-drawer-panel]');
		if (!panel) return;
		panel.classList.toggle('hidden', !open);
		if (overlay) overlay.classList.toggle('hidden', !open);
		expanded(trigger, open);
		document.body.classList.toggle('overflow-hidden', open);
	}

	function closeDrawers() {
		document.querySelectorAll('[data-nav-drawer]').forEach(function (r) { setDrawerOpen(r, false); });
	}

	document.addEventListener('click', function (e) {
		var t = e.target.closest ? e.target : null;
		if (!t) return;

		var drTrigger = t.closest('[data-nav-drawer-trigger]');
		if (drTrigger) {
			var root = drTrigger.closest('[data-nav-drawer]');
			var p = root.querySelector('[data-nav-drawer-panel]');
			setDrawerOpen(root, p && p.classList.contains('hidden'));
			return;
		}
		if (t.closest('[data-nav-drawer-close]') || t.closest('[data-nav-drawer-overlay]')) {
			closeDrawers();
		}
	});

	document.addEventListener('keydown', function (e) {
		if (e.key !== 'Escape') return;
		closeDrawers();
	});

	mq.addEventListener('change', function () { if (mq.matches) closeDrawers(); });
})();
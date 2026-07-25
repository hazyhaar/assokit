// dropdown.js — menu déroulant générique (data-dropdown-*).
// CSP-sûr (aucun eval, servi depuis 'self') ; AUCUN écouteur par élément :
// tout est DÉLÉGUÉ au document pour survivre aux swaps hx-boost (le body
// entier est remplacé à chaque navigation boostée — des écouteurs posés sur
// les éléments du header mourraient avec lui, bug « menu mort après la
// première navigation »).
(function () {
	'use strict';

	function expanded(el, open) {
		if (el) el.setAttribute('aria-expanded', open ? 'true' : 'false');
	}

	function closeDropdowns() {
		document.querySelectorAll('[data-dropdown-panel]').forEach(function (p) { p.classList.add('hidden'); });
		document.querySelectorAll('[data-dropdown-trigger]').forEach(function (t) { expanded(t, false); });
	}

	document.addEventListener('click', function (e) {
		var t = e.target.closest ? e.target : null;
		if (!t) return;

		var ddTrigger = t.closest('[data-dropdown-trigger]');
		if (ddTrigger) {
			var panel = ddTrigger.closest('[data-dropdown]').querySelector('[data-dropdown-panel]');
			var wasClosed = panel && panel.classList.contains('hidden');
			closeDropdowns();
			if (panel && wasClosed) { panel.classList.remove('hidden'); expanded(ddTrigger, true); }
			return;
		}
		if (!t.closest('[data-dropdown]')) closeDropdowns();
	});

	document.addEventListener('keydown', function (e) {
		if (e.key !== 'Escape') return;
		closeDropdowns();
	});
})();
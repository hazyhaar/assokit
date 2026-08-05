// app.js — gestion minimaliste des événements inline déplacés hors du HTML
// (CSP nonce-free : ce fichier est servi via <script src> autorisé par 'self').

(function () {
	"use strict";

	// Feedback FAB : ouvre la modal et charge le formulaire via HTMX
	var fab = document.querySelector('[data-feedback-fab]');
	if (fab) {
		fab.addEventListener('click', function () {
			var modal = document.getElementById('feedback-modal');
			if (modal) modal.removeAttribute('hidden');
		});
	}

	// Feedback cancel : ferme la modal
	var cancelBtn = document.querySelector('[data-feedback-cancel]');
	if (cancelBtn) {
		cancelBtn.addEventListener('click', function () {
			var modal = document.getElementById('feedback-modal');
			if (modal) modal.setAttribute('hidden', '');
		});
	}

	// Feedback textarea : compteur de caractères
	var fbTextarea = document.getElementById('feedback-message');
	if (fbTextarea) {
		fbTextarea.addEventListener('input', function () {
			var count = document.getElementById('fb-count');
			if (count) count.textContent = this.value.length + ' / 2000';
		});
	}

	// Select auto-submit (ex: admin feedback status filter)
	document.addEventListener('change', function (e) {
		if (e.target.matches('[data-auto-submit]')) {
			e.target.form && e.target.form.submit();
		}
	});
})();

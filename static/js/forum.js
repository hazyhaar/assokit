// forum.js — interactions de la station forum à panneaux :
//  - clic sur le bandeau d'un panneau : replie/déplie son corps ;
//  - glisser une poignée entre panneaux : ajuste la largeur du panneau de gauche ;
//  - glisser une poignée de bord : ajuste l'écart entre l'écran et les panneaux.
// État dynamique posé en styles en ligne (aucune classe Tailwind générée à la volée).
(function () {
	"use strict";

	// Repli / dépli d'un panneau au clic sur son bandeau.
	document.addEventListener("click", function (e) {
		var band = e.target.closest("[data-collapse]");
		if (!band) return;
		var panel = band.closest("[data-panel]");
		if (!panel) return;
		var body = panel.querySelector("[data-panel-body]");
		var chevron = band.querySelector("[data-chevron]");
		if (panel.getAttribute("data-collapsed") === "1") {
			panel.removeAttribute("data-collapsed");
			panel.style.flex = panel.dataset.prevFlex || "1 1 0";
			if (body) body.style.display = "";
			if (chevron) chevron.style.transform = "";
		} else {
			panel.dataset.prevFlex = panel.style.flex || "";
			panel.setAttribute("data-collapsed", "1");
			if (body) body.style.display = "none";
			panel.style.flex = "0 0 auto";
			if (chevron) chevron.style.transform = "rotate(-90deg)";
		}
	});

	// Glisser-redimensionner via les poignées [data-resize].
	var drag = null;

	document.addEventListener("pointerdown", function (e) {
		var gap = e.target.closest("[data-resize]");
		if (!gap) return;
		e.preventDefault();
		var station = gap.closest("[data-forum-station]");
		drag = { gap: gap, startX: e.clientX, kind: gap.getAttribute("data-resize") };
		if (drag.kind === "panel") {
			drag.left = gap.previousElementSibling;
			drag.startW = drag.left.getBoundingClientRect().width;
		} else {
			drag.station = station;
			drag.edge = gap.getAttribute("data-edge");
			var prop = drag.edge === "left" ? "paddingLeft" : "paddingRight";
			drag.prop = prop;
			drag.startPad = parseFloat(getComputedStyle(station)[prop]) || 0;
		}
		try { gap.setPointerCapture(e.pointerId); } catch (_) {}
		document.body.style.userSelect = "none";
		document.body.style.cursor = "col-resize";
	});

	document.addEventListener("pointermove", function (e) {
		if (!drag) return;
		var dx = e.clientX - drag.startX;
		if (drag.kind === "panel") {
			var w = Math.max(80, drag.startW + dx);
			drag.left.style.flex = "0 0 " + w + "px";
		} else {
			var sign = drag.edge === "left" ? 1 : -1;
			var pad = Math.max(0, drag.startPad + sign * dx);
			drag.station.style[drag.prop] = pad + "px";
		}
	});

	function endDrag() {
		if (!drag) return;
		document.body.style.userSelect = "";
		document.body.style.cursor = "";
		drag = null;
	}
	document.addEventListener("pointerup", endDrag);
	document.addEventListener("pointercancel", endDrag);

	// --- Pièces jointes du formulaire de réponse : coller une capture (Ctrl+V) ou
	// sélectionner des fichiers ; aperçus + accumulation dans l'input file. ---

	function dtFor(input) {
		if (!input._dt) input._dt = new DataTransfer();
		return input._dt;
	}

	function renderPreviews(form) {
		var input = form.querySelector("[data-forum-reply-files]");
		var box = form.querySelector("[data-forum-reply-previews]");
		if (!input || !box) return;
		box.innerHTML = "";
		var files = dtFor(input).files;
		for (var i = 0; i < files.length; i++) {
			var f = files[i];
			var chip = document.createElement("span");
			chip.style.cssText =
				"display:inline-flex;align-items:center;gap:4px;border:1px solid var(--border);" +
				"border-radius:6px;padding:2px 6px;font-size:12px;max-width:170px;background:var(--surface-muted);";
			if (f.type.indexOf("image/") === 0) {
				var img = document.createElement("img");
				img.src = URL.createObjectURL(f);
				img.style.cssText = "height:30px;width:30px;object-fit:cover;border-radius:4px;";
				chip.appendChild(img);
			} else {
				chip.appendChild(document.createTextNode("📎"));
			}
			var name = document.createElement("span");
			name.textContent = f.name;
			name.style.cssText = "overflow:hidden;text-overflow:ellipsis;white-space:nowrap;";
			chip.appendChild(name);
			box.appendChild(chip);
		}
	}

	// L'utilisateur a choisi des fichiers : les fusionner dans l'accumulateur (le
	// navigateur a remplacé input.files par la seule sélection courante).
	document.addEventListener("change", function (e) {
		var input = e.target.closest && e.target.closest("[data-forum-reply-files]");
		if (!input) return;
		var picked = Array.prototype.slice.call(input.files);
		var dt = dtFor(input);
		picked.forEach(function (f) {
			for (var j = 0; j < dt.files.length; j++) {
				if (dt.files[j].name === f.name && dt.files[j].size === f.size) return;
			}
			dt.items.add(f);
		});
		input.files = dt.files;
		var form = input.closest("form");
		if (form) renderPreviews(form);
	});

	// Collage d'une capture dans la zone de texte : on la verse dans l'input file.
	document.addEventListener("paste", function (e) {
		var ta = e.target.closest && e.target.closest("[data-forum-reply-text]");
		if (!ta) return;
		var items = (e.clipboardData || {}).items || [];
		var imgs = [];
		for (var i = 0; i < items.length; i++) {
			if (items[i].kind === "file" && items[i].type.indexOf("image/") === 0) {
				var f = items[i].getAsFile();
				if (f) imgs.push(f);
			}
		}
		if (!imgs.length) return; // collage de texte normal : laisser faire
		e.preventDefault();
		var form = ta.closest("form");
		var input = form && form.querySelector("[data-forum-reply-files]");
		if (!input) return;
		var dt = dtFor(input);
		imgs.forEach(function (f) { dt.items.add(f); });
		input.files = dt.files;
		renderPreviews(form);
	});
})();

// admin_connector_form.js — Vanilla JS ≤200 LOC. Render JSON Schema → HTML form.
// Usage : <div id="connector-form" data-connector-id="helloasso"></div>
//         <script src="/static/js/admin_connector_form.js"></script>
'use strict';

(function () {
  const root = document.getElementById('connector-form');
  if (!root) return;
  const connectorID = root.dataset.connectorId;
  if (!connectorID) {
    root.innerHTML = '<p class="error">data-connector-id manquant</p>';
    return;
  }

  fetch('/admin/connectors/' + encodeURIComponent(connectorID) + '/schema')
    .then(function (r) {
      if (!r.ok) throw new Error('schema fetch HTTP ' + r.status);
      return r.json();
    })
    .then(function (schema) { renderForm(root, connectorID, schema); })
    .catch(function (err) {
      root.innerHTML = '<p class="error">Erreur chargement schéma : ' + escapeHTML(err.message) + '</p>';
    });

  function renderForm(container, id, schema) {
    const form = document.createElement('form');
    form.className = 'connector-config-form';
    const props = (schema && schema.properties) || {};
    const required = new Set((schema && schema.required) || []);

    Object.keys(props).sort().forEach(function (key) {
      const prop = props[key];
      const wrap = document.createElement('div');
      wrap.className = 'field';

      const label = document.createElement('label');
      label.htmlFor = 'cf-' + key;
      label.textContent = (prop.title || key) + (required.has(key) ? ' *' : '');
      wrap.appendChild(label);

      const input = buildInput(key, prop, required.has(key));
      wrap.appendChild(input);

      if (prop.format === 'password') {
        const toggle = document.createElement('button');
        toggle.type = 'button';
        toggle.className = 'toggle-secret';
        toggle.textContent = 'Afficher';
        toggle.addEventListener('click', function () {
          if (input.type === 'password') { input.type = 'text'; toggle.textContent = 'Masquer'; }
          else { input.type = 'password'; toggle.textContent = 'Afficher'; }
        });
        wrap.appendChild(toggle);
      }
      if (prop.description) {
        const hint = document.createElement('p');
        hint.className = 'hint';
        hint.textContent = prop.description;
        wrap.appendChild(hint);
      }
      form.appendChild(wrap);
    });

    const submit = document.createElement('button');
    submit.type = 'submit';
    submit.textContent = 'Enregistrer';
    form.appendChild(submit);

    const status = document.createElement('p');
    status.className = 'status';
    form.appendChild(status);

    form.addEventListener('submit', function (ev) {
      ev.preventDefault();
      submitForm(id, form, status, props, required);
    });

    container.innerHTML = '';
    container.appendChild(form);
  }

  function buildInput(key, prop, isRequired) {
    let input;
    if (Array.isArray(prop.enum)) {
      input = document.createElement('select');
      prop.enum.forEach(function (opt) {
        const o = document.createElement('option');
        o.value = opt; o.textContent = opt;
        input.appendChild(o);
      });
    } else if (prop.type === 'boolean') {
      input = document.createElement('input');
      input.type = 'checkbox';
    } else if (prop.type === 'integer' || prop.type === 'number') {
      input = document.createElement('input');
      input.type = 'number';
      if (prop.minimum != null) input.min = prop.minimum;
      if (prop.maximum != null) input.max = prop.maximum;
    } else if (prop.format === 'password') {
      input = document.createElement('input');
      input.type = 'password';
    } else if (prop.format === 'uri') {
      input = document.createElement('input');
      input.type = 'url';
    } else {
      input = document.createElement('input');
      input.type = 'text';
    }
    input.id = 'cf-' + key;
    input.name = key;
    if (isRequired) input.required = true;
    if (prop.minLength != null) input.minLength = prop.minLength;
    if (prop.maxLength != null) input.maxLength = prop.maxLength;
    if (prop.pattern) input.pattern = prop.pattern;
    return input;
  }

  function submitForm(id, form, status, props, required) {
    status.textContent = 'Envoi…';
    status.className = 'status';
    const values = {};
    let firstInvalid = null;
    Object.keys(props).forEach(function (key) {
      const el = form.elements.namedItem(key);
      if (!el) return;
      let v;
      if (el.type === 'checkbox') v = el.checked;
      else if (el.type === 'number') v = el.value === '' ? undefined : Number(el.value);
      else v = el.value;
      if (required.has(key) && (v === '' || v == null)) {
        if (!firstInvalid) firstInvalid = el;
      }
      if (v !== undefined && v !== '') values[key] = v;
    });
    if (firstInvalid) {
      firstInvalid.focus();
      status.textContent = 'Champs obligatoires manquants.';
      status.className = 'status error';
      return;
    }
    fetch('/admin/connectors/' + encodeURIComponent(id) + '/configure', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(values),
    }).then(function (r) {
      return r.text().then(function (text) {
        if (!r.ok) {
          status.textContent = 'Erreur ' + r.status + ' : ' + text;
          status.className = 'status error';
          return;
        }
        status.textContent = 'Enregistré ✓';
        status.className = 'status ok';
      });
    }).catch(function (err) {
      status.textContent = 'Erreur réseau : ' + err.message;
      status.className = 'status error';
    });
  }

  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }
})();

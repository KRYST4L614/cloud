'use strict';

const state = {
  token: localStorage.getItem('token'),
  user: null,
  authMode: 'login',
  view: 'files',
  searchTimer: null,
};

const $ = (sel) => document.querySelector(sel);

function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k.startsWith('on')) node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v);
  }
  for (const c of children) node.append(c);
  return node;
}

function formatSize(value) {
  let bytes = Number(value);
  if (!Number.isFinite(bytes) || bytes < 0) return '—';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let i = 0;
  while (bytes >= 1024 && i < units.length - 1) { bytes /= 1024; i++; }
  return `${bytes.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

const formatDate = (s) => new Date(s).toLocaleString('en-GB');

function notificationMessage(message) {
  const text = String(message || 'Something went wrong. Please try again.').trim();
  return text.charAt(0).toUpperCase() + text.slice(1);
}

function toast(msg, isError = false) {
  const t = $('#toast');
  t.textContent = notificationMessage(msg);
  t.className = `toast${isError ? ' error' : ''}`;
  clearTimeout(t._timer);
  t._timer = setTimeout(() => t.classList.add('hidden'), 3500);
}

async function api(path, opts = {}) {
  const headers = { ...(opts.headers || {}) };
  const isAuthRequest = path.startsWith('/api/auth/');
  if (state.token && !isAuthRequest) headers.Authorization = `Bearer ${state.token}`;
  if (opts.json !== undefined) {
    headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(opts.json);
  }
  let res;
  try {
    res = await fetch(path, { ...opts, headers });
  } catch {
    throw new Error('Unable to connect. Please try again.');
  }
  if (res.status === 401 && state.token && !isAuthRequest) {
    logout();
    throw new Error('Your session has expired. Please log in again.');
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    const error = new Error(notificationMessage(body.error || `Request failed (${res.status})`));
    error.status = res.status;
    throw error;
  }
  return res.status === 204 ? null : res.json();
}


function clearAuthError() {
  $('#auth-error').textContent = '';
  document.querySelectorAll('#auth-form input').forEach((input) => input.removeAttribute('aria-invalid'));
}

function showAuthError(message, fields = []) {
  clearAuthError();
  $('#auth-error').textContent = notificationMessage(message);
  fields.forEach((name) => $(`#auth-form input[name="${name}"]`).setAttribute('aria-invalid', 'true'));
}

function setAuthMode(mode) {
  state.authMode = mode;
  document.querySelectorAll('#auth-view .tab').forEach((b) => b.classList.toggle('active', b.dataset.mode === mode));
  $('#auth-submit').textContent = mode === 'login' ? 'Log in' : 'Sign up';
  $('#auth-form input[name="password"]').autocomplete = mode === 'login' ? 'current-password' : 'new-password';
  clearAuthError();
}

async function submitAuth(e) {
  e.preventDefault();
  clearAuthError();
  const form = new FormData(e.target);
  const email = String(form.get('email')).trim();
  const password = String(form.get('password'));
  if (!email) {
    showAuthError('Enter your email address.', ['email']);
    return;
  }
  if ($('#auth-form input[name="email"]').validity.typeMismatch) {
    showAuthError('Enter a valid email address.', ['email']);
    return;
  }
  if (!password) {
    showAuthError('Enter your password.', ['password']);
    return;
  }
  if (state.authMode === 'register') {
    const bytes = new TextEncoder().encode(password).length;
    if (bytes < 8 || bytes > 72) {
      showAuthError('Password must be between 8 and 72 bytes long.', ['password']);
      return;
    }
  }
  $('#auth-submit').disabled = true;
  document.querySelectorAll('#auth-view .tab').forEach((tab) => { tab.disabled = true; });
  try {
    const res = await api(`/api/auth/${state.authMode}`, {
      method: 'POST',
      json: { email, password },
    });
    state.token = res.token;
    localStorage.setItem('token', res.token);
    e.target.reset();
  } catch (err) {
    let fields = [];
    if (state.authMode === 'login' && err.status === 401) fields = ['email', 'password'];
    else if (err.status === 409 || (err.status === 400 && /email/i.test(err.message))) fields = ['email'];
    else if (err.status === 400 && /password/i.test(err.message)) fields = ['password'];
    showAuthError(err.message, fields);
    return;
  } finally {
    $('#auth-submit').disabled = false;
    document.querySelectorAll('#auth-view .tab').forEach((tab) => { tab.disabled = false; });
  }
  try {
    await showApp();
  } catch (err) {
    toast(err.message, true);
  }
}

function logout() {
  state.token = null;
  state.user = null;
  localStorage.removeItem('token');
  clearAuthError();
  $('#app-view').classList.add('hidden');
  $('#auth-view').classList.remove('hidden');
}

async function showApp() {
  $('#auth-view').classList.add('hidden');
  $('#app-view').classList.remove('hidden');
  await refreshUser();
  await loadFiles();
  loadAlerts();
}

async function refreshUser() {
  state.user = await api('/api/me');
  const { email, used_bytes: used, quota_bytes: quota } = state.user;
  $('#user-email').textContent = email;
  const pct = quota ? Math.min(100, (used / quota) * 100) : 0;
  $('#usage-fill').style.width = `${pct}%`;
  $('#usage-fill').className = pct > 90 ? 'danger' : pct > 75 ? 'warn' : '';
  $('#usage-text').textContent = `${formatSize(used)} of ${formatSize(quota)}`;
}


async function loadFiles() {
  const q = $('#search').value.trim();
  const files = await api(`/api/files${q ? `?q=${encodeURIComponent(q)}` : ''}`);
  const tbody = $('#file-list');
  tbody.replaceChildren(...files.map(fileRow));
  $('#empty').classList.toggle('hidden', files.length > 0);
}

const previewable = (type) => /^(image|video|audio|text)\//.test(type);

function downloadUrl(f, inline = false) {
  const params = new URLSearchParams({ access_token: state.token });
  if (inline) params.set('inline', '1');
  return `/api/files/${f.id}/download?${params}`;
}

function fileRow(f) {
  const actions = el('td', { class: 'actions' });
  const btn = (label, onclick, cls = '') => el('button', { class: cls, onclick, title: label }, label);

  if (previewable(f.content_type)) {
    actions.append(btn('Open', () => window.open(downloadUrl(f, true), '_blank', 'noopener')));
  }
  actions.append(
    btn('Download', () => { window.location.href = downloadUrl(f); }),
    btn(f.share_token ? 'Link ✓' : 'Share', () => shareFile(f)),
  );
  if (f.share_token) actions.append(btn('Revoke access', () => unshareFile(f)));
  actions.append(
    btn('Rename', () => renameFile(f)),
    btn('Delete', () => deleteFile(f), 'danger'),
  );

  return el('tr', {},
    el('td', { class: 'name', title: f.name }, f.name),
    el('td', {}, formatSize(f.size)),
    el('td', {}, formatDate(f.created_at)),
    actions,
  );
}

async function shareFile(f) {
  try {
    const res = await api(`/api/files/${f.id}/share`, { method: 'POST' });
    const url = `${location.origin}${res.url}`;
    try {
      await navigator.clipboard.writeText(url);
      toast('Public link copied to clipboard');
    } catch {
      prompt('Public link:', url);
    }
    await loadFiles();
  } catch (err) { toast(err.message, true); }
}

async function unshareFile(f) {
  try {
    await api(`/api/files/${f.id}/share`, { method: 'DELETE' });
    toast('Public access revoked');
    await loadFiles();
  } catch (err) { toast(err.message, true); }
}

async function renameFile(f) {
  const name = prompt('New file name:', f.name);
  if (!name || name === f.name) return;
  try {
    await api(`/api/files/${f.id}`, { method: 'PATCH', json: { name } });
    await loadFiles();
  } catch (err) { toast(err.message, true); }
}

async function deleteFile(f) {
  if (!confirm(`Delete "${f.name}"?`)) return;
  try {
    await api(`/api/files/${f.id}`, { method: 'DELETE' });
    toast('File deleted');
    await Promise.all([loadFiles(), refreshUser()]);
  } catch (err) { toast(err.message, true); }
}

function uploadFile(file) {
  const bar = el('div', { class: 'progress-fill' });
  const status = el('span', { class: 'muted' }, '0%');
  const item = el('li', {}, el('span', { class: 'name' }, file.name), el('div', { class: 'progress' }, bar), status);
  $('#uploads').append(item);

  return new Promise((resolve) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/files');
    xhr.setRequestHeader('Authorization', `Bearer ${state.token}`);
    xhr.upload.onprogress = (e) => {
      if (!e.lengthComputable) return;
      const pct = Math.round((e.loaded / e.total) * 100);
      bar.style.width = `${pct}%`;
      status.textContent = `${pct}%`;
    };
    xhr.onload = () => {
      if (xhr.status === 201) {
        status.textContent = 'Done';
        setTimeout(() => item.remove(), 2000);
      } else {
        let msg = `Upload failed (${xhr.status})`;
        try { msg = JSON.parse(xhr.responseText).error || msg; } catch {  }
        status.textContent = notificationMessage(msg);
        item.classList.add('failed');
        setTimeout(() => item.remove(), 8000);
      }
      resolve();
    };
    xhr.onerror = () => {
      status.textContent = 'Network error';
      item.classList.add('failed');
      resolve();
    };
    const form = new FormData();
    form.append('file', file);
    xhr.send(form);
  });
}

async function uploadFiles(files) {
  for (const f of files) await uploadFile(f);
  await Promise.all([loadFiles(), refreshUser()]);
}


async function loadAlerts() {
  if (!state.token) return;
  let alerts;
  try {
    alerts = await api('/api/alerts');
  } catch { return; }

  const firing = alerts.filter((a) => a.status === 'firing').length;
  const badge = $('#alert-badge');
  badge.textContent = firing;
  badge.classList.toggle('hidden', firing === 0);

  $('#alert-list').replaceChildren(...alerts.map((a) => el('li', { class: `alert ${a.status} sev-${a.severity || 'none'}` },
    el('div', { class: 'alert-head' },
      el('span', { class: 'status' }, a.status === 'firing' ? '🔥 Active' : '✅ Resolved'),
      el('strong', {}, a.name || 'Alert'),
      el('span', { class: 'severity' }, a.severity || ''),
      el('span', { class: 'muted' }, formatDate(a.starts_at)),
    ),
    el('div', {}, a.summary || ''),
    el('div', { class: 'muted' }, a.description || ''),
  )));
  $('#alerts-empty').classList.toggle('hidden', alerts.length > 0);
}

function setView(view) {
  state.view = view;
  document.querySelectorAll('.topbar .tab').forEach((b) => b.classList.toggle('active', b.dataset.view === view));
  $('#files-view').classList.toggle('hidden', view !== 'files');
  $('#alerts-view').classList.toggle('hidden', view !== 'alerts');
  if (view === 'alerts') loadAlerts();
}


function init() {
  setAuthMode('login');
  document.querySelectorAll('#auth-view .tab').forEach((b) => b.addEventListener('click', () => setAuthMode(b.dataset.mode)));
  document.querySelectorAll('.topbar .tab').forEach((b) => b.addEventListener('click', () => setView(b.dataset.view)));
  $('#auth-form').addEventListener('submit', submitAuth);
  $('#auth-form').addEventListener('input', clearAuthError);
  $('#logout').addEventListener('click', logout);
  $('#file-input').addEventListener('change', (e) => { uploadFiles([...e.target.files]); e.target.value = ''; });
  $('#search').addEventListener('input', () => {
    clearTimeout(state.searchTimer);
    state.searchTimer = setTimeout(() => loadFiles().catch((err) => toast(err.message, true)), 250);
  });

  const dz = $('#dropzone');
  ['dragenter', 'dragover'].forEach((ev) => document.addEventListener(ev, (e) => { e.preventDefault(); dz.classList.add('over'); }));
  ['dragleave', 'drop'].forEach((ev) => document.addEventListener(ev, (e) => { e.preventDefault(); if (ev === 'drop' || !e.relatedTarget) dz.classList.remove('over'); }));
  document.addEventListener('drop', (e) => {
    if (state.token && e.dataTransfer.files.length) uploadFiles([...e.dataTransfer.files]);
  });

  $('#grafana-link').href = `${location.protocol}//${location.hostname}:3000`;
  $('#prometheus-link').href = `${location.protocol}//${location.hostname}:9090`;

  setInterval(loadAlerts, 15000);

  if (state.token) {
    showApp().catch(() => logout());
  } else {
    logout();
  }
}

init();

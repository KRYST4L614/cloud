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

function formatSize(bytes) {
  const units = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ'];
  let i = 0;
  while (bytes >= 1024 && i < units.length - 1) { bytes /= 1024; i++; }
  return `${bytes.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

const formatDate = (s) => new Date(s).toLocaleString('ru-RU');

function toast(msg, isError = false) {
  const t = $('#toast');
  t.textContent = msg;
  t.className = `toast${isError ? ' error' : ''}`;
  clearTimeout(t._timer);
  t._timer = setTimeout(() => t.classList.add('hidden'), 3500);
}

async function api(path, opts = {}) {
  const headers = { ...(opts.headers || {}) };
  if (state.token) headers.Authorization = `Bearer ${state.token}`;
  if (opts.json !== undefined) {
    headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(opts.json);
  }
  const res = await fetch(path, { ...opts, headers });
  if (res.status === 401 && state.token) {
    logout();
    throw new Error('Сессия истекла, войдите снова');
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `Ошибка ${res.status}`);
  }
  return res.status === 204 ? null : res.json();
}


function setAuthMode(mode) {
  state.authMode = mode;
  document.querySelectorAll('#auth-view .tab').forEach((b) => b.classList.toggle('active', b.dataset.mode === mode));
  $('#auth-submit').textContent = mode === 'login' ? 'Войти' : 'Зарегистрироваться';
  $('#auth-error').textContent = '';
}

async function submitAuth(e) {
  e.preventDefault();
  const form = new FormData(e.target);
  try {
    const res = await api(`/api/auth/${state.authMode}`, {
      method: 'POST',
      json: { email: form.get('email'), password: form.get('password') },
    });
    state.token = res.token;
    localStorage.setItem('token', res.token);
    e.target.reset();
    await showApp();
  } catch (err) {
    $('#auth-error').textContent = err.message;
  }
}

function logout() {
  state.token = null;
  state.user = null;
  localStorage.removeItem('token');
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
  $('#usage-text').textContent = `${formatSize(used)} из ${formatSize(quota)}`;
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
    actions.append(btn('Открыть', () => window.open(downloadUrl(f, true), '_blank', 'noopener')));
  }
  actions.append(
    btn('Скачать', () => { window.location.href = downloadUrl(f); }),
    btn(f.share_token ? 'Ссылка ✓' : 'Поделиться', () => shareFile(f)),
  );
  if (f.share_token) actions.append(btn('Закрыть доступ', () => unshareFile(f)));
  actions.append(
    btn('Переименовать', () => renameFile(f)),
    btn('Удалить', () => deleteFile(f), 'danger'),
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
      toast('Публичная ссылка скопирована в буфер обмена');
    } catch {
      prompt('Публичная ссылка:', url);
    }
    await loadFiles();
  } catch (err) { toast(err.message, true); }
}

async function unshareFile(f) {
  try {
    await api(`/api/files/${f.id}/share`, { method: 'DELETE' });
    toast('Публичный доступ закрыт');
    await loadFiles();
  } catch (err) { toast(err.message, true); }
}

async function renameFile(f) {
  const name = prompt('Новое имя файла:', f.name);
  if (!name || name === f.name) return;
  try {
    await api(`/api/files/${f.id}`, { method: 'PATCH', json: { name } });
    await loadFiles();
  } catch (err) { toast(err.message, true); }
}

async function deleteFile(f) {
  if (!confirm(`Удалить «${f.name}»?`)) return;
  try {
    await api(`/api/files/${f.id}`, { method: 'DELETE' });
    toast('Файл удалён');
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
        status.textContent = 'Готово';
        setTimeout(() => item.remove(), 2000);
      } else {
        let msg = `Ошибка ${xhr.status}`;
        try { msg = JSON.parse(xhr.responseText).error || msg; } catch {  }
        status.textContent = msg;
        item.classList.add('failed');
        setTimeout(() => item.remove(), 8000);
      }
      resolve();
    };
    xhr.onerror = () => {
      status.textContent = 'Сетевая ошибка';
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
      el('span', { class: 'status' }, a.status === 'firing' ? '🔥 Активно' : '✅ Решено'),
      el('strong', {}, a.name || 'alert'),
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
  document.querySelectorAll('#auth-view .tab').forEach((b) => b.addEventListener('click', () => setAuthMode(b.dataset.mode)));
  document.querySelectorAll('.topbar .tab').forEach((b) => b.addEventListener('click', () => setView(b.dataset.view)));
  $('#auth-form').addEventListener('submit', submitAuth);
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

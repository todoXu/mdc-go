import { api, escapeHTML as h } from './api.js';

// 补头像进度：页面上的进度条显示已处理数、成功数、失败数和失败明细。
function renderFillProgress(state) {
  const box = document.querySelector('#fill-progress');
  if (!box) return;
  const done = state.done || 0;
  const total = state.total || 0;
  const failed = state.failed || [];
  const pct = total ? Math.min(100, Math.round((done / total) * 100)) : 0;
  box.hidden = !state.running && !state.finished;
  // 进度条每秒重绘，取消按钮用事件委托处理，不依赖按钮本身。
  box.onclick = (event) => {
    const cancel = event.target.closest('#fill-cancel');
    if (cancel) cancelFill(cancel);
  };
  const title = state.running ? '正在补头像' : state.error === '已取消' ? '补头像已取消' : state.error ? '补头像中断' : '补头像完成';
  box.innerHTML = `<div class="fill-progress-head"><b>${title}</b><span>${done} / ${total}</span><span class="fill-ok">成功 ${state.updated || 0}</span><span class="fill-bad">失败 ${failed.length}</span><span>Gfriends 无头像 ${state.no_gfriends || 0}</span>${state.running ? '<button class="button small" type="button" id="fill-cancel">取消补头像</button>' : ''}</div>`
    + `<div class="fill-progress-bar"><i style="width:${pct}%"></i></div>`
    + (state.error ? `<p class="fill-bad">${h(state.error)}</p>` : '')
    + (failed.length ? `<details><summary>查看 ${failed.length} 条失败记录</summary><ul>${failed.map((item) => `<li>${h(item)}</li>`).join('')}</ul></details>` : '');
}

// 取消补头像：服务端停止后续处理，已上传的头像保留；进度条随轮询更新为「已取消」。
async function cancelFill(button) {
  button.disabled = true;
  button.textContent = '正在取消…';
  try {
    await api('/api/actor-tools/fill-missing/cancel', { method: 'POST' });
  } catch {
    button.disabled = false;
    button.textContent = '取消补头像';
  }
}

// 轮询补头像状态直到任务结束，并刷新进度条；页面已经切走时停止。
async function pollFillProgress() {
  for (;;) {
    if (!document.querySelector('#fill-progress')) return null;
    const state = await api('/api/actor-tools/fill-missing');
    renderFillProgress(state);
    if (!state.running) return state;
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
}

// 演员管理：演员、头像、简介和关联影片来自 Emby；头像候选来自 Gfriends，写真来自 Jalbum。
// 修改头像后上传回 Emby；编辑名字直接修改映射表（远程映射表只读）。

// 每页显示整行：按网格实际列数取「列数 × 行数」，最后一行不会缺几个。
const gridColumns = (grid) => Math.max(1, getComputedStyle(grid).gridTemplateColumns.split(' ').filter(Boolean).length);
const listRows = 5;
const photoRows = 3;

// 跳转到指定页：输入页码后按回车或点「跳转」，超出范围时取最近的有效页。
// 不用 <form>：修改头像弹窗本身就是表单，表单不能嵌套。
const jumpHTML = (id) => `<span class="actor-jump">跳至<input type="text" inputmode="numeric" id="${id}" aria-label="跳转页码">页<button class="button small" type="button" data-jump="${id}">跳转</button></span>`;
function bindJump(root, id, pages, go) {
  const input = root.querySelector(`#${id}`);
  const submit = () => {
    const value = Number.parseInt(input.value, 10);
    if (!Number.isFinite(value)) { input.value = ''; input.focus(); return; }
    go(Math.min(pages, Math.max(1, value)));
  };
  root.querySelector(`[data-jump="${id}"]`).onclick = submit;
  input.onkeydown = (event) => { if (event.key === 'Enter') { event.preventDefault(); submit(); } };
}
const sourceNames = { gfriends: 'Gfriends 头像', jalbum: 'Jalbum 写真' };
const galleryURL = (source, path) => `/api/actor-gallery/${source}/image?path=${encodeURIComponent(path)}`;
const hashQuery = () => new URLSearchParams(location.hash.split('?')[1] || '');
const initial = (name) => h([...String(name || '?')][0]);

function formatTime(value) {
  const date = new Date(value);
  if (!value || Number.isNaN(date.getTime()) || date.getFullYear() < 2000) return '';
  const pad = (n) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function sourceCard(id, state) {
  const status = {
    ready: '<span class="actor-src-ok">● 可用</span>',
    loading: '<span class="actor-src-busy">● 加载中…</span>',
    error: '<span class="actor-src-err">● 失败</span>',
    missing: '<span class="actor-src-busy">● 未下载</span>',
  }[state.state] || '';
  const detail = state.state === 'error' ? h(state.error) : [
    formatTime(state.updated) && `索引更新于 ${formatTime(state.updated)}`,
    state.actors && `${state.actors} 位演员`,
    state.images && `${state.images} 张${id === 'jalbum' ? '写真' : '头像'}`,
  ].filter(Boolean).join(' · ') || '首次使用时自动下载文件树';
  return `<div class="panel actor-src"><div class="actor-src-icon actor-src-${id}">${id === 'gfriends' ? 'G' : 'J'}</div><div class="actor-src-text"><b>${sourceNames[id]}</b><small title="${detail}">${detail}</small></div>${status}<button class="button small" type="button" data-refresh-source="${id}" ${state.state === 'loading' ? 'disabled' : ''}>更新</button></div>`;
}

function actorCard(actor) {
  const picture = actor.image
    ? `<img src="${h(actor.image)}" alt="" loading="lazy" decoding="async">`
    : `<span class="actor-initial">${initial(actor.name)}</span><span class="actor-flag">无头像</span>`;
  return `<a class="actor-card" href="#/actors/${encodeURIComponent(actor.id)}"><div class="actor-photo${actor.image ? '' : ' actor-photo-empty'}">${picture}${actor.photos ? `<span class="actor-count">写真 ${actor.photos}</span>` : ''}</div><div class="actor-card-info"><b title="${h(actor.name)}">${h(actor.name)}</b><small>${actor.movies} 部作品</small></div></a>`;
}

function embyMissing(heading, message) {
  return {
    html: heading('演员管理', '演员和头像来自 Emby。') + `<section class="panel actor-empty"><div class="empty"><div class="empty-icon">♙</div><h3>还没有连接 Emby</h3><p>${h(message)}</p><a class="button primary" href="#/settings/emby">前往 设置 → 头像</a></div></section>`,
  };
}

export async function actorsPage({ heading, toast, reload }) {
  let data;
  try {
    data = await api('/api/actors' + (hashQuery().get('refresh') === '1' ? '?refresh=1' : ''), { timeout: 120000 });
  } catch (error) {
    if (error.code === 'emby_not_configured') return embyMissing(heading, error.message);
    throw error;
  }
  const actors = data.data || [];
  const missing = actors.filter((actor) => !actor.image).length;
  return {
    html: heading('演员管理', `Emby 媒体库中的演员，名字按映射表显示。共 ${actors.length} 位`,
      '<div class="actor-heading-actions"><button class="button" type="button" id="actor-refresh">↻ 从 Emby 刷新</button><button class="button primary" type="button" id="actor-fill">🖼 为无头像演员补头像</button></div>')
      + `<div class="actor-sources">${Object.entries(data.sources || {}).map(([id, state]) => sourceCard(id, state)).join('')}</div>`
      + '<div class="fill-progress" id="fill-progress" hidden></div>'
      + `<section class="panel"><div class="toolbar actor-toolbar"><div class="tabs">
          <button data-actor-filter="all">全部 ${actors.length}</button><button data-actor-filter="missing">无头像 ${missing}</button>
        </div><input class="search" id="actor-search" aria-label="搜索演员" placeholder="搜索名字、别名（简繁日均可）…"><select id="actor-sort" aria-label="排序"><option value="movies">按作品数 ↓</option><option value="name">按名字</option></select></div>
        <div class="actor-grid" id="actor-grid"></div><div id="actor-empty"></div>
        <div class="actor-pager"><button class="button small" type="button" id="actor-prev">‹ 上一页</button><span id="actor-page-info"></span><button class="button small" type="button" id="actor-next">下一页 ›</button>${jumpHTML('actor-page-input')}</div></section>`,
    bind() {
      const query = hashQuery();
      let filter = query.get('filter') === 'missing' ? 'missing' : 'all';
      let sort = query.get('sort') === 'name' ? 'name' : 'movies';
      let page = Math.max(1, Number(query.get('page')) || 1);
      const search = document.querySelector('#actor-search');
      search.value = query.get('q') || '';
      document.querySelector('#actor-sort').value = sort;
      const collator = new Intl.Collator('ja', { numeric: true, sensitivity: 'base' });
      const syncURL = () => {
        const params = new URLSearchParams();
        if (filter !== 'all') params.set('filter', filter);
        if (sort !== 'movies') params.set('sort', sort);
        if (search.value.trim()) params.set('q', search.value.trim());
        if (page > 1) params.set('page', String(page));
        history.replaceState(null, '', `${location.pathname}${location.search}#/actors${params.toString() ? `?${params}` : ''}`);
      };
      const render = () => {
        const keyword = search.value.trim().toLowerCase();
        let rows = actors.filter((actor) => (filter === 'all' || !actor.image)
          && (!keyword || actor.name.toLowerCase().includes(keyword) || actor.search.includes(keyword)));
        rows = rows.sort(sort === 'name' ? (a, b) => collator.compare(a.name, b.name) : (a, b) => b.movies - a.movies || collator.compare(a.name, b.name));
        const pageSize = gridColumns(document.querySelector('#actor-grid')) * listRows;
        const pages = Math.max(1, Math.ceil(rows.length / pageSize));
        page = Math.min(page, pages);
        syncURL();
        document.querySelectorAll('[data-actor-filter]').forEach((button) => button.classList.toggle('active', button.dataset.actorFilter === filter));
        document.querySelector('#actor-grid').innerHTML = rows.slice((page - 1) * pageSize, page * pageSize).map(actorCard).join('');
        document.querySelector('#actor-empty').innerHTML = rows.length ? '' : `<div class="empty"><div class="empty-icon">♙</div><h3>没有找到演员</h3><p>${keyword ? '试试其他名字或别名。' : '当前筛选下没有演员。'}</p></div>`;
        document.querySelector('#actor-page-info').textContent = `第 ${page} / ${pages} 页 · ${rows.length} 位`;
        document.querySelector('#actor-prev').disabled = page <= 1;
        document.querySelector('#actor-next').disabled = page >= pages;
        bindJump(document, 'actor-page-input', pages, (target) => { page = target; render(); window.scrollTo(0, 0); });
        // 头像加载失败（Emby 中图片被删除等）时显示首字。
        document.querySelectorAll('#actor-grid img').forEach((img) => {
          img.onerror = () => { img.closest('.actor-photo').classList.add('actor-photo-empty'); img.outerHTML = `<span class="actor-initial">${initial(img.closest('.actor-card').querySelector('b').textContent)}</span>`; };
        });
      };
      document.querySelectorAll('[data-actor-filter]').forEach((button) => { button.onclick = () => { filter = button.dataset.actorFilter; page = 1; render(); }; });
      search.oninput = () => { page = 1; render(); };
      document.querySelector('#actor-sort').onchange = (event) => { sort = event.target.value; page = 1; render(); };
      document.querySelector('#actor-prev').onclick = () => { page--; render(); window.scrollTo(0, 0); };
      document.querySelector('#actor-next').onclick = () => { page++; render(); window.scrollTo(0, 0); };
      // 窗口宽度变化后列数会变，重新按整行分页。
      const grid = document.querySelector('#actor-grid');
      let columns = gridColumns(grid);
      const resize = new ResizeObserver(() => {
        if (!grid.isConnected) { resize.disconnect(); return; }
        const next = gridColumns(grid);
        if (next !== columns) { columns = next; render(); }
      });
      resize.observe(grid);
      document.querySelector('#actor-refresh').onclick = async (event) => {
        event.target.disabled = true;
        try { await api('/api/actors?refresh=1', { timeout: 120000 }); await reload({ silent: true }); toast('已从 Emby 刷新演员列表'); }
        catch (error) { toast(error.message, true); event.target.disabled = false; }
      };
      document.querySelector('#actor-fill').onclick = async (event) => {
        if (!missing) { toast('所有演员都已有头像'); return; }
        if (!window.confirm(`为 ${missing} 位没有头像的演员补头像？\n每位演员使用 Gfriends 文件树中的最后一张头像，并上传到 Emby。已有头像的演员不会改动。`)) return;
        try {
          await api('/api/actor-tools/fill-missing', { method: 'POST' });
        } catch (error) {
          toast(error.message, true);
          return;
        }
        runFill(event.target);
      };
      // 启动补头像后轮询进度；页面刷新时如果任务仍在进行，继续显示进度。
      const runFill = async (button) => {
        button.disabled = true;
        button.textContent = '正在补头像…';
        try {
          const state = await pollFillProgress();
          if (!state) return;
          const failed = state.failed?.length || 0;
          toast(state.error ? `补头像中断：${state.error}` : `补头像完成：成功 ${state.updated} 位，Gfriends 中没有 ${state.no_gfriends} 位${failed ? `，失败 ${failed} 位` : ''}`, Boolean(state.error) || failed > 0);
          await reload({ silent: true });
        } catch (error) {
          toast(error.message, true);
        } finally {
          button.disabled = false;
          button.textContent = '🖼 为无头像演员补头像';
        }
      };
      api('/api/actor-tools/fill-missing').then((state) => {
        if (state.running) runFill(document.querySelector('#actor-fill'));
        else if (state.finished) renderFillProgress(state);
      }).catch(() => {});
      document.querySelectorAll('[data-refresh-source]').forEach((button) => {
        button.onclick = async () => {
          button.disabled = true;
          button.textContent = '下载中…';
          try { await api(`/api/actor-gallery/${button.dataset.refreshSource}/refresh`, { method: 'POST', timeout: 600000 }); toast(`${sourceNames[button.dataset.refreshSource]}索引已更新`); await reload({ silent: true }); }
          catch (error) { toast(error.message, true); button.disabled = false; button.textContent = '更新'; }
        };
      });
      // 文件树在后台下载时，过几秒自动刷新一次，写真数量随之出现。
      if (Object.values(data.sources || {}).some((state) => state.state === 'loading' || state.state === 'missing')) {
        const hash = location.hash;
        setTimeout(() => { if (location.hash.split('?')[0] === hash.split('?')[0]) reload({ silent: true }); }, 4000);
      }
      render();
    },
  };
}

// 图片查看：点写真放大，左右键切换，Esc 关闭。
function openViewer(urls, start) {
  let index = start;
  const dialog = document.createElement('dialog');
  dialog.className = 'actor-viewer';
  dialog.innerHTML = `<button class="actor-viewer-close" type="button" aria-label="关闭">×</button><button class="actor-viewer-nav prev" type="button" aria-label="上一张">‹</button><img alt=""><button class="actor-viewer-nav next" type="button" aria-label="下一张">›</button><span class="actor-viewer-count"></span>`;
  document.body.append(dialog);
  const show = () => {
    dialog.querySelector('img').src = urls[index];
    dialog.querySelector('.actor-viewer-count').textContent = `${index + 1} / ${urls.length}`;
  };
  const move = (step) => { index = (index + step + urls.length) % urls.length; show(); };
  dialog.querySelector('.prev').onclick = () => move(-1);
  dialog.querySelector('.next').onclick = () => move(1);
  dialog.querySelector('.actor-viewer-close').onclick = () => dialog.close();
  // 点击遮罩由 app.js 统一处理（按下和松开都在遮罩上才关闭）。
  dialog.onkeydown = (event) => { if (event.key === 'ArrowLeft') move(-1); if (event.key === 'ArrowRight') move(1); };
  dialog.addEventListener('close', () => dialog.remove(), { once: true });
  show();
  dialog.showModal();
}

function readFile(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result);
    reader.onerror = () => reject(Error('读取图片失败'));
    reader.readAsDataURL(file);
  });
}

function fileSize(bytes) {
  return bytes < 1024 * 1024 ? `${Math.round(bytes / 1024)} KB` : `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

// 修改头像：Gfriends 头像、Jalbum 写真、本地上传三个标签，选中一张后上传到 Emby。
function avatarDialog(actor, onSaved, toast) {
  // 写真每页的张数在渲染时按弹窗里网格的列数计算（整行），先用 20 渲染一次以测量列数。
  let photoPage = 20;
  let tab = actor.gfriends.length ? 'gfriends' : actor.jalbum.length ? 'jalbum' : 'upload';
  let photoIndex = 0;
  // 默认选中 Gfriends 文件树里的最后一张。
  let choice = actor.gfriends.length ? { source: 'gfriends', path: actor.gfriends[actor.gfriends.length - 1].path, label: `Gfriends · ${actor.gfriends[actor.gfriends.length - 1].company}` } : null;
  let upload = null;
  const dialog = document.createElement('dialog');
  dialog.className = 'record-editor record-editor-lg actor-avatar-dialog actor-dialog';
  dialog.innerHTML = `<form method="dialog"><header><div><h2>修改头像 · ${h(actor.name)}</h2><p class="muted">选好后上传到 Emby，替换当前头像。</p></div><button class="icon-button" value="cancel" aria-label="关闭">×</button></header>
    <div class="actor-tabs" role="tablist"></div><div class="record-editor-body actor-avatar-body"></div><p class="record-editor-error" role="alert"></p>
    <footer><span class="muted actor-choice"></span><div><button class="button" value="cancel">取消</button> <button class="button primary" type="button" data-save>设为头像并上传到 Emby</button></div></footer></form>`;
  document.body.append(dialog);
  const body = dialog.querySelector('.actor-avatar-body');
  const match = `<div class="actor-match">用这些名字匹配：${actor.match_names.map((name) => `<code>${h(name)}</code>`).join(' ')}</div>`;
  const option = (source, path, caption, extra = '') => {
    const selected = choice?.source === source && choice.path === path;
    return `<button type="button" class="actor-option${selected ? ' selected' : ''}" data-source="${source}" data-path="${h(path)}" data-label="${h(caption)}"><span class="actor-option-photo"><img src="${h(galleryURL(source, path))}" alt="" loading="lazy">${extra}</span><small>${h(caption)}<span class="actor-option-size"></span></small></button>`;
  };
  const renderTabs = () => {
    dialog.querySelector('.actor-tabs').innerHTML = [['gfriends', 'Gfriends 头像', actor.gfriends.length], ['jalbum', 'Jalbum 写真', actor.jalbum.length], ['upload', '本地上传', '']]
      .map(([id, name, count]) => `<button type="button" role="tab" class="${tab === id ? 'active' : ''}" data-tab="${id}">${name}${count !== '' ? `<em>${count}</em>` : ''}</button>`).join('');
    dialog.querySelectorAll('[data-tab]').forEach((button) => { button.onclick = () => { tab = button.dataset.tab; render(); }; });
  };
  const render = () => {
    renderTabs();
    if (tab === 'gfriends') {
      body.innerHTML = match + (actor.gfriends.length
        ? `<div class="actor-options">${actor.gfriends.map((image) => option('gfriends', image.path, image.company, `<span class="actor-option-tags">${image.default ? '<i class="default">默认</i>' : ''}${image.ai ? '<i class="ai">AI 修复</i>' : ''}</span>`)).join('')}</div>`
        : `<div class="actor-none">Gfriends 中没有找到这位演员的头像</div>`);
    } else if (tab === 'jalbum') {
      if (!body.querySelector('.actor-options')) body.innerHTML = '<div class="actor-options"></div>';
      photoPage = gridColumns(body.querySelector('.actor-options')) * photoRows;
      const pages = Math.max(1, Math.ceil(actor.jalbum.length / photoPage));
      const page = Math.floor(photoIndex / photoPage);
      body.innerHTML = actor.jalbum.length
        ? `<div class="actor-options">${actor.jalbum.slice(page * photoPage, (page + 1) * photoPage).map((path, i) => option('jalbum', path, `写真 ${page * photoPage + i + 1}`)).join('')}</div>
           <div class="actor-option-pager"><button class="button small" type="button" data-photo-page="-1" ${page === 0 ? 'disabled' : ''}>‹ 上一页</button><span class="muted">第 ${page + 1} / ${pages} 页 · 共 ${actor.jalbum.length} 张</span><button class="button small" type="button" data-photo-page="1" ${page >= pages - 1 ? 'disabled' : ''}>下一页 ›</button>${pages > 1 ? jumpHTML('photo-page-input') : ''}</div>`
        : match + `<div class="actor-none">Jalbum 中没有找到这位演员的写真</div>`;
      body.querySelectorAll('[data-photo-page]').forEach((button) => { button.onclick = () => { photoIndex = Math.max(0, (page + Number(button.dataset.photoPage)) * photoPage); render(); }; });
      if (pages > 1) bindJump(body, 'photo-page-input', pages, (target) => { photoIndex = (target - 1) * photoPage; render(); });
    } else {
      body.innerHTML = `<div class="actor-upload${upload ? ' has-file' : ''}"><label class="actor-drop"><input type="file" accept="image/jpeg,image/png,image/webp" hidden><span class="actor-drop-icon">⇪</span><b>${upload ? '换一张' : '拖动图片到这里，或点击选择'}</b><small>JPG、PNG、WebP，不超过 10 MB</small></label>${upload ? `<div class="actor-upload-preview"><img src="${h(upload.url)}" alt="预览"><small>${h(upload.name)}<br>${upload.width}×${upload.height} · ${fileSize(upload.size)}</small></div>` : ''}</div>`;
      const drop = body.querySelector('.actor-drop');
      const input = drop.querySelector('input');
      const accept = async (file) => {
        if (!file) return;
        if (!['image/jpeg', 'image/png', 'image/webp'].includes(file.type)) { toast('只支持 JPG、PNG、WebP 图片', true); return; }
        if (file.size > 10 * 1024 * 1024) { toast('图片不能超过 10 MB', true); return; }
        const url = await readFile(file);
        const image = new Image();
        image.onload = () => {
          upload = { url, name: file.name, size: file.size, width: image.naturalWidth, height: image.naturalHeight };
          choice = { source: 'upload', label: `本地图片 ${file.name}` };
          render();
        };
        image.onerror = () => toast('图片无法读取', true);
        image.src = url;
      };
      input.onchange = () => accept(input.files[0]);
      drop.ondragover = (event) => { event.preventDefault(); drop.classList.add('over'); };
      drop.ondragleave = () => drop.classList.remove('over');
      drop.ondrop = (event) => { event.preventDefault(); drop.classList.remove('over'); accept(event.dataTransfer.files[0]); };
    }
    body.querySelectorAll('.actor-option').forEach((button) => {
      button.onclick = () => { choice = { source: button.dataset.source, path: button.dataset.path, label: `${sourceNames[button.dataset.source].split(' ')[0]} · ${button.dataset.label}` }; render(); };
      const img = button.querySelector('img');
      const size = () => { if (img.naturalWidth) button.querySelector('.actor-option-size').textContent = ` · ${img.naturalWidth}×${img.naturalHeight}`; };
      img.onload = size;
      img.onerror = () => { button.classList.add('failed'); button.disabled = true; };
      if (img.complete) size();
    });
    dialog.querySelector('.actor-choice').textContent = choice ? `已选：${choice.label}` : '未选择';
    dialog.querySelector('[data-save]').disabled = !choice;
  };
  dialog.querySelector('[data-save]').onclick = async (event) => {
    if (!choice) return;
    const button = event.target;
    const error = dialog.querySelector('[role=alert]');
    button.disabled = true;
    button.textContent = '正在上传…';
    error.textContent = '';
    try {
      const body = choice.source === 'upload' ? { source: 'upload', body_base64: upload.url.split(',')[1] } : { source: choice.source, path: choice.path };
      await api(`/api/actors/${encodeURIComponent(actor.id)}/image`, { method: 'POST', body, timeout: 120000 });
      dialog.close();
      toast('头像已更新到 Emby');
      onSaved();
    } catch (failure) {
      error.textContent = failure.message;
      button.disabled = false;
      button.textContent = '设为头像并上传到 Emby';
    }
  };
  dialog.addEventListener('close', () => dialog.remove(), { once: true });
  render();
  dialog.showModal();
}

// 编辑名字：写入内置映射表；使用远程映射表时只读。
function namesDialog(actor, onSaved, toast) {
  const names = actor.names || { jp: actor.emby_name, zh_cn: '', zh_tw: '', aliases: [] };
  const locked = !actor.mapping?.editable;
  const dialog = document.createElement('dialog');
  dialog.className = 'record-editor record-editor-sm actor-dialog';
  const field = (key, label, value, hint = '') => `<label class="actor-name-field"><span>${label}</span><input name="${key}" value="${h(value)}" ${locked ? 'disabled' : ''} spellcheck="false">${hint ? `<small>${hint}</small>` : ''}</label>`;
  dialog.innerHTML = `<form method="dialog"><header><div><h2>编辑名字 · ${h(actor.name)}</h2><p class="muted">${locked ? '当前使用远程仓库的自定义映射表' : actor.names ? '保存到内置映射表 data/mapping_actor.xml' : '映射表里还没有这位演员，保存后新增一条'}</p></div><button class="icon-button" value="cancel" aria-label="关闭">×</button></header>
    <div class="record-editor-body actor-names">${field('jp', '日文', names.jp)}${field('zh_cn', '简体', names.zh_cn)}${field('zh_tw', '繁体', names.zh_tw)}<label class="actor-name-field"><span>别名</span><textarea name="aliases" rows="1" ${locked ? 'disabled' : ''} spellcheck="false">${h((names.aliases || []).join(', '))}</textarea><small>多个别名用逗号分隔，输入框会随内容自动变高</small></label>
    ${locked ? '<p class="actor-lock">🔒 远程仓库的映射表不能在这里修改，请到仓库中修改后在「设置 → 元数据」检查更新。</p>' : ''}</div>
    <p class="record-editor-error" role="alert"></p>
    <footer><span class="muted">保存后，重新整理的 NFO 使用新名字</span><div><button class="button" value="cancel">${locked ? '关闭' : '取消'}</button> <button class="button primary" type="button" data-save ${locked ? 'disabled' : ''}>保存</button></div></footer></form>`;
  document.body.append(dialog);
  // 别名输入框按内容自动变高，全部别名都能看到。
  const aliases = dialog.querySelector('[name=aliases]');
  const grow = () => { aliases.style.height = 'auto'; aliases.style.height = `${aliases.scrollHeight + 2}px`; };
  aliases.addEventListener('input', grow);
  // 回车不换行：别名之间用逗号分隔。
  aliases.addEventListener('keydown', (event) => { if (event.key === 'Enter') event.preventDefault(); });
  dialog.querySelector('[data-save]').onclick = async (event) => {
    const value = (name) => dialog.querySelector(`[name=${name}]`).value.trim();
    const body = { jp: value('jp'), zh_cn: value('zh_cn'), zh_tw: value('zh_tw'), aliases: value('aliases').split(/[,，、]/).map((item) => item.trim()).filter(Boolean) };
    const error = dialog.querySelector('[role=alert]');
    event.target.disabled = true;
    error.textContent = '';
    try {
      const result = await api(`/api/actors/${encodeURIComponent(actor.id)}/names`, { method: 'PUT', body });
      dialog.close();
      toast(result.added ? '已在映射表中新增这位演员' : '已保存到映射表');
      onSaved();
    } catch (failure) {
      error.textContent = failure.message;
      event.target.disabled = false;
    }
  };
  dialog.addEventListener('close', () => dialog.remove(), { once: true });
  dialog.showModal();
  grow();
}

export async function actorDetailPage(id, { heading, toast, reload }) {
  let actor;
  try {
    actor = await api('/api/actors/' + encodeURIComponent(id), { timeout: 120000 });
  } catch (error) {
    if (error.code === 'emby_not_configured') return embyMissing(heading, error.message);
    throw error;
  }
  const names = actor.names;
  const preview = 8;
  return {
    html: `<div class="record-nav-row"><a class="button back-button" href="#/actors"><span class="back-arrow">←</span>返回演员列表</a></div>
      <section class="panel actor-detail">
        <div class="actor-side">
          <div class="actor-avatar">${actor.image ? `<img src="${h(actor.image)}" alt="${h(actor.name)} 头像">` : `<span class="actor-initial">${initial(actor.name)}</span>`}</div>
          <button class="button primary" type="button" id="actor-avatar">🖼 修改头像</button>
          <button class="button" type="button" id="actor-names">✎ 编辑名字</button>
          <a class="button" href="${h(actor.emby_url)}" target="_blank" rel="noopener noreferrer">▶ 在 Emby 中查看 ↗</a>
        </div>
        <div class="actor-main">
          <div class="actor-title"><h1>${h(actor.name)}</h1></div>
          ${names ? `<div class="actor-chips"><span class="actor-chip jp">JP ${h(names.jp)}</span><span class="actor-chip zh">简 ${h(names.zh_cn)}</span><span class="actor-chip tw">繁 ${h(names.zh_tw)}</span></div>
            ${names.aliases.length ? `<div class="actor-chips"><span class="actor-chip alias">别名：${h(names.aliases.join('，'))}</span></div>` : ''}`
            : `<div class="actor-chips"><span class="actor-chip gray">映射表中没有这位演员 · Emby 原名：${h(actor.emby_name)}</span></div>`}
          <div class="actor-chips"><span class="actor-chip gray">关联影片 ${actor.movies.length} 部</span><span class="actor-chip gray">Gfriends 头像 ${actor.gfriends.length} 张</span><span class="actor-chip gray">Jalbum 写真 ${actor.jalbum.length} 张</span><span class="actor-chip gray">映射表：${actor.mapping.custom ? '自定义（远程）' : '内置'}</span></div>
          <div class="actor-section"><h3>简介 <small>来自 Emby</small></h3><div class="actor-overview">${actor.overview ? h(actor.overview) : '<span class="muted">暂无简介</span>'}</div></div>
          <div class="actor-section"><h3>写真 <small>Jalbum · ${actor.jalbum.length} 张</small></h3>
            ${actor.jalbum.length ? `<div class="actor-photos" id="actor-photos"></div>${actor.jalbum.length > preview ? `<div class="actor-more"><button class="button small" type="button" id="actor-photos-all">显示全部 ${actor.jalbum.length} 张</button></div>` : ''}` : '<div class="actor-none">Jalbum 中没有找到这位演员的写真</div>'}</div>
          <div class="actor-section"><h3>关联影片 <small>来自 Emby</small></h3>
            ${actor.movies.length ? `<div class="actor-works">${actor.movies.map((movie) => `<a class="actor-work" href="${h(movie.url)}" target="_blank" rel="noopener noreferrer" title="${h([movie.number, movie.title].filter(Boolean).join(' '))}"><span class="actor-work-cover">${movie.poster ? `<img src="${h(movie.poster)}" alt="" loading="lazy" decoding="async">` : `<span class="actor-work-nocover">${h(movie.number || '无封面')}</span>`}</span><span class="actor-work-num">${h(movie.number || '—')}</span><span class="actor-work-title">${h(movie.title)}</span></a>`).join('')}</div>` : '<div class="actor-none">Emby 中没有关联影片</div>'}</div>
        </div>
      </section>`,
    bind() {
      const urls = actor.jalbum.map((path) => galleryURL('jalbum', path));
      const showPhotos = (count) => {
        const box = document.querySelector('#actor-photos');
        if (!box) return;
        box.innerHTML = urls.slice(0, count).map((url, i) => `<button type="button" class="actor-photo-thumb" data-photo="${i}"><img src="${h(url)}" alt="写真 ${i + 1}" loading="lazy"></button>`).join('');
        box.querySelectorAll('[data-photo]').forEach((button) => { button.onclick = () => openViewer(urls, Number(button.dataset.photo)); });
      };
      // 预览按容器宽度显示两整行（每格至少 150px、间距 12px，与样式一致），宽屏不留空。
      const box = document.querySelector('#actor-photos');
      const rows = box ? Math.max(preview, Math.floor((box.clientWidth + 12) / 162) * 2) : preview;
      showPhotos(rows);
      // 「显示全部」和「收起」来回切换；收起时滚回写真区，避免停在页面下方。
      const all = document.querySelector('#actor-photos-all');
      if (all) {
        if (urls.length <= rows) all.parentElement.remove();
        else {
          let expanded = false;
          all.onclick = () => {
            expanded = !expanded;
            showPhotos(expanded ? urls.length : rows);
            all.textContent = expanded ? '收起' : `显示全部 ${urls.length} 张`;
            if (!expanded) box.closest('.actor-section').scrollIntoView({ block: 'start', behavior: 'smooth' });
          };
        }
      }
      const refresh = () => reload({ silent: true });
      document.querySelector('#actor-avatar').onclick = () => avatarDialog(actor, refresh, toast);
      document.querySelector('#actor-names').onclick = () => namesDialog(actor, refresh, toast);
    },
  };
}

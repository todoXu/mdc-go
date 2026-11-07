import { api, escapeHTML as h } from './api.js';
import { deleteRecordDialog, taskDialog, batchRetryDialog } from './record-actions.js';
import { durationHTML } from './duration.js';

const statusNames = { 0: '等待中', 1: '处理中', 2: '成功', '-1': '失败', '-2': '已终止' };
const statusValues = { waiting: 0, processing: 1, finished: 2, failed: -1, stopped: -2 };
const modes = { 2: '移动', 3: '原地整理', 4: 'STRM' };
const sources = { watcher: '监控', retry: '重试', reorganize: '重新整理' };
// 每页条数可在页面上选择，选择结果在本次打开期间保留。
let pageSize = 30;

// 刷新或关闭页面时记下列表滚动位置，重新打开同一地址后滚回去。
// 只记录列表页，其它页面不受影响；翻页本身由 goToPage 主动回到顶部。
if (!window.recordsRefreshBound) {
  window.recordsRefreshBound = true;
  window.addEventListener('pagehide', () => {
    if (!document.querySelector('#record-rows, #record-grid')) return;
    sessionStorage.setItem(`records-refresh:${location.hash}`, String(window.scrollY));
  });
}

function dateLabel(value) {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false });
}



// 番号后面的标记：字幕、破解方式、分辨率，数据来自后端识别的 file_info。
const mosaicLabels = { is_ltx_hack: 'LTX', is_lada_hack: 'LADA', is_jav_hack: 'JAV', is_other_hack: '破解', is_uncensored: '无码' };
function flagTags(row) {
  const info = row.file_info || {};
  const tags = [];
  if (info.has_subtitle ?? row.has_subtitle) tags.push(`<span class="record-flag record-flag-sub" title="${h(info.subtitle_marker ? `字幕标记：${info.subtitle_marker}` : '中文字幕')}">中字</span>`);
  if (mosaicLabels[info.mosaic]) tags.push(`<span class="record-flag record-flag-hack" title="${h(info.mosaic_marker ? `识别标记：${info.mosaic_marker}` : '')}">${mosaicLabels[info.mosaic]}</span>`);
  if (info.resolution) tags.push(`<span class="record-flag record-flag-res">${h(info.resolution)}</span>`);
  return tags.join('');
}

// 演员超过 2 人时显示「前两位 等 N 人」，完整名单放在悬停提示里。
// 上限来自「命名设置 → 演员显示数量上限」，由后端按记录所在目录返回。
// 超过上限时直接显示「多人作品」，与文件夹命名规则一致；完整名单保留在悬停提示里。
function actorsText(row) {
  const actors = row.actors || [];
  return actors.length > (row.actor_limit || 3) ? '多人作品' : actors.join('、');
}
function actorsHTML(row) {
  const actors = row.actors || [];
  if (!actors.length) return '<span class="record-muted">未知演员</span>';
  return `<span title="${h(actors.join('、'))}">${h(actorsText(row))}</span>`;
}

// 完整路径：目录部分灰色，文件名深色。
function pathHTML(path) {
  const index = Math.max(path.lastIndexOf('\\'), path.lastIndexOf('/'));
  // 在每个分隔符后插入 <wbr>，长路径优先在目录边界换行。
  const dir = h(path.slice(0, index + 1)).replace(/([\\/])/g, '$1<wbr>');
  return `<span class="record-path-dir">${dir}</span><span class="record-path-name">${h(path.slice(index + 1))}</span>`;
}

// 目录列第二行：成功显示整理后的完整路径；其它状态显示进度、错误原因或说明。
function secondLineHTML(row) {
  if (row.status === 2 && row.output) return `<div class="record-path-line record-path-out">${pathHTML(row.output)}</div>`;
  if (row.status === 1) return `<div class="record-info record-info-run record-progress" data-job-id="${h(row.id)}">${h(row.stop_requested ? '正在终止…' : row.progress || '开始处理…')}</div>`;
  if (row.status === 0) return '<div class="record-info record-info-muted">排队等待处理</div>';
  if (row.status === -1) return `<div class="record-info record-info-err">${h(row.error || '处理失败')}</div>`;
  if (row.status === -2) return '<div class="record-info record-info-muted">已终止</div>';
  return '';
}

// 创建时间：日期在上、时间在下。
function dateTimeHTML(value) {
  const date = new Date(value);
  if (!value || Number.isNaN(date.getTime())) return h(value || '—');
  const pad = (n) => String(n).padStart(2, '0');
  return `${date.getFullYear()}/${pad(date.getMonth() + 1)}/${pad(date.getDate())}<br>${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

const statusClasses = { 0: 'wait', 1: 'run', 2: 'ok', '-1': 'fail', '-2': 'stop' };
// 成功但有开启了「失败时特别提示」的数据源失败：显示为黄色（warn），并标出失败的数据源数量。
const failedSources = (row) => (row.status === 2 && row.notify_failed_sources?.length ? row.notify_failed_sources : []);
const stateClass = (row) => (failedSources(row).length ? 'warn' : statusClasses[row.status] || 'wait');
const stateLabel = (row) => {
  const text = row.stop_requested && row.status === 1 ? '终止中' : statusNames[row.status] || '—';
  const failed = failedSources(row);
  return failed.length ? `${text} · ${failed.length} 个源失败` : text;
};
const stateTitle = (row) => (failedSources(row).length ? `失败的数据源：${failedSources(row).join('、')}` : '');

function modeLabel(row) {
  return modes[row.link_mode] || (row.link_mode == null ? '—' : `模式 ${row.link_mode}`);
}

export function recordsTableHTML() {
  return `<section class="panel record-detail">
    <div class="record-selection" id="record-selection"><span class="record-selection-count"></span><button type="button" class="record-select-btn" id="record-select-filtered">全选</button><button type="button" class="record-select-btn" id="record-select-page" hidden>全选本页</button><div class="record-selection-actions"><button type="button" class="button small" data-bulk-action="stop">停止</button><button type="button" class="button small" data-bulk-action="retry">重新刮削</button><button type="button" class="button small danger" data-bulk-action="delete">删除</button></div></div>
    <div class="table-wrap">
      <table class="records-table">
        <thead>
          <tr>
            <th class="record-cell-select"><input type="checkbox" id="record-select-all" aria-label="选择本页全部记录"></th>
            <th class="record-cell-sequence">${sortHeader('sequence', '#', '序号')}</th>
            <th>${sortHeader('number', '番号')}</th>
            <th>${sortHeader('actors', '演员')}</th>
            <th class="record-cell-path">目录</th>
            <th>触发</th>
            <th>${sortHeader('mode', '整理模式')}</th>
            <th>${sortHeader('created', '创建时间')}</th>
            <th>用时</th>
            <th>状态</th>
            <th class="record-cell-actions">操作</th>
          </tr>
        </thead>
        <tbody id="record-rows"></tbody>
      </table>
    </div>
    <div class="poster-grid" id="record-grid" hidden></div>
    <div id="record-empty" role="status"></div>
    <div class="records-pagination">
      <button type="button" class="record-page-nav" id="record-prev">← 上一页</button>
      <div class="record-page-center">
        <nav class="record-page-list" id="record-page" aria-label="页码"></nav>
        <div class="record-page-tools">
        <form class="record-page-jump" id="record-page-jump" novalidate>跳至<input type="text" inputmode="numeric" id="record-page-input" aria-label="跳转页码">页<button type="submit" class="button small">跳转</button></form>
        <label class="record-page-size">每页
          <select id="record-page-size" aria-label="每页显示条数">${[10, 20, 30, 40, 50].map((size) => `<option value="${size}" ${size === pageSize ? 'selected' : ''}>${size}</option>`).join('')}</select>
          条
        </label>
        </div>
        <small id="record-visible-count"></small>
      </div>
      <button type="button" class="record-page-nav" id="record-next">下一页 →</button>
    </div>
  </section>
  <div popover="auto" class="record-action-menu" id="record-action-menu" aria-label="记录操作"></div>`;
}

const pageSizes = [10, 20, 30, 40, 50];
const hashQuery = () => new URLSearchParams(location.hash.split('?')[1] || '');

// 排序：序号、番号、演员、整理模式、创建时间可以排序，同一时间只按一列排序。
// 地址栏写 sort=列-方向（如 sort=actors-asc），默认按序号降序（最新的在前），此时不写入地址栏。
const sortKeys = ['sequence', 'number', 'actors', 'mode', 'created'];
const defaultSort = { key: 'sequence', dir: 'desc' };
// 第一次点某一列时的方向：序号和创建时间先显示最新的，文字列从小到大。
const firstDirection = { sequence: 'desc', created: 'desc', number: 'asc', actors: 'asc', mode: 'asc' };
const isDefaultSort = (sort) => sort.key === defaultSort.key && sort.dir === defaultSort.dir;
export function sortFromQuery(query) {
  const [key, dir] = String(query.get('sort') || '').split('-');
  return sortKeys.includes(key) && (dir === 'asc' || dir === 'desc') ? { key, dir } : { ...defaultSort };
}
const collator = new Intl.Collator('zh-CN', { numeric: true, sensitivity: 'base' });
// Go 输出的时间可能有 7 位小数，先截成 3 位再解析。
const createdTime = (row) => Date.parse(String(row.created_at || '').replace(/(\.\d{3})\d+/, '$1')) || 0;
const sortCompare = {
  sequence: (a, b) => (a.sequence || 0) - (b.sequence || 0),
  number: (a, b) => collator.compare(a.number || '', b.number || ''),
  // 按列表显示的文字排序，「多人作品」的记录排在一起；同为多人作品时再按真实演员名排序。
  actors: (a, b) => collator.compare(actorsText(a), actorsText(b)) || collator.compare((a.actors || []).join('、'), (b.actors || []).join('、')),
  mode: (a, b) => collator.compare(modeLabel(a), modeLabel(b)),
  created: (a, b) => createdTime(a) - createdTime(b),
};
// 列表页和详情页（上一条、下一条）共用，保证顺序一致。
export function sortRecords(rows, sort) {
  const compare = sortCompare[sort.key] || sortCompare.sequence;
  const sign = sort.dir === 'asc' ? 1 : -1;
  // 没有番号或没有演员的记录，无论升序降序都排在最后。
  const blank = (row) => (sort.key === 'number' && !row.number) || (sort.key === 'actors' && !row.actors?.length);
  return [...rows].sort((a, b) => {
    if (blank(a) !== blank(b)) return blank(a) ? 1 : -1;
    // 值相同时按序号降序，顺序稳定。
    return sign * compare(a, b) || (b.sequence || 0) - (a.sequence || 0);
  });
}
function sortHeader(key, label, name = label) {
  return `<button type="button" class="record-sort" data-sort="${key}" data-sort-name="${name}">${label}<span class="record-sort-arrow" aria-hidden="true"></span></button>`;
}
// 表格 / 海报：地址栏的 view 参数优先；没有时沿用上次的选择（保存在浏览器里），默认表格。
const views = ['table', 'poster'];
function viewFromURL() {
  const value = hashQuery().get('view');
  if (views.includes(value)) return value;
  const saved = localStorage.getItem('records-view');
  return views.includes(saved) ? saved : 'table';
}

// 海报模式的一张卡片。图片上不叠加标记（生成的海报已经带水印），信息分五行：
// 番号（⋮ 在右上角）、标记、演员、系列（有才显示）、状态与触发 · 模式 · 用时。
function posterPlaceholder(row) {
  const text = row.status === 1 ? `<span class="record-progress" data-job-id="${h(row.id)}">${h(row.stop_requested ? '正在终止…' : row.progress || '开始处理…')}</span>`
    : row.status === 0 ? '<span>排队等待处理</span>'
    : row.status === -1 ? `<span>${h(row.error || '处理失败')}</span>`
    : row.status === -2 ? '<span>已终止</span>'
    : '<span>没有海报</span>';
  return `<div class="poster-ph poster-ph-${statusClasses[row.status] || 'wait'}"><b>${h(row.number || '未识别')}</b>${text}</div>`;
}
function posterCardHTML(row, index, detail, isSelected) {
  const state = stateClass(row);
  const picture = row.poster
    ? `<img src="${h(row.poster)}" alt="${h(row.number || '')} 海报" loading="lazy" decoding="async" data-poster-img>`
    : posterPlaceholder(row);
  const flags = flagTags(row) || '<span class="record-muted">无标记</span>';
  return `<article class="poster-card${isSelected ? ' selected' : ''}${row.status === 1 ? ' running' : ''}" data-record-item="${index}">`
    + `<label class="poster-check" title="选择"><input type="checkbox" data-record-select="${index}" aria-label="选择记录 ${h(row.number || row.sequence)}" ${isSelected ? 'checked' : ''}></label>`
    + `<div class="poster-pic">${picture}</div>`
    + `<div class="poster-info">`
    + `<div class="poster-row poster-num"><a class="record-number-link" href="${h(detail)}">${h(row.number || '未识别')}</a></div>`
    + `<button type="button" class="record-more poster-more" aria-label="记录 ${h(row.number || row.sequence)} 的操作" aria-haspopup="menu" data-record-more="${index}">⋮</button>`
    + `<div class="poster-row">${flags}</div>`
    + `<div class="poster-row poster-actors"><i>演员：</i>${actorsHTML(row)}</div>`
    + (row.series ? `<div class="poster-row poster-series" title="${h(row.series)}"><i>系列：</i>${h(row.series)}</div>` : '')
    // 目录：与表格模式的目录列一致，第一行源文件完整路径，第二行 → 整理后完整路径（其它状态显示进度、原因或说明）。
    + `<div class="poster-dir"><div class="record-path-line"><i>目录：</i>${row.path ? pathHTML(row.path) : '—'}</div>${secondLineHTML(row)}</div>`
    + `<div class="poster-row poster-foot"><span class="record-state record-state-${state}" title="${h(stateTitle(row))}"><i></i>${h(stateLabel(row))}</span>`
    // 用时单独一段且不收缩，放不下时只省略「触发 · 模式」，用时始终完整显示。
    + `<span class="poster-meta"><span class="poster-meta-text" title="${h(`${sources[row.source] || row.source || '—'} · ${modeLabel(row)}`)}">${h(sources[row.source] || row.source || '—')} · ${h(modeLabel(row))} ·</span><span class="poster-meta-time">${durationHTML(row)}</span></span></div>`
    + `</div></article>`;
}
// 每个筛选标签对应的状态码；地址栏用重复的 status 参数保存，与原版 mdcng 的格式一致。
export const statusTabs = { all: [2, -1, -2, 0, 1], waiting: [0], processing: [1], finished: [2], failed: [-1], stopped: [-2] };
const sameStatuses = (a, b) => [...a].sort((x, y) => x - y).join(',') === [...b].sort((x, y) => x - y).join(',');
// 地址栏里的 status 参数对应的筛选标签；没有参数或与标签不一致时视为「全部」。
export function filterFromURL() {
  const values = hashQuery().getAll('status').map(Number).filter(Number.isFinite);
  if (!values.length) return 'all';
  const match = Object.entries(statusTabs).find(([, statuses]) => sameStatuses(statuses, values));
  return match ? match[0] : 'all';
}
// 把当前筛选写入地址栏，只替换 status 参数，其它参数保持不变。
export function syncStatusURL(filter) {
  const params = hashQuery();
  params.delete('status');
  for (const status of statusTabs[filter] || statusTabs.all) params.append('status', String(status));
  const base = location.hash.split('?')[0];
  history.replaceState(null, '', `${location.pathname}${location.search}${base}?${params}`);
}

export function mountRecordsTable({ jobs, filter, setFilter, empty, reload, toast }) {
  const $ = (selector) => document.querySelector(selector);
  // 记住本次挂载对应的列表容器，用来判断这个挂载是否已被新的页面渲染替换。
  const host = $('#record-rows');
  // 页码和每页条数写在地址栏里（?page=2&page_size=10），刷新或从详情页返回时保持不变。
  const requestedSize = Number(hashQuery().get('page_size'));
  if (pageSizes.includes(requestedSize)) pageSize = requestedSize;
  let page = Math.max(1, Number(hashQuery().get('page')) || 1);
  let view = viewFromURL();
  let sort = sortFromQuery(hashQuery());
  $('#record-page-size').value = String(pageSize);
  // 记住列表的滚动位置：离开列表时保存，从详情页「返回列表」回来时恢复。
  const scrollKey = (hash) => `records-scroll:${hash.split('?')[0]}`;
  if (!window.recordsScrollBound) {
    window.recordsScrollBound = true;
    // 点击记录（进入详情）之前保存滚动位置；路由切换后页面内容已被替换，无法再读取。
    document.addEventListener('click', (event) => {
      if (!event.target.closest?.('#record-rows, #record-grid, #record-action-menu') || !document.querySelector('#record-rows')) return;
      sessionStorage.setItem(scrollKey(location.hash), String(window.scrollY));
    }, true);
  }
  // 浏览器默认的滚动恢复会与自定义逻辑打架：关掉它，刷新和返回列表都由本页自己恢复位置。
  if ('scrollRestoration' in history) history.scrollRestoration = 'manual';
  const restoreScroll = () => {
    // 保存的位置只使用一次：恢复后立即清除，之后刷新或从其他页面进入列表都不会再跳转。
    const key = scrollKey(location.hash);
    const saved = Number(sessionStorage.getItem(key));
    sessionStorage.removeItem(key);
    if (!hashQuery().has('page') || !(saved > 0)) return false;
    // 路由渲染完成后会把页面滚到顶部，所以要等到这一步之后再恢复。
    setTimeout(() => window.scrollTo(0, saved), 0);
    return true;
  };
  // 刷新页面后恢复滚动位置。海报是懒加载的，图片没加载完时页面高度不够、滚动会被截断，
  // 所以隔一小段时间重试，直到到位或超时；用户一滚动就停止，不抢滚动条。
  const restoreRefreshScroll = () => {
    const key = `records-refresh:${location.hash}`;
    const target = Number(sessionStorage.getItem(key));
    sessionStorage.removeItem(key);
    if (!(target > 0)) return;
    let tries = 0;
    let stopped = false;
    const stop = () => { stopped = true; };
    window.addEventListener('wheel', stop, { once: true, passive: true });
    window.addEventListener('touchmove', stop, { once: true, passive: true });
    window.addEventListener('keydown', stop, { once: true });
    const attempt = () => {
      if (stopped) return;
      window.scrollTo(0, target);
      if (Math.abs(window.scrollY - target) > 2 && tries++ < 20) setTimeout(attempt, 100);
    };
    // 路由渲染完会把页面滚到顶部，等这一步之后再恢复。
    setTimeout(attempt, 0);
  };
  // 详情页沿用列表当前的状态筛选和搜索，用于计算上一条、下一条。
  const detailURL = (row) => {
    const params = new URLSearchParams();
    for (const status of statusTabs[filter] || statusTabs.all) params.append('status', String(status));
    const q = document.querySelector('#task-search')?.value.trim();
    if (q) params.set('q', q);
    params.set('page', String(page));
    params.set('page_size', String(pageSize));
    params.set('view', view);
    if (!isDefaultSort(sort)) params.set('sort', `${sort.key}-${sort.dir}`);
    const base = location.hash.split('?')[0].split('/records/')[0];
    return `${base}/records/${encodeURIComponent(row.id)}?${params}`;
  };
  // 把当前页码写入地址栏，不触发页面重新加载。
  const syncURL = () => {
    const params = hashQuery();
    params.set('page', String(page));
    params.set('page_size', String(pageSize));
    params.set('view', view);
    if (isDefaultSort(sort)) params.delete('sort');
    else params.set('sort', `${sort.key}-${sort.dir}`);
    const base = location.hash.split('?')[0];
    history.replaceState(null, '', `${location.pathname}${location.search}${base}?${params}`);
  };
  const selected = new Set();
  let busy = false;
  const confirmBulkDelete = (count) => new Promise((resolve) => {
    const dialog = document.createElement('dialog');
    dialog.className = 'record-editor';
    dialog.innerHTML = `<form method="dialog"><header><h2>删除记录</h2><button class="icon-button" value="cancel" aria-label="关闭">×</button></header><div class="record-editor-body"><p>确定删除选中的 ${count} 条记录？</p><label class="record-delete-media"><input type="checkbox" name="delete_media"> 同时删除媒体数据</label><p class="record-delete-warning">按整理模式删除视频和产物；失败的记录会删除源视频，以及已经存在的产物。</p></div><footer><button class="button" value="cancel">取消</button><button class="button primary" value="confirm">确认删除</button></footer></form>`;
    document.body.append(dialog);
    dialog.addEventListener('close', () => { resolve(dialog.returnValue === 'confirm' ? Boolean(dialog.querySelector('[name=delete_media]').checked) : null); dialog.remove(); }, { once: true });
    dialog.showModal();
  });
  const runAction = async (row, action) => {
    $('#record-action-menu').hidePopover();
    if (busy) return;
    if (action === 'delete') {
      deleteRecordDialog(row, async () => {
        await reload();
        toast('已删除记录');
      });
      return;
    }
    if (action === 'retry') {
      taskDialog(row, reload);
      return;
    }
    if (action === 'reorganize' && !window.confirm('使用当前配置重新整理该视频，并通过刮削器重新下载图片、生成水印？下载失败或遇到其他视频的同名文件会停止。')) return;
    busy = true;
    try {
      const endpoint = '/api/scrape/jobs/' + encodeURIComponent(row.id);
      await api(endpoint + '/' + action, { method: 'POST' });
      await reload();
      toast(action === 'stop' ? '已请求终止' : '已加入处理队列');
    } catch (error) { toast(error.message, true); }
    finally { busy = false; }
  };
  const runBulkAction = async (action) => {
    if (busy) return;
    if (action === 'retry') {
      // 按序号从小到大提交，服务端按提交顺序开始刮削。
      const rows = jobs.filter((row) => selected.has(row.id) && [2, -1, -2].includes(row.status)).sort((a, b) => a.sequence - b.sequence);
      if (!rows.length) { toast('所选记录没有可执行的项目', true); return; }
      batchRetryDialog(rows, {
        // 窗口关闭时就取消勾选：列表在有勾选的记录时会暂停自动刷新，取消后才能看到正在刮削的记录。
        onStart: () => {
          rows.forEach((row) => selected.delete(row.id));
          render();
          toast(`正在提交重新刮削：0 / ${rows.length}`);
        },
        onProgress: (done, total) => { if (done < total) toast(`正在提交重新刮削：${done} / ${total}`); },
        onDone: async (success, failed) => {
          // 提交期间已经离开这个列表时不重新加载，以免刷掉当前页面；列表本身由推送事件更新。
          if (host.isConnected) await reload();
          toast(`已加入处理队列：${success} 条${failed ? `，失败 ${failed} 条` : ''}`, failed > 0);
        },
      });
      return;
    }
    const rows = jobs.filter((row) => selected.has(row.id));
    if (!rows.length) return;
    const eligible = rows.filter((row) => action === 'stop' ? (row.status === 0 || row.status === 1) && !row.stop_requested : action === 'retry' ? [2, -1, -2].includes(row.status) : row.status !== 0 && row.status !== 1);
    if (!eligible.length) { toast('所选记录没有可执行的项目', true); return; }
    let deleteMedia = false;
    if (action === 'delete') {
      const choice = await confirmBulkDelete(eligible.length);
      if (choice === null) return;
      deleteMedia = choice;
    }
    busy = true;
    let success = 0, failed = 0;
    try {
      for (const row of eligible) {
        try {
          const endpoint = '/api/scrape/jobs/' + encodeURIComponent(row.id);
          if (action === 'delete') {
            const removeMedia = deleteMedia && Boolean(row.output || row.path);
            await api(endpoint, { method: 'DELETE', body: removeMedia ? { delete_media: true } : {} });
          }
          else await api(endpoint + '/' + action, { method: 'POST' });
          selected.delete(row.id);
          success++;
        } catch { failed++; }
      }
      await reload();
      toast(`${action === 'stop' ? '停止' : action === 'retry' ? '重新刮削' : '删除'}完成：${success} 条${failed ? `，失败 ${failed} 条` : ''}` , failed > 0);
    } finally { busy = false; }
  };
  const openMenu = (row, trigger) => {
    // 每次打开都重新查找菜单：页面重新渲染后，挂载时保存的节点已脱离文档，showPopover 会报错。
    const menu = $('#record-action-menu');
    const active = row.status === 0 || row.status === 1;
    const options = [
      ['view', '查看', false], ['retry', '重试', ![2, -1, -2].includes(row.status)],
      ['stop', '终止', !active || row.stop_requested], ['reorganize', '重新整理', row.status !== 2], ['delete', '删除', active],
    ];
    menu.innerHTML = options.map(([value, name, disabled]) => `<button type="button" data-record-action="${value}" ${disabled ? 'disabled' : ''}>${name}</button>`).join('');
    // 页面整体放大时，菜单的 left/top 按放大后的单位计算，屏幕坐标要先除以放大倍数。
    // 菜单隐藏时 currentCSSZoom 总是 1，所以读取可见的触发按钮（与菜单在同一个放大区域内）。
    const zoom = trigger.currentCSSZoom || 1;
    const bounds = trigger.getBoundingClientRect();
    const width = window.innerWidth / zoom, height = window.innerHeight / zoom;
    const right = bounds.right / zoom, top = bounds.top / zoom, bottom = bounds.bottom / zoom;
    menu.style.left = Math.max(8, Math.min(right - 170, width - 178)) + 'px';
    menu.style.top = (bottom + 8 + 242 > height ? Math.max(8, top - 250) : bottom + 8) + 'px';
    menu.showPopover();
    menu.querySelectorAll('[data-record-action]').forEach((button) => {
      button.onclick = () => {
        if (button.dataset.recordAction === 'view') { menu.hidePopover(); location.hash = detailURL(row); }
        else runAction(row, button.dataset.recordAction);
      };
    });
  };
  // 翻页后回到页面顶部，方便从第一行开始查看。
  const goToPage = (next) => {
    if (next === page) return;
    page = next;
    render();
    window.scrollTo({ top: 0, behavior: 'smooth' });
  };
  const render = () => {
    const search = $('#task-search').value.trim().toLowerCase();
    const statuses = statusTabs[filter] || statusTabs.all;
    const filtered = jobs.filter((row) =>
      // 没有演员时列表显示「未知演员」，搜索也要能匹配到这几个字。
      // 海报地址不参与搜索，否则搜「poster」会匹配所有记录。
      statuses.includes(row.status) && (JSON.stringify({ ...row, poster: undefined }) + (row.actors?.length ? '' : ' 未知演员')).toLowerCase().includes(search),
    );
    // 全选针对所有符合当前搜索和状态筛选的记录，不限于当前页。
    const selectFiltered = $('#record-select-filtered');
    if (selectFiltered) {
      const allSelected = filtered.length > 0 && filtered.every((row) => selected.has(row.id));
      selectFiltered.disabled = !filtered.length;
      selectFiltered.textContent = allSelected ? '取消全选' : `全选全部（${filtered.length}）`;
      selectFiltered.title = allSelected ? '取消选择当前筛选出的全部记录' : `选择当前筛选出的全部 ${filtered.length} 条记录（不限于当前页）`;
      selectFiltered.onclick = () => {
        filtered.forEach((row) => (allSelected ? selected.delete(row.id) : selected.add(row.id)));
        render();
      };
    }
    const pages = Math.max(1, Math.ceil(filtered.length / pageSize));
    page = Math.min(page, pages);
    syncURL();
    // 只有当前排序的列显示箭头：↑ 升序，↓ 降序。
    document.querySelectorAll('[data-sort]').forEach((button) => {
      const active = button.dataset.sort === sort.key;
      button.classList.toggle('active', active);
      button.querySelector('.record-sort-arrow').textContent = active ? (sort.dir === 'asc' ? '↑' : '↓') : '';
      button.title = active ? `按${button.dataset.sortName}${sort.dir === 'asc' ? '升序' : '降序'}排列，点击切换` : `按${button.dataset.sortName}排序`;
      button.closest('th').setAttribute('aria-sort', active ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none');
    });
    const start = (page - 1) * pageSize;
    const visible = sortRecords(filtered, sort).slice(start, start + pageSize);
    const poster = view === 'poster';
    document.querySelectorAll('[data-record-view]').forEach((button) => button.classList.toggle('active', button.dataset.recordView === view));
    $('.records-table').closest('.table-wrap').hidden = poster;
    $('#record-grid').hidden = !poster;
    // 列表随推送刷新时，地址没变的海报沿用已经加载好的 <img>，不重新下载，也不会闪。
    const loaded = new Map([...$('#record-grid').querySelectorAll('img[data-poster-img]')].filter((img) => img.complete && img.naturalWidth).map((img) => [img.getAttribute('src'), img]));
    $('#record-grid').innerHTML = poster ? visible.map((row, index) => posterCardHTML(row, index, detailURL(row), selected.has(row.id))).join('') : '';
    $('#record-grid').querySelectorAll('img[data-poster-img]').forEach((img) => {
      const old = loaded.get(img.getAttribute('src'));
      if (old) { img.replaceWith(old); loaded.delete(img.getAttribute('src')); }
    });
    // 海报加载失败（文件被删除等）时换成占位，卡片高度不变。
    $('#record-grid').querySelectorAll('[data-poster-img]').forEach((img) => {
      img.onerror = () => { img.outerHTML = posterPlaceholder({ ...visible[Number(img.closest('[data-record-item]').dataset.recordItem)], status: 2 }); };
    });
    // 点卡片进入详情页；选择框、⋮ 菜单和番号链接各自处理。
    $('#record-grid').querySelectorAll('[data-record-item]').forEach((card) => {
      card.onclick = (event) => {
        if (event.target.closest('a,button,input,label') || window.getSelection()?.toString()) return;
        location.hash = detailURL(visible[Number(card.dataset.recordItem)]);
      };
    });
    $('#record-rows').innerHTML = poster ? '' : visible.map((row, index) =>
      `<tr class="record-clickable${selected.has(row.id) ? ' record-selected' : ''}${row.status === 1 ? ' record-running' : ''}">`
      + `<td class="record-cell-select"><input type="checkbox" data-record-select="${index}" aria-label="选择记录 ${h(row.number || row.sequence)}" ${selected.has(row.id) ? 'checked' : ''}></td>`
      + `<td class="record-cell-sequence">${h(row.sequence ?? start + index + 1)}</td>`
      + `<td class="record-cell-number"><a class="record-number-link" href="${h(detailURL(row))}">${h(row.number || '未识别')}</a>${flagTags(row)}</td>`
      + `<td class="record-cell-actors">${actorsHTML(row)}</td>`
      + `<td class="record-cell-path"><div class="record-path-line">${row.path ? pathHTML(row.path) : '—'}</div>${secondLineHTML(row)}</td>`
      + `<td class="record-cell-gray">${h(sources[row.source] || row.source || '—')}</td>`
      + `<td class="record-cell-gray">${h(modeLabel(row))}</td>`
      + `<td class="record-cell-time" title="${h(row.created_at)}">${dateTimeHTML(row.created_at)}</td>`
      + `<td class="record-cell-duration">${durationHTML(row)}</td>`
      + `<td><span class="record-state record-state-${stateClass(row)}" title="${h(stateTitle(row))}"><i></i>${h(stateLabel(row))}</span></td>`
      + `<td class="record-cell-actions"><button type="button" class="record-more" aria-label="记录 ${h(row.number || row.sequence)} 的操作" aria-haspopup="menu" data-record-more="${index}">⋮</button></td></tr>`,
    ).join('');
    $('#record-rows').querySelectorAll('tr').forEach((tr, index) => {
      const row = visible[index];
      tr.onclick = (event) => {
        if (event.target.closest('a,button,input,label') || window.getSelection()?.toString()) return;
        location.hash = detailURL(row);
      };
    });
    $('#record-empty').innerHTML = filtered.length ? '' : empty(
      search || filter !== 'all' ? '没有符合筛选条件的记录' : '该监控目录暂无刮削记录',
      search || filter !== 'all' ? '试试其他关键词或状态。' : '扫描到新视频后，记录会显示在这里。', '☷',
    );
    $('#record-visible-count').textContent = `共 ${filtered.length} 条${filtered.length ? ` · 显示 ${start + 1}–${start + visible.length} 条` : ''}`;
    // 页码：总是显示首页、末页和当前页前后两页，中间用 … 省略，例如 1 … 98 99 [100] 101 102 … 200。
    const numbers = [];
    for (let n = 1; n <= pages; n++) {
      if (n === 1 || n === pages || Math.abs(n - page) <= 2) numbers.push(n);
      else if (numbers[numbers.length - 1] !== '…') numbers.push('…');
    }
    $('#record-page').innerHTML = numbers.map((n) => n === '…'
      ? '<span class="record-page-gap">…</span>'
      : `<button type="button" class="record-page-num${n === page ? ' active' : ''}" data-page="${n}" ${n === page ? 'aria-current="page"' : ''}>${n}</button>`).join('');
    $('#record-page').querySelectorAll('[data-page]').forEach((button) => {
      button.onclick = () => goToPage(Number(button.dataset.page));
    });
    $('#record-page-input').dataset.pages = String(pages);
    $('#record-page-input').placeholder = `1–${pages}`;
    $('#record-prev').disabled = page === 1;
    $('#record-next').disabled = page === pages;
    document.querySelectorAll('[data-record-more]').forEach((button) => {
      button.onclick = () => openMenu(visible[Number(button.dataset.recordMore)], button);
    });
    const selectionChanged = () => {
      const count = visible.filter((row) => selected.has(row.id)).length;
      $('#record-select-all').checked = count > 0 && count === visible.length;
      $('#record-select-all').indeterminate = count > 0 && count < visible.length;
      $('#record-select-all').disabled = !visible.length;
      // 海报模式看不到表头的全选框，在操作栏里补一个「全选本页」。
      const pageButton = $('#record-select-page');
      pageButton.hidden = !poster;
      pageButton.disabled = !visible.length;
      pageButton.textContent = count > 0 && count === visible.length ? '取消本页' : `全选本页（${visible.length}）`;
      $('#record-selection .record-selection-count').textContent = `已选择 ${selected.size} 条记录`;
      $('#record-selection').querySelectorAll('[data-bulk-action]').forEach((button) => {
        const action = button.dataset.bulkAction;
        button.disabled = !visible.some((row) => selected.has(row.id) && (action === 'stop' ? (row.status === 0 || row.status === 1) && !row.stop_requested : action === 'retry' ? [2, -1, -2].includes(row.status) : row.status !== 0 && row.status !== 1));
      });
      document.querySelectorAll('[data-record-select]').forEach((box) => {
        const holder = box.closest('tr, [data-record-item]');
        holder?.classList.toggle(holder.tagName === 'TR' ? 'record-selected' : 'selected', box.checked);
      });
    };
    document.querySelectorAll('[data-record-select]').forEach((box) => {
      box.onchange = () => { const id = visible[Number(box.dataset.recordSelect)].id; if (box.checked) selected.add(id); else selected.delete(id); selectionChanged(); };
    });
    $('#record-select-all').onchange = (event) => {
      visible.forEach((row) => { if (event.target.checked) selected.add(row.id); else selected.delete(row.id); });
      render();
    };
    $('#record-select-page').onclick = () => {
      const all = visible.length > 0 && visible.every((row) => selected.has(row.id));
      visible.forEach((row) => { if (all) selected.delete(row.id); else selected.add(row.id); });
      render();
    };
    selectionChanged();
  };
  $('#record-selection').querySelectorAll('[data-bulk-action]').forEach((button) => {
    button.onclick = () => runBulkAction(button.dataset.bulkAction);
  });
  // 表格 / 海报切换：写入地址栏并记住选择，页码和筛选保持不变。
  document.querySelectorAll('[data-record-view]').forEach((button) => {
    button.onclick = () => {
      if (view === button.dataset.recordView) return;
      view = button.dataset.recordView;
      localStorage.setItem('records-view', view);
      render();
    };
  });
  document.querySelectorAll('[data-filter]').forEach((button) => {
    button.onclick = () => {
      filter = button.dataset.filter;
      setFilter(filter);
      syncStatusURL(filter);
      page = 1;
      document.querySelectorAll('[data-filter]').forEach((item) => item.classList.toggle('active', item === button));
      render();
    };
  });
  $('#task-search').oninput = () => { page = 1; render(); };
  // 点同一列切换升序 / 降序；点另一列改按那一列排序。排序变化后回到第一页。
  document.querySelectorAll('[data-sort]').forEach((button) => {
    button.onclick = () => {
      const key = button.dataset.sort;
      sort = key === sort.key ? { key, dir: sort.dir === 'asc' ? 'desc' : 'asc' } : { key, dir: firstDirection[key] };
      page = 1;
      render();
    };
  });
  // 跳转到指定页：超出范围时取最近的有效页。
  $('#record-page-jump').onsubmit = (event) => {
    event.preventDefault();
    const input = $('#record-page-input');
    const target = Math.round(Number(input.value.trim()));
    if (!input.value.trim() || !Number.isFinite(target)) { input.value = ''; return; }
    input.value = '';
    goToPage(Math.max(1, Math.min(Number(input.dataset.pages) || 1, target)));
  };
  $('#record-prev').onclick = () => goToPage(page - 1);
  $('#record-next').onclick = () => goToPage(page + 1);
  $('#record-page-size').onchange = (event) => {
    pageSize = Number(event.target.value);
    page = 1;
    render();
  };
  render();
  if (!restoreScroll()) restoreRefreshScroll();
  // 推送到来时只更新表格行，不重建整个页面；分页、选择和搜索状态都保留。
  return {
    update(next) {
      // 页面已重新渲染（例如删除后整页刷新），这个挂载已经过期。它的定时刷新若继续执行，
      // 会把旧的复选框处理函数写回当前列表，导致之后的选择和批量操作失效。
      if (!host.isConnected) return;
      jobs = next;
      render();
    },
  };
}

import { api, escapeHTML as h } from './api.js';
import { recordsTableHTML, mountRecordsTable, filterFromURL, syncStatusURL } from './records.js';
import { scrapeRecordPage } from './record-detail.js';
import { mountPriorityEditor } from './priority.js';
import { mountFailureNotify } from './failure-notify.js';
import { actorsPage, actorDetailPage } from './actors.js';
import { imageGallery } from './gallery.js';
import { watchSettings } from './watch.js';
import { downloadSettings } from './download.js';
import { namingSettings } from './naming.js';
import { watermarkSettings } from './watermark.js';
import { metadataSettings } from './metadata.js';
import { nfoSettings } from './nfo.js';
import { systemSettings } from './system.js';
import { onJobEvents, clearJobEvents, connectJobEvents, flushMissedJobEvents } from './job-events.js';
connectJobEvents();
import { logsPage } from './logs.js';
import { userSettings, applyUserProfile } from './user.js';
import { embySettings } from './emby.js';
import { overviewPage } from './overview.js';
import { loadPlatform, platformName, platformVersion, isAbsolutePath, absolutePathHint } from './platform.js';

const $ = (selector, parent = document) => parent.querySelector(selector);
const main = $('#main');
// 点击弹窗外的遮罩时关闭弹窗；点击弹窗内部（表单、按钮、输入框）不关闭。
// 只有按下和松开都在遮罩上才算点击遮罩：在输入框里按住鼠标拖到弹窗外松开（例如选中文字）时，
// 浏览器也会把 click 发给弹窗本身，不能因此关闭。
let backdropPress = null;
document.addEventListener('pointerdown', (event) => {
  backdropPress = event.target instanceof HTMLDialogElement ? event.target : null;
}, true);
document.addEventListener('click', (event) => {
  const dialog = event.target instanceof HTMLDialogElement ? event.target : null;
  const pressed = backdropPress;
  backdropPress = null;
  if (dialog?.open && dialog === pressed) dialog.close();
});
// 刷新时由本程序自己决定滚动位置，不让浏览器恢复旧位置。
if ('scrollRestoration' in history) history.scrollRestoration = 'manual';
const nav = [
  ['/', '▦', '概览', 'workspace'],
  ['/tasks', '☷', '刮削记录', 'workspace'],
  ['/actors', '♙', '演员管理', 'workspace'],
  ['/providers', '◈', '数据源', 'system'],
  ['/settings', '⚙', '设置', 'system'],
  ['/logs', '≡', '日志', 'system'],
];
const labels = {
  target_folder: '整理目标目录',
  delete_metadata_folder_when_failed: '失败后清理元数据目录',
  target_overwrite_video: '覆盖已有视频',
  target_overwrite_pic: '覆盖已有图片',
  link_mode: '文件整理方式',
  delete_empty_parent_after_move: '移动后清理空目录',
  file_size_filter: '文件大小下限（字节）',
  file_ext_filter: '视频扩展名',
  file_name_blacklist: '文件名黑名单',
  subtitle_keywords: '中文字幕关键词',
  other_hack_keywords: '其他破解关键词',
  uncensored_keywords: '无码关键词',
  jav_hack_keywords: 'JAV 破解关键词',
  lada_hack_keywords: 'LADA 破解关键词',
  ltx_hack_keywords: 'LTX 破解关键词',
  watcher_enabled: '启用目录监控',
  watch_dirs: '监控目录',
  poster: '海报',
  thumb: '封面',
  fanart: '背景图',
  extra_fanarts: '额外剧照',
  trailer: '预告片',
  thumb_download_strategy: '封面下载策略',
  crop_type_censored: '常规作品海报裁剪',
  crop_type_fc2: 'FC2 海报裁剪',
  crop_aspect: '裁剪比例',
  crop_poster: '裁剪海报',
  crop_when_poster_low_res: '低分辨率时重新裁剪',
  folder: '目录命名模板',
  file: '文件命名模板',
  title: '标题模板',
  suffix: '文件后缀模板',
  max_folder_length: '目录名称长度上限',
  max_actor_count: '演员数量上限',
  image_with_prefix: '图片使用文件名前缀',
  suffixs: '后缀规则',
  has_subtitle: '含字幕',
  is_censored: '有码',
  is_uncensored: '无码',
  part_naming: '分集命名',
  resolution_type: '分辨率命名类型',
  resolution_naming: '分辨率命名模板',
  resolution_detect_method: '分辨率识别方式',
  resolution_detect_fallback: '识别失败时回退',
  resolution_hd: '720p 标记',
  resolution_fhd: '1080p 标记',
  resolution_uhd: '4K 标记',
  resolution_uhd8: '8K 标记',
  add_chs_to_subtitle: '添加中文字幕标记',
  enabled: '启用',
  custom_folder: '自定义水印目录',
  mode: '水印排列方向',
  start: '起始位置',
  scale: '缩放比例',
  offset_x: '水平偏移',
  offset_y: '垂直偏移',
  gap: '水印间距',
  proxy: '代理地址',
  flaresolverr: 'FlareSolverr 地址',
  strict_mode: '严格匹配',
  image_strict_mode: '严格匹配图片',
  use_actor_mapping: '启用演员名称映射',
  use_tag_mapping: '启用标签映射',
  outline_trim_multiple_br: '清理简介多余换行',
  static_data_lang: '元数据语言',
  auto_translate_title: '自动翻译标题',
  auto_translate_outline: '自动翻译简介',
  translate_engine: '翻译服务',
  deepl_endpoint: 'DeepL 接口地址',
  deepl_api_key: 'DeepL API Key',
  openai_custom_prompt: '自定义翻译提示词',
  additional_tags: '额外标签',
  serial_name: '系列名称',
  actors: '演员',
  resolution: '分辨率',
  mosaic: '影片类型',
  subtitle_format: '字幕标签格式',
  series: '系列',
  series_format: '系列标签格式',
  studio: '制作商',
  studio_format: '制作商标签格式',
  publisher: '发行商',
  publisher_format: '发行商标签格式',
  scrape_info: '获取演员资料',
  scrape_pics: '下载演员图片',
  emby_config: 'Emby 连接',
  overwrite_mode: '覆盖方式',
  server_address: '服务器地址',
  api_key: 'API Key',
  user_id: '用户 ID',
  library_ids: '媒体库 ID',
  refresh_after_scrape: '刮削后刷新',
  scrape_thread_count: '刮削并发数',
  scrape_timeout: '刮削超时（秒）',
  openai_key: 'OpenAI API Key',
  openai_base: 'OpenAI 接口地址',
  openai_model: 'OpenAI 模型',
  base_url: '站点地址',
  cookies: 'Cookies',
  user_agent: 'User-Agent',
  cooldown_seconds: '请求间隔（秒）',
  request_timeout: '请求超时（秒）',
  trailer_timeout: '预告片超时（秒）',
  retry_times: '失败重复次数',
  sanitize_actors_info: '清理演员信息',
  add_part_to_title: '标题添加分集标记',
  add_translated_by: '标记翻译来源',
  tagline_format: '副标题格式（tagline）',
  set_by_actress: '按演员设置',
  set_by_series: '按系列设置',
  set_by_serial_name: '按系列名称设置',
};
const enums = {
  link_mode: [
    ['2', '移动'],
    ['3', '原地整理'],
    ['4', 'STRM'],
  ],
  translate_engine: ['Google', 'DeepL', 'Deeplx', 'OpenAI'],
  static_data_lang: [
    ['zh_cn', '简体中文'],
    ['zh_tw', '繁体中文'],
    ['jp', '日文'],
  ],
  thumb_download_strategy: ['Priority', 'Quality'],
  crop_aspect: ['Full', 'Emby'],
  resolution_detect_method: ['Real', 'ByPath'],
  mode: ['Stack', 'CounterClockwise', 'Clockwise'],
};
const nfoLabels = {
  sorttitle: '排序标题',
  originaltitle: '原始标题',
  outline: '简介',
  plot: '剧情',
  originalplot: '原始剧情',
  release: '发行日期',
  releasedate: '发布日期',
  premiered: '首映日期',
  country: '国家',
  mpaa: '分级',
  customrating: '自定义分级',
  year: '年份',
  runtime: '时长',
  votes: '投票数',
  score: '评分',
  criticrating: '影评评分',
  actor: '演员',
  director: '导演',
  series: '系列',
  tag: '标签',
  genre: '类型',
  studio: '制作商',
  maker: '厂商',
  publisher: '发行商',
  label: '厂牌',
  poster: '海报',
  cover: '封面',
  trailer: '预告片',
  website: '网站',
};
const fieldHelp = {
  target_folder: '完整的目录路径',
  file_size_filter: '小于此值的文件会被过滤，0 表示不限',
  max_actor_count: '0 表示不限',
  request_timeout: '单个请求最长等待时间，0 表示使用默认值 20 秒。',
  trailer_timeout: '预告片下载最长等待时间，0 表示使用默认值 60 秒。',
  retry_times: '请求失败后额外重复请求的次数，0 表示不重试，最多 5 次。',
  max_folder_length: '0 表示不限',
  scrape_thread_count: '同时处理的任务数量，上限 20',
  scrape_timeout: '单个任务的最长等待时间',
  proxy: '支持填写 HTTP 或 SOCKS 代理地址',
  folder: '使用命名变量组合目录名称',
  file: '使用命名变量组合文件名称',
  watcher_enabled: '目录监控功能正在接入',
};
let generation = 0,
  dirty = false,
  currentHash = location.hash || '#/',
  toastTimer,
  taskFilter = 'all';
let pendingNavigation = null;

function label(key) {
  if (labels[key]) return labels[key];
  if (key.startsWith('enable_')) return '写入' + (nfoLabels[key.slice(7)] || key.slice(7));
  if (key.startsWith('watermark_'))
    return (
      ({
        subtitle: '字幕',
        hack: '破解',
        uncensored: '无码',
        censored: '有码',
        resolution: '分辨率',
      }[key.slice(10).replace('_fixed', '')] || key) +
      (key.endsWith('_fixed') ? '固定位置' : '水印')
    );
  return key.replaceAll('_', ' ');
}
function toast(message, error = false) {
  const box = $('#toast');
  box.textContent = message;
  box.className = 'toast visible' + (error ? ' error' : '');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => box.classList.remove('visible'), 4500);
}
function notice() {
  return '<div class="notice"><span>ⓘ</span><div><strong>开发预览</strong> · 数据源配置可以保存；刮削、文件整理及图片处理功能正在接入，暂不执行实际操作。</div></div>';
}
function heading(title, subtitle, action = '') {
  return `<div class="page-heading"><div><div class="eyebrow">MEDIA WORKSPACE</div><h1>${h(title)}</h1><p class="subtitle">${h(subtitle)}</p></div>${action}</div>`;
}
function empty(title, desc, icon = '▱', action = '') {
  return `<div class="empty"><div class="empty-icon">${icon}</div><h3>${h(title)}</h3><p>${h(desc)}</p>${action}</div>`;
}
function loading() {
  main.innerHTML = '<div class="skeleton">正在载入工作空间…</div>';
}
function setSettingsDirty(value) {
  dirty = value;
  main.classList.toggle('has-unsaved-changes', value);
}
let settingsBaseline = '';
function settingFormState() {
  const form = main.querySelector('.settings-layout form');
  if (!form) return '';
  return JSON.stringify({
    controls: [...form.querySelectorAll('input, select, textarea')].map((control) => ({
      id: control.id,
      name: control.name,
      type: control.type,
      value: control.type === 'checkbox' || control.type === 'radio' ? control.checked : control.value,
    })),
    tags: [...form.querySelectorAll('.tag-chip')].map((chip) => chip.dataset.value),
  });
}
function refreshSettingsDirty() {
  setSettingsDirty(Boolean(settingsBaseline) && settingFormState() !== settingsBaseline);
}
function clearSettingsDirty() {
  settingsBaseline = settingFormState();
  setSettingsDirty(false);
}
function button(text, id, primary = false) {
  return `<button class="button${primary ? ' primary' : ''}" id="${id}">${text}</button>`;
}
function setStatus(online) {
  $('#connection').classList.toggle('offline', !online);
  $('#connection').innerHTML = `<i></i>${online ? '本地服务已连接' : '本地服务未连接'}`;
}
async function checkStatus() {
  try {
    await api('/healthz');
    setStatus(true);
  } catch {
    setStatus(false);
  }
}

function navigation(path) {
  $('#nav').innerHTML = nav
    .map(
      ([url, icon, text, group], i) =>
        `${group === 'system' && nav[i - 1]?.[3] !== 'system' ? '<div class="nav-divider"></div><div class="nav-group-label">系统管理</div>' : ''}<a href="#${url}" class="${path === url || (url !== '/' && path.startsWith(url)) ? 'active' : ''}"><span class="nav-icon">${icon}</span>${text}</a>`,
    )
    .join('');
  const current = nav.find(([url]) => path === url || (url !== '/' && path.startsWith(url)));
  $('#breadcrumb').innerHTML =
    `${current?.[3] === 'system' ? '系统管理' : '工作空间'} <span>/</span> ${h(current?.[2] || '概览')}`;
}
async function route(options = {}) {
  const token = ++generation,
    path = decodeURI((location.hash || '#/').slice(1)).split('?')[0];
  currentHash = location.hash || '#/';
  settingsBaseline = '';
  setSettingsDirty(false);
  navigation(path);
  // 演员管理的字号和图片已按大尺寸设计，其它页面整体放大到同样的观感（见 style.css 的 --ui-zoom）。
  main.classList.toggle('page-zoom', !path.startsWith('/actors'));
  $('#sidebar').classList.remove('open');
  // 每个页面自己重新订阅推送，所以切换页面前先清空上一个页面的订阅。
  clearJobEvents();
  // 推送触发的刷新不显示加载骨架，避免处理中的记录每次变化都闪一下。
  if (!options.silent) loading();
  try {
    let view;
    const settingsCallbacks = {
      heading,
      toast,
      markDirty: (value) => typeof value === 'boolean' ? setSettingsDirty(value) : refreshSettingsDirty(),
      clearDirty: clearSettingsDirty,
      reload: route,
    };
    if (path === '/') view = await overviewPage({ heading, toast });
    else if (/^\/tasks\/[^/]+\/records\/[^/]+$/.test(path)) view = await scrapeRecordPage(path);
    else if (path === '/tasks' || path.startsWith('/tasks/')) view = await tasks(path);
    else if (path === '/persons') {
      location.replace('#/actors');
      return;
    } else if (path === '/actors') view = await actorsPage({ heading, toast, reload: route });
    else if (/^\/actors\/[^/]+$/.test(path)) view = await actorDetailPage(decodeURIComponent(path.slice(8)), { heading, toast, reload: route });
    else if (path === '/providers') view = await providers();
    else if (path.startsWith('/providers/')) view = await providerEdit(path.slice(11));
    else if (path === '/settings/watch-dir')
      view = await watchSettings(settingsCallbacks);
    else if (path === '/settings/download')
      view = await downloadSettings(settingsCallbacks);
    else if (path === '/settings/naming')
      view = await namingSettings(settingsCallbacks);
    else if (path === '/settings/watermark')
      view = await watermarkSettings(settingsCallbacks);
    else if (path === '/metadata') {
      location.replace('#/settings/metadata');
      return;
    } else if (path === '/settings/metadata')
      view = await metadataSettings(settingsCallbacks);
    else if (path === '/settings/nfo')
      view = await nfoSettings(settingsCallbacks);
    else if (path === '/logs') view = logsPage(heading);
    else if (path === '/settings/system')
      view = await systemSettings(settingsCallbacks);
    else if (path === '/settings/user')
      view = await userSettings(settingsCallbacks);
    else if (path === '/settings/emby')
      view = await embySettings(settingsCallbacks);
    else if (path === '/settings' || path.startsWith('/settings/')) view = await settings();
    else
      view = {
        html:
          heading('页面未找到', '这个页面暂时不存在。') +
          '<a class="button" href="#/">回到概览</a>',
      };
    if (token !== generation) return;
    main.innerHTML = view.html;
    groupSettingsSections();
    view.bind?.();
    // 加载数据期间完成的任务没有被页面收到，这里补一次整体刷新。
    flushMissedJobEvents();
    settingsBaseline = settingFormState();
    // 推送触发的静默刷新保留当前滚动位置；正常切换页面时才回到顶部。
    if (!options.silent) window.scrollTo(0, 0);
    checkStatus();
  } catch (error) {
    if (token !== generation) return;
    console.error('[route] failed to render', error);
    main.innerHTML =
      heading('暂时无法载入', '请检查本地服务是否正在运行。') +
      `<div class="error-panel">${h(error.message)}<p><button class="button" id="retry">重新加载</button></p></div>`;
    $('#retry').onclick = route;
    checkStatus();
  }
}

function groupSettingsSections() {
  for (const panel of main.querySelectorAll('.settings-layout > .panel')) {
    const firstTitle = panel.querySelector('.download-section-title');
    if (!firstTitle) continue;
    const content = firstTitle.parentElement;
    const grouped = document.createDocumentFragment();
    let section;
    let body;
    for (const item of [...content.children]) {
      if (item.classList.contains('savebar')) {
        grouped.append(item);
        section = body = null;
        continue;
      }
      if (item.classList.contains('download-section-title')) {
        section = document.createElement('section');
        section.className = 'settings-group';
        item.classList.add('settings-group-title');
        section.append(item);
        body = document.createElement('div');
        body.className = 'settings-group-body';
        section.append(body);
        grouped.append(section);
        continue;
      }
      if (!body) {
        section = document.createElement('section');
        section.className = 'settings-group';
        body = document.createElement('div');
        body.className = 'settings-group-body';
        section.append(body);
        grouped.append(section);
      }
      body.append(item);
    }
    content.replaceChildren(grouped);
    content.classList.add('settings-grouped-content');
    panel.classList.add('settings-grouped');
  }
}
// 只拉取当前记录库的任务，用于原地刷新表格。分组规则与 tasks() 保持一致。
// 返回 null 表示该记录库已不存在，需要整页重新渲染。
async function fetchSelectedGroupJobs(selectedKey) {
  const [data, common] = await Promise.all([
    api('/api/scrape/jobs?page=1&page_size=1000'),
    api('/api/config/common'),
  ]);
  const items = data.data || [];
  const directoryIDs = new Set((common.watch_dirs || []).map((directory) => directory.id));
  if (selectedKey === '__unmatched__') {
    const unmatched = items.filter((job) => !directoryIDs.has(job.directory_id));
    return unmatched.length ? unmatched : null;
  }
  if (!directoryIDs.has(selectedKey)) return null;
  return items.filter((job) => job.directory_id === selectedKey);
}

// 记录库顶部的统计数字，整页渲染和原地刷新共用。
function recordSummaryHTML(jobs) {
  const count = (status) => jobs.filter((job) => job.status === status).length;
  return `<span>全部 <b>${jobs.length}</b></span><span>等待 <b>${count(0)}</b></span><span>处理中 <b>${count(1)}</b></span><span>已完成 <b>${count(2)}</b></span><span>失败 <b>${count(-1)}</b></span>`;
}

async function tasks(path) {
  // 状态筛选从地址栏的 status 参数读取（如 status=2&status=-1&status=-2&status=0&status=1）。
  taskFilter = filterFromURL();
  return scrapeRecords(path);
}

async function scrapeRecords(path) {
  const [data, common] = await Promise.all([
    api('/api/scrape/jobs?page=1&page_size=1000'),
    api('/api/config/common'),
  ]);
  const items = data.data || [];
  const directories = (common.watch_dirs || []).map((directory, index) => ({
    ...directory,
    groupIndex: index,
    jobs: items.filter((job) => job.directory_id === directory.id),
  }));
  const directoryIDs = new Set(directories.map((directory) => directory.id));
  const unmatched = items.filter((job) => !directoryIDs.has(job.directory_id));
  const groups = unmatched.length
    ? [
        ...directories,
        {
          id: '',
          path: '其他 / 已删除的监控目录',
          enabled: false,
          groupIndex: directories.length,
          jobs: unmatched,
          unmatched: true,
        },
      ]
    : directories;
  const statusNames = { 0: '等待中', 1: '处理中', 2: '已完成', '-1': '失败' };
  const statusValues = { waiting: 0, processing: 1, finished: 2, failed: -1 };
  const countStatus = (jobs, status) => jobs.filter((job) => job.status === status).length;
  const groupKey = (group) => (group.unmatched ? '__unmatched__' : group.id);
  const libraryHTML = (group) =>
    `<a class="record-library" href="#/tasks/${encodeURIComponent(groupKey(group))}"><div class="record-library-head"><span class="record-library-icon">◫</span><span class="pill ${group.enabled ? '' : 'muted-pill'}">${group.unmatched ? '历史记录' : group.enabled ? '监控中' : '已停用'}</span></div><h2>${h(group.path || '未命名监控目录')}</h2><p>${group.unmatched ? '目录已从当前配置中删除' : `${group.jobs.length} 条刮削记录`}</p><div class="record-library-stats"><span><b>${group.jobs.length}</b>全部</span><span><b>${countStatus(group.jobs, 1)}</b>处理中</span><span><b>${countStatus(group.jobs, 2)}</b>已完成</span><span><b>${countStatus(group.jobs, -1)}</b>失败</span></div><div class="record-library-open">查看记录 <span>→</span></div></a>`;
  const selectedKey = path.startsWith('/tasks/')
    ? decodeURIComponent(path.slice('/tasks/'.length))
    : '';
  if (!selectedKey) {
    return {
      html:
        heading('刮削记录', '选择一个监控目录库，查看该目录的自动刮削记录。') +
        notice() +
        `<div class="record-overview"><div class="record-overview-head"><div><h2>监控目录库</h2><p>每个库独立统计和展示刮削记录。</p></div><div class="record-summary"><span>监控目录 <b>${directories.length}</b> 个</span><span>刮削记录 <b>${items.length}</b> 条</span></div></div>${
          groups.length
            ? `<div class="record-library-grid">${groups.map(libraryHTML).join('')}</div>`
            : `<section class="panel">${empty('还没有监控目录', '先添加监控目录，每个目录会成为一个独立记录库。', '◷', '<a class="button" href="#/settings/watch-dir">配置目录监控 →</a>')}</section>`
        }</div>`,
    };
  }
  const selected = groups.find((group) => groupKey(group) === selectedKey);
  if (!selected) {
    return {
      html:
        heading('记录库不存在', '该监控目录可能已被删除。') +
        `<section class="panel">${empty('无法找到这个记录库', '返回监控目录库后重新选择。', '◫', '<a class="button" href="#/tasks">返回记录库 →</a>')}</section>`,
    };
  }
  return {
    html:
      heading(
        '刮削记录',
        selected.path || '未命名监控目录',
        '<a class="button back-button" href="#/tasks"><span class="back-arrow">←</span>返回记录库</a>',
      ) +
      notice() +
      `<section class="panel record-toolbar"><div class="toolbar"><div class="tabs">${[
        ['all', '全部'],
        ['waiting', '等待中'],
        ['processing', '处理中'],
        ['finished', '已完成'],
        ['failed', '失败'],
        ['stopped', '已终止'],
      ]
        .map(
          ([value, name]) =>
            `<button data-filter="${value}" class="${taskFilter === value ? 'active' : ''}">${name}</button>`,
        )
        .join('')}</div><input class="search" id="task-search" aria-label="搜索刮削记录" placeholder="搜索名称、番号或文件路径…"><div class="record-view-switch" role="group" aria-label="显示方式"><button type="button" data-record-view="table">表格</button><button type="button" data-record-view="poster">海报</button></div></div><div class="record-summary" id="record-summary">${recordSummaryHTML(selected.jobs)}</div></section>${recordsTableHTML()}`,
    bind() {
      const table = mountRecordsTable({ jobs: selected.jobs, filter: taskFilter, setFilter: (value) => { taskFilter = value; }, empty, reload: route, toast });
      let timer = null;
      const refresh = async () => {
        timer = null;
        // 地址栏可能带有 ?page=…&status=… 等参数，比较时只看路径部分。
        if (location.hash.split('?')[0] !== '#' + path) return;
        // 用户正在选择记录、打开菜单或搜索时不刷新，避免丢失选择。
        // 这只检查页面上的状态，不会请求服务器；条件解除后再刷新。
        if (document.querySelector('#record-action-menu:popover-open, #record-dialog[open], [data-record-select]:checked') || document.querySelector('#task-search')?.value) {
          timer = setTimeout(refresh, 1000);
          return;
        }
        let jobs;
        try {
          jobs = await fetchSelectedGroupJobs(selectedKey);
        } catch {
          return;
        }
        if (location.hash.split('?')[0] !== '#' + path) return;
        if (!jobs) {
          // 记录库本身变化（例如目录被删除），这时才需要整页重新渲染。
          route({ silent: true });
          return;
        }
        // 只替换表格行和统计数字，页面结构、滚动位置和搜索框都保持不动。
        table.update(jobs);
        document.querySelector('#record-summary').innerHTML = recordSummaryHTML(jobs);
      };
      onJobEvents((kind, data) => {
        // 处理中记录的进度文字随事件推送，直接改那一格，不重新拉取列表。
        if (kind === 'jobs-changed' && data.progress) {
          // 海报卡片里进度同时出现在图片占位区和目录行，全部更新。
          document.querySelectorAll(`.record-progress[data-job-id="${CSS.escape(data.id)}"]`).forEach((cell) => { cell.textContent = data.progress; });
        }
        // 只有列表展示的内容变化（状态、番号、路径等）、任务结束或重连时才刷新；日志变化不重绘列表。
        const relevant = kind === 'resync' || kind === 'job-finished' || (kind === 'jobs-changed' && data.list);
        if (!relevant) return;
        // 处理中的记录每条日志都会推送一次，合并 200 毫秒内的事件后再刷新。
        clearTimeout(timer);
        timer = setTimeout(refresh, 200);
      });
    },
  };
}
async function providers() {
  const { data: entries } = await api('/api/providers');
  return {
    html:
      heading(
        '数据源',
        '以插件的方式，逐步扩展你的媒体工作空间。',
        `<div class="provider-heading-actions">${button('＋ 添加插件', 'install-plugin', true)}${button('↻ 重新加载插件', 'reload-plugins')}</div>`,
      ) +
      `<input id="plugin-zip" type="file" accept=".zip,application/zip" hidden><div class="notice"><span>◈</span><div>已安装 ${entries.length} 个插件。可上传 ZIP 插件包、下载已有插件；内置插件可下载，第三方插件可删除。</div></div><div class="toolbar" style="padding:0 0 20px;border:0"><input id="provider-search" class="search" placeholder="搜索数据源…" aria-label="搜索数据源"><span class="content-count">独立配置 · 连接测试 · 番号查询</span></div><div class="provider-grid" id="provider-grid"></div><div id="failure-notify"></div><div id="priority-editor"></div>`,
    bind() {
      const render = () => {
        const query = $('#provider-search').value.toLowerCase();
        $('#provider-grid').innerHTML =
          entries
            .filter((source) => source.name.toLowerCase().includes(query))
            .map(
              (source) =>
                `<article class="provider-card"><div class="provider-title"><span class="provider-initial">${h(source.name[0].toUpperCase())}</span><h3>${h(source.name)}</h3><span class="pill ${source.enabled ? '' : 'muted-pill'}">${source.enabled ? '已启用' : '未启用'}</span></div><div class="provider-url" title="${h(source.base_url)}">${h(source.base_url)} · v${h(source.version)} · ${source.builtin ? '内置插件' : '第三方插件'}</div><div class="provider-actions"><button class="button small" data-test-provider="${h(source.id)}">测试连接</button><a class="button small" href="#/providers/${encodeURIComponent(source.id)}">配置与查询 →</a><a class="button small" href="/api/providers/${encodeURIComponent(source.id)}/package" download="${h(source.id)}.zip">下载 ZIP</a>${source.builtin ? '' : `<button class="button small" data-delete-provider="${h(source.id)}">删除</button>`}</div><p class="pending" role="status" data-test-result="${h(source.id)}"></p></article>`,
            )
            .join('') || '<p class="pending">没有找到匹配的数据源。</p>';
        document.querySelectorAll('[data-test-provider]').forEach(
          (button) =>
            (button.onclick = async () => {
              button.disabled = true;
              try {
                const result = await api(
                  '/api/providers/' + encodeURIComponent(button.dataset.testProvider) + '/test',
                  {
                    method: 'POST',
                    timeout: 30000,
                  },
                );
                button.closest('article').querySelector('[role=status]').textContent =
                  result.message;
              } catch (error) {
                button.closest('article').querySelector('[role=status]').textContent =
                  error.message;
              } finally {
                button.disabled = false;
              }
            }),
        );
        document.querySelectorAll('[data-delete-provider]').forEach((button) => {
          button.onclick = async () => {
            if (dirty) {
              toast('请先保存或撤销优先级修改');
              return;
            }
            const id = button.dataset.deleteProvider;
            if (
              !window.confirm(`删除插件 ${id}？插件文件会被移除，已保存的配置和字段优先级会保留。`)
            )
              return;
            button.disabled = true;
            try {
              await api('/api/providers/' + encodeURIComponent(id), { method: 'DELETE' });
              await route();
              toast(`已删除插件 ${id}`);
            } catch (error) {
              button.disabled = false;
              toast(error.message, true);
            }
          };
        });
      };
      const fileInput = $('#plugin-zip');
      $('#install-plugin').onclick = () => {
        if (dirty) {
          toast('请先保存或撤销优先级修改');
          return;
        }
        fileInput.click();
      };
      fileInput.onchange = async () => {
        const file = fileInput.files?.[0];
        if (!file) return;
        const button = $('#install-plugin');
        button.disabled = true;
        try {
          if (file.size > 2 * 1024 * 1024) throw Error('插件 ZIP 不得超过 2 MB');
          const response = await fetch('/api/providers/install', {
            method: 'POST',
            headers: { 'Content-Type': 'application/zip' },
            body: file,
            signal: AbortSignal.timeout(30000),
          });
          const result = await response.json();
          if (!response.ok) throw Error(result.message || '插件安装失败');
          await route();
          toast(`已添加插件 ${result.name}`);
        } catch (error) {
          toast(error.message, true);
        } finally {
          button.disabled = false;
          fileInput.value = '';
        }
      };
      $('#provider-search').oninput = render;
      $('#reload-plugins').onclick = () => {
        if (dirty) {
          toast('请先保存或撤销优先级修改');
          return;
        }
        route();
      };
      render();
      mountPriorityEditor(
        $('#priority-editor'),
        entries,
        () => {
          dirty = true;
        },
        () => {
          dirty = false;
        },
      );
      mountFailureNotify($('#failure-notify'), entries, { toast }).catch((error) => toast(error.message, true));
    },
  };
}
async function providerEdit(name) {
  const endpoint = '/api/providers/' + encodeURIComponent(name);
  const [value, catalog] = await Promise.all([api(endpoint + '/config'), api('/api/providers')]);
  const descriptor = catalog.data.find((source) => source.id === name);
  const displayName = descriptor?.name || name;
  return {
    html:
      heading(
        displayName,
        '独立的数据源插件，按番号获取元数据。',
        '<a class="button back-button" href="#/providers"><span class="back-arrow">←</span>返回数据源</a>',
      ) +
      `<div class="notice"><span>ⓘ</span><div>请先启用并保存数据源，再测试连接或查询。遇到官网验证时，请在浏览器中处理，Go 程序不会自动共享浏览器 Cookies。</div></div>` +
      `<section class="panel"><div class="panel-head"><h2>番号查询</h2><button class="button small" id="test-source">测试连接</button></div><form class="path-bar" id="fetch-form"><input class="search" name="number" aria-label="查询番号" placeholder="输入番号，例如 ABP-123" required maxlength="64"><button class="button primary" id="fetch-source">获取元数据</button></form><div id="source-result" class="activity-note" role="status">只读取元数据，不下载图片、不移动文件。</div></section>` +
      formHTML(displayName + ' 配置', '修改配置后先保存，再进行连接测试或查询。', value),
    bind() {
      bindForm(value, endpoint + '/config');
      $('#fetch-form input').maxLength = 256;
      if (descriptor?.input_hint) $('#fetch-form input').placeholder = descriptor.input_hint;
      const showError = (error) => {
        $('#source-result').textContent = error.message;
        if (error.code === 'verification_required') {
          const link = document.createElement('a');
          link.textContent = ' 在浏览器中打开站点 ↗';
          link.href = $('#field-base_url').value || value.base_url;
          link.target = '_blank';
          link.rel = 'noopener noreferrer';
          $('#source-result').append(link);
        }
      };
      $('#test-source').onclick = async (event) => {
        if (dirty) {
          toast('请先保存配置');
          return;
        }
        event.target.disabled = true;
        $('#source-result').textContent = '正在连接数据源…';
        try {
          const result = await api(endpoint + '/test', { method: 'POST', timeout: 30000 });
          $('#source-result').textContent = result.message + `（${result.elapsed_ms} ms）`;
        } catch (error) {
          showError(error);
        } finally {
          event.target.disabled = false;
        }
      };
      $('#fetch-form').onsubmit = async (event) => {
        event.preventDefault();
        if (dirty) {
          toast('请先保存配置');
          return;
        }
        const button = $('#fetch-source');
        button.disabled = true;
        $('#source-result').textContent = '正在获取元数据…';
        try {
          const result = await api(endpoint + '/fetch', {
            method: 'POST',
            body: { number: new FormData(event.target).get('number') },
            timeout: 30000,
          });
          const rows = [
            ['番号', result.number],
            ['标题', result.title],
            ['原始标题', result.original_title],
            ['发行日期', result.release],
            ['时长', result.runtime],
            ['评分', result.rating],
            ['导演', result.director],
            ['制作商', result.studio],
            ['发行商', result.publisher],
            ['系列', result.series],
            // 男女演员分开列：male_actors 缺省时按空数组处理，其它插件不返回也不会出错。
            ['女演员', (result.actors || []).join('、')],
            ['男演员', (result.male_actors || []).join('、')],
            ['标签', (result.tags || []).join('、')],
            ['简介', result.outline],
            ['预告片', result.trailer],
          ];
          $('#source-result').innerHTML =
            `<div class="metadata-result">${rows.map(([key, value]) => `<div><span>${h(key)}</span><b>${h(value || '—')}</b></div>`).join('')}<div class="metadata-images"><h3>封面</h3>${imageGallery(result.cover, '封面', result.image_previews)}</div><div class="metadata-images"><h3>剧照</h3>${imageGallery(result.samples, '剧照', result.image_previews)}</div><a class="button small" href="${h(result.source_url)}" target="_blank" rel="noopener noreferrer">打开来源页面 ↗</a></div>`;
        } catch (error) {
          showError(error);
        } finally {
          button.disabled = false;
        }
      };
    },
  };
}
async function settings() {
  const value = await api('/api/config/common');
  const modes = [
    ['2', '移动'],
    ['3', '原地整理'],
    ['4', 'STRM'],
  ];
  const modeHelp = {
    2: '移动视频文件到整理目录；源文件会从原位置移走。',
    3: '保持视频在原位置，在原文件夹中生成整理结果；整理目录不参与此模式。',
    4: '在整理目录生成 .strm 文件指向原视频，视频保持原位置；字幕不处理。覆盖已有的 .strm 和配套文件。',
  };
  const tagEditor = (id, key, label) => {
    const items = Array.isArray(value[key]) ? value[key] : [];
    return `<div class="tag-editor" id="${id}"><div class="tag-items">${items.map((item) => `<span class="tag-chip" data-value="${h(item)}"><span>${h(item)}</span><button type="button" class="tag-remove" aria-label="移除 ${h(item)}">×</button></span>`).join('')}</div><button type="button" class="tag-add">＋ 添加</button><span class="tag-entry" hidden><input id="${id}-input" type="text" spellcheck="false" aria-label="添加${label}" placeholder="输入后按回车"><button type="button" class="tag-confirm" aria-label="确认添加">✓</button><button type="button" class="tag-cancel" aria-label="取消添加">×</button></span></div>`;
  };
  return {
    html:
      heading('设置', '先配置媒体文件的整理方式。') +
      `<div class="settings-layout"><nav class="settings-nav"><a class="active" href="#/settings/common">整理</a><a href="#/settings/watch-dir">监控</a><a href="#/settings/download">下载</a><a href="#/settings/naming">命名</a><a href="#/settings/watermark">水印</a><a href="#/settings/metadata">元数据</a><a href="#/settings/nfo">NFO</a><a href="#/settings/emby">头像</a><a href="#/settings/system">系统</a><a href="#/settings/user">用户</a></nav><form id="settings-form" class="panel">
        <div class="download-section-title"><h2>整理方式</h2></div>
        <div class="field"><label class="field-label" for="organize-folder">整理目录<small>移动和 STRM 模式下的目标目录，点击按钮浏览本机文件夹</small></label><div class="folder-control"><input id="organize-folder" type="text" value="${h(value.target_folder || '')}" placeholder="请选择目录" readonly><button class="button small" type="button" id="pick-organize-folder">选择目录…</button></div></div>
        <div class="field"><label class="field-label" for="organize-mode">整理模式<small id="organize-mode-help"></small></label><select id="organize-mode">${modes.map(([id, name]) => `<option value="${id}" ${Number(value.link_mode) === Number(id) ? 'selected' : ''}>${name}</option>`).join('')}</select></div>
        <div class="field mode-option" id="move-options"><label class="field-label" for="move-cleanup">移动后删除源文件夹<small>移动完成后，若原视频所在的源文件夹已没有任何文件，则删除该文件夹</small></label><input id="move-cleanup" type="checkbox" class="switch" ${value.delete_empty_parent_after_move ? 'checked' : ''}></div>
        <div class="field mode-option" id="cleanup-metadata-option"><label class="field-label" for="cleanup-metadata">刮削出错时删除元数据目录<small>用于移动模式；原地整理会自动清理本次未完成的输出文件</small></label><input id="cleanup-metadata" type="checkbox" class="switch" ${value.delete_metadata_folder_when_failed ? 'checked' : ''}></div>
        <div class="field mode-option" id="strm-options"><label class="field-label" for="strm-path-from">STRM 路径映射（可选）<small>把 mdc-go 看到的源路径前缀替换为播放器看到的前缀。两项都留空则不映射，strm 直接写本机路径。例：从 /data/av 到 /media/av。</small></label><div class="strm-map-row"><input id="strm-path-from" type="text" value="${h(value.strm_path_from || '')}" placeholder="mdc-go 中的前缀，如 /data/av" spellcheck="false"><span aria-hidden="true">→</span><input id="strm-path-to" type="text" value="${h(value.strm_path_to || '')}" placeholder="播放器中的前缀，如 /media/av" spellcheck="false"></div></div>
        <div class="field mode-option" id="overwrite-video-option"><label class="field-label" for="overwrite-video">覆盖目标目录视频和字幕<small>开启后，整理时会覆盖目标目录中已存在的视频和字幕文件</small></label><input id="overwrite-video" type="checkbox" class="switch" ${value.target_overwrite_video ? 'checked' : ''}></div>
        <div class="field mode-option" id="overwrite-pic-option"><label class="field-label" for="overwrite-pic">覆盖目标目录图片<small>开启后，整理时会覆盖目标目录中已存在的图片</small></label><input id="overwrite-pic" type="checkbox" class="switch" ${value.target_overwrite_pic ? 'checked' : ''}></div>
        <div class="download-section-title"><h2>文件筛选</h2></div>
        <div class="field"><label class="field-label" for="min-file-size">文件大小过滤（MB）<small>小于此大小的视频文件将被忽略；0 表示不过滤。将影响后续目录监控和文件搜索。</small></label><input id="min-file-size" type="number" min="0" step="any" value="${h(Number(value.file_size_filter || 0) / 1048576)}"></div>
        <div class="field"><label class="field-label" for="video-extensions-input">文件类型白名单<small>仅识别这些扩展名的视频文件；点添加后输入，不带点号。</small></label>${tagEditor('video-extensions', 'file_ext_filter', '视频扩展名')}</div>
        <div class="field"><label class="field-label" for="filename-blacklist-input">文件名黑名单<small>文件名包含任一条目时忽略该文件。</small></label>${tagEditor('filename-blacklist', 'file_name_blacklist', '文件名黑名单')}</div>
        <div class="download-section-title"><h2>中文字幕识别</h2><p>文件名包含任一关键词即视为有中文字幕。</p></div>
        <div class="field"><label class="field-label" for="subtitle-keywords-input">中文字幕关键词<small>不区分大小写。纯英文字母或数字的关键词（如 C、CHS）须在文件名中单独成段，例如 ABP-123-C。</small></label>${tagEditor('subtitle-keywords', 'subtitle_keywords', '中文字幕关键词')}</div>
        <div class="download-section-title"><h2>影片类型识别</h2><p>按 LTX、LADA、JAV、其他破解、无码的顺序识别，命中一项即停止；都没命中时为有码。</p></div>
        <div class="field"><label class="field-label" for="ltx-keywords-input">LTX 破解关键词<small>匹配文件名（不含番号部分），不区分大小写。</small></label>${tagEditor('ltx-keywords', 'ltx_hack_keywords', 'LTX 破解关键词')}</div>
        <div class="field"><label class="field-label" for="lada-keywords-input">LADA 破解关键词<small>匹配文件名（不含番号部分），不区分大小写。</small></label>${tagEditor('lada-keywords', 'lada_hack_keywords', 'LADA 破解关键词')}</div>
        <div class="field"><label class="field-label" for="jav-keywords-input">JAV 破解关键词<small>匹配文件名（不含番号部分），不区分大小写。</small></label>${tagEditor('jav-keywords', 'jav_hack_keywords', 'JAV 破解关键词')}</div>
        <div class="field"><label class="field-label" for="other-hack-keywords-input">其他破解关键词<small>不属于 LTX、LADA、JAV 的破解版本；匹配文件名，不区分大小写。</small></label>${tagEditor('other-hack-keywords', 'other_hack_keywords', '其他破解关键词')}</div>
        <div class="field"><label class="field-label" for="uncensored-keywords-input">无码关键词<small>匹配文件名，不区分大小写。</small></label>${tagEditor('uncensored-keywords', 'uncensored_keywords', '无码关键词')}</div>
        <div class="savebar"><span id="save-status">所有修改均保存在本地</span><div><button class="button" type="button" id="reset-form">撤销修改</button> <button class="button primary" type="submit" id="save-form">保存设置</button></div></div></form></div>
        <dialog id="folder-dialog" class="folder-dialog"><div class="folder-dialog-head"><h2>选择整理目录</h2><button class="icon-button" id="folder-close" type="button" aria-label="关闭">×</button></div><div class="folder-location"><button class="button small" id="folder-up" type="button">↑ 上一级</button><input id="folder-path" type="text" aria-label="目录路径" placeholder="完整目录路径"><button class="button small" id="folder-go" type="button">转到</button></div><div id="folder-list" class="folder-list" role="list"></div><p id="folder-status" class="folder-status" role="status"></p><div class="folder-dialog-actions"><button class="button" id="folder-cancel" type="button">取消</button><button class="button primary" id="folder-select" type="button" disabled>选择此目录</button></div></dialog>`,
    bind() {
      const form = $('#settings-form');
      const mark = () => {
        refreshSettingsDirty();
        $('#save-status').textContent = '有尚未保存的修改';
        $('#save-status').classList.add('unsaved');
      };
      const showModeHelp = () => {
        const mode = $('#organize-mode').value;
        $('#organize-mode-help').textContent = modeHelp[mode];
        $('#move-options').hidden = mode !== '2';
        $('#cleanup-metadata-option').hidden = mode !== '2';
        $('#strm-options').hidden = mode !== '4';
        // 覆盖选项用于移动和 STRM 模式；原地整理不移动也不写目标目录，所以隐藏。
        $('#overwrite-video-option').hidden = mode === '3';
        $('#overwrite-pic-option').hidden = mode === '3';
      };
      showModeHelp();
      form.oninput = (event) => {
        if (!event.target.closest('.tag-entry')) mark();
      };
      form.onchange = (event) => {
        if (!event.target.closest('.tag-entry')) mark();
      };
      $('#organize-mode').addEventListener('change', showModeHelp);
      const tagEditors = [...form.querySelectorAll('.tag-editor')];
      const readTags = (id) =>
        [...$('#' + id).querySelectorAll('.tag-chip')].map((chip) => chip.dataset.value);
      const cancelTagEntry = (editor) => {
        editor.querySelector('.tag-entry input').value = '';
        editor.querySelector('.tag-entry').hidden = true;
        editor.querySelector('.tag-add').hidden = false;
      };
      const addPendingTag = (editor) => {
        const input = editor.querySelector('.tag-entry input');
        const entries = input.value
          .split(/[,，]/)
          .map((item) => {
            const trimmed = item.trim();
            return editor.id === 'video-extensions'
              ? trimmed.replace(/^\./, '').toLowerCase()
              : trimmed;
          })
          .filter(Boolean);
        if (editor.id === 'video-extensions' && entries.some((item) => !/^[a-z0-9]+$/.test(item))) {
          throw Error('文件类型白名单须为有效的扩展名');
        }
        const existing = new Set(readTags(editor.id).map((item) => item.toLowerCase()));
        for (const item of entries) {
          if (existing.has(item.toLowerCase())) continue;
          existing.add(item.toLowerCase());
          const chip = document.createElement('span');
          chip.className = 'tag-chip';
          chip.dataset.value = item;
          const name = document.createElement('span');
          name.textContent = item;
          const remove = document.createElement('button');
          remove.type = 'button';
          remove.className = 'tag-remove';
          remove.setAttribute('aria-label', '移除 ' + item);
          remove.textContent = '×';
          chip.append(name, remove);
          editor.querySelector('.tag-items').append(chip);
          mark();
        }
        cancelTagEntry(editor);
      };
      for (const editor of tagEditors) {
        const add = editor.querySelector('.tag-add');
        const input = editor.querySelector('.tag-entry input');
        add.onclick = () => {
          add.hidden = true;
          editor.querySelector('.tag-entry').hidden = false;
          input.focus();
        };
        editor.querySelector('.tag-confirm').onclick = () => {
          try {
            addPendingTag(editor);
          } catch (error) {
            toast(error.message, true);
          }
        };
        editor.querySelector('.tag-cancel').onclick = () => cancelTagEntry(editor);
        input.onkeydown = (event) => {
          if (event.key === 'Enter') {
            event.preventDefault();
            try {
              addPendingTag(editor);
            } catch (error) {
              toast(error.message, true);
            }
          }
          if (event.key === 'Escape') cancelTagEntry(editor);
        };
        editor.querySelector('.tag-items').onclick = (event) => {
          const remove = event.target.closest('.tag-remove');
          if (!remove) return;
          remove.closest('.tag-chip').remove();
          mark();
        };
      }
      const dialog = $('#folder-dialog');
      let folderState = { path: '', parent: '' };
      const browse = async (path = '') => {
        $('#folder-status').textContent = '正在读取文件夹…';
        $('#folder-list').innerHTML = '';
        $('#folder-select').disabled = true;
        try {
          folderState = await api('/api/system/folders?path=' + encodeURIComponent(path));
          $('#folder-path').value = folderState.path;
          $('#folder-up').disabled = !folderState.path;
          $('#folder-select').disabled = !folderState.path;
          $('#folder-list').innerHTML = folderState.folders.length
            ? folderState.folders
                .map(
                  (item) =>
                    `<button type="button" class="folder-item" data-folder-path="${h(item.path)}"><span aria-hidden="true">▰</span>${h(item.name)}</button>`,
                )
                .join('')
            : '<div class="folder-empty">没有子文件夹</div>';
          $('#folder-status').textContent = folderState.truncated
            ? '文件夹较多，仅显示前 2000 个；可在上方输入完整路径。'
            : folderState.path || '请选择起始目录';
          $('#folder-list')
            .querySelectorAll('[data-folder-path]')
            .forEach((button) => (button.onclick = () => browse(button.dataset.folderPath)));
        } catch (error) {
          $('#folder-status').textContent = error.message;
        }
      };
      $('#pick-organize-folder').onclick = () => {
        dialog.showModal();
        browse($('#organize-folder').value || '');
      };
      $('#folder-up').onclick = () => browse(folderState.parent || '');
      $('#folder-go').onclick = () => browse($('#folder-path').value.trim());
      $('#folder-path').onkeydown = (event) => {
        if (event.key === 'Enter') {
          event.preventDefault();
          browse(event.target.value.trim());
        }
      };
      $('#folder-select').onclick = () => {
        $('#organize-folder').value = folderState.path;
        mark();
        dialog.close();
      };
      $('#folder-cancel').onclick = () => dialog.close();
      $('#folder-close').onclick = () => dialog.close();
      $('#reset-form').onclick = () => {
        setSettingsDirty(false);
        route();
        toast('已撤销未保存的修改');
      };
      form.onsubmit = async (event) => {
        event.preventDefault();
        const button = $('#save-form');
        button.disabled = true;
        try {
          // 整理目录可以留空；开启目录监控时由后端检查是否可用。
          const folder = $('#organize-folder').value.trim();
          if (folder && !isAbsolutePath(folder))
            throw Error(absolutePathHint('整理目录'));
          const sizeMB = Number($('#min-file-size').value);
          const sizeBytes = Math.round(sizeMB * 1048576);
          if (!Number.isFinite(sizeMB) || sizeMB < 0 || !Number.isSafeInteger(sizeBytes))
            throw Error('文件大小过滤须为有效的非负数');
          for (const editor of tagEditors) {
            if (!editor.querySelector('.tag-entry').hidden) addPendingTag(editor);
          }
          await api('/api/config/common', {
            method: 'PUT',
            body: {
              target_folder: $('#organize-folder').value,
              delete_metadata_folder_when_failed: $('#cleanup-metadata').checked,
              link_mode: Number($('#organize-mode').value),
              delete_empty_parent_after_move: $('#move-cleanup').checked,
              target_overwrite_video: $('#overwrite-video').checked,
              target_overwrite_pic: $('#overwrite-pic').checked,
              strm_path_from: $('#strm-path-from').value.trim(),
              strm_path_to: $('#strm-path-to').value.trim(),
              file_size_filter: sizeBytes,
              file_ext_filter: readTags('video-extensions'),
              file_name_blacklist: readTags('filename-blacklist'),
              subtitle_keywords: readTags('subtitle-keywords'),
              jav_hack_keywords: readTags('jav-keywords'),
              lada_hack_keywords: readTags('lada-keywords'),
              ltx_hack_keywords: readTags('ltx-keywords'),
              other_hack_keywords: readTags('other-hack-keywords'),
              uncensored_keywords: readTags('uncensored-keywords'),
            },
          });
          clearSettingsDirty();
          $('#save-status').textContent = '设置已保存';
          $('#save-status').classList.remove('unsaved');
          toast('整理设置已保存');
        } catch (error) {
          toast(error.message, true);
        } finally {
          button.disabled = false;
        }
      };
    },
  };
}
function formHTML(title, desc, value) {
  return `<form id="settings-form" class="panel">${fields(value)}<div class="savebar"><span id="save-status">所有修改均保存在本地</span><div><button class="button" type="button" id="reset-form">撤销修改</button> <button class="button primary" type="submit" id="save-form">保存设置</button></div></div></form>`;
}
function fields(object, prefix = []) {
  return Object.entries(object)
    .map(([key, value]) => {
      const path = [...prefix, key];
      if (key === 'options' && value !== null && typeof value === 'object' && !Array.isArray(value))
        return `<div class="field"><label class="field-label" for="field-options">插件扩展配置<small>JSON 对象，由插件脚本自行读取</small></label><textarea id="field-options" data-path='${h(JSON.stringify(path))}' data-type="json-object" spellcheck="false">${h(JSON.stringify(value, null, 2))}</textarea></div>`;
      if (value !== null && !Array.isArray(value) && typeof value === 'object')
        return `<div class="field-group">${h(label(key))}</div>${fields(value, path)}`;
      const id = 'field-' + path.join('-'),
        attributes = `id="${h(id)}" data-path="${h(JSON.stringify(path))}"`;
      let input;
      if (typeof value === 'boolean')
        input = `<input ${attributes} type="checkbox" class="switch" ${value ? 'checked' : ''}>`;
      else if (Array.isArray(value)) {
        const complex =
          key === 'watch_dirs' ||
          value.some((item) => typeof item === 'object');
        input = `<textarea ${attributes} data-type="${complex ? 'json' : 'string-list'}" spellcheck="false">${h(complex ? JSON.stringify(value, null, 2) : value.join('\n'))}</textarea>`;
      } else if (enums[key] || key.startsWith('crop_type_')) {
        const options = enums[key] || ['None', 'Face', 'Right', 'Center'];
        const available = options.map((o) => (Array.isArray(o) ? o : [o, o]));
        if (!available.some(([v]) => String(v) === String(value)))
          available.unshift([value, value]);
        input = `<select ${attributes} data-type="${typeof value === 'number' ? 'number' : 'string'}">${available.map(([v, n]) => `<option value="${h(v)}" ${String(v) === String(value) ? 'selected' : ''}>${h(n)}</option>`).join('')}</select>`;
      } else {
        const secret = /(?:api_key|openai_key|cookies|password)$/.test(key);
        input = `<input ${attributes} type="${typeof value === 'number' ? 'number' : secret ? 'password' : 'text'}" ${typeof value === 'number' ? 'min="0" step="any"' : ''} ${secret ? 'autocomplete="new-password"' : ''} data-type="${value === null ? 'nullable' : typeof value}" value="${h(value ?? '')}" ${value === null ? 'placeholder="未设置（可留空）"' : ''}>`;
      }
      const hint = Array.isArray(value)
        ? key === 'watch_dirs'
          ? '高级配置，使用 JSON 数组编辑'
          : '每行填写一项，可留空'
        : fieldHelp[key];
      return `<div class="field"><label class="field-label" for="${h(id)}">${h(label(key))}${hint ? `<small>${h(hint)}</small>` : ''}</label>${input}</div>`;
    })
    .join('');
}
function bindForm(original, endpoint, transform = (v) => v) {
  let saved = structuredClone(original);
  const form = $('#settings-form');
  const mark = () => {
    dirty = true;
    $('#save-status').textContent = '有尚未保存的修改';
    $('#save-status').classList.add('unsaved');
  };
  form.oninput = mark;
  form.onchange = mark;
  $('#reset-form').onclick = () => {
    if (dirty) {
      dirty = false;
      route();
      toast('已撤销未保存的修改');
    }
  };
  form.onsubmit = async (event) => {
    event.preventDefault();
    const save = $('#save-form');
    save.disabled = true;
    try {
      const draft = structuredClone(saved);
      form.querySelectorAll('[data-path]').forEach((input) => {
        const path = JSON.parse(input.dataset.path);
        let object = draft;
        for (const key of path.slice(0, -1)) object = object[key];
        let value = input.type === 'checkbox' ? input.checked : input.value;
        if (input.dataset.type === 'json') {
          try {
            value = JSON.parse(value);
          } catch {
            throw Error(`${label(path.at(-1))}：请输入合法的 JSON 数组`);
          }
          if (!Array.isArray(value)) throw Error(`${label(path.at(-1))}：必须填写数组`);
        } else if (input.dataset.type === 'json-object') {
          try {
            value = JSON.parse(value);
          } catch {
            throw Error('插件扩展配置：请输入合法 JSON 对象');
          }
          if (value === null || Array.isArray(value) || typeof value !== 'object')
            throw Error('插件扩展配置：必须填写 JSON 对象');
        } else if (input.dataset.type === 'string-list') {
          value = value
            .split(/\r?\n/)
            .map((item) => item.trim())
            .filter(Boolean);
        } else if (input.dataset.type === 'number') {
          if (value.trim() === '' || !Number.isFinite(Number(value)))
            throw Error(`${label(path.at(-1))}：请输入有效数字`);
          if (Number(value) < 0) throw Error(`${label(path.at(-1))}：不能小于 0`);
          value = Number(value);
        } else if (input.dataset.type === 'nullable') value = value === '' ? null : value;
        object[path.at(-1)] = value;
      });
      await api(endpoint, { method: 'PUT', body: transform(draft) });
      saved = draft;
      dirty = false;
      $('#save-status').textContent = '设置已保存';
      $('#save-status').classList.remove('unsaved');
      toast('设置已保存到本地');
    } catch (error) {
      toast(error.message, true);
    } finally {
      save.disabled = false;
    }
  };
}
function guardedNavigate(hash) {
  if (!dirty) {
    location.hash = hash;
    return;
  }
  pendingNavigation = hash;
  $('#confirm-dialog').showModal();
}
document.addEventListener('click', (event) => {
  const link = event.target.closest('a[href^="#/"]');
  if (link && dirty && link.hash !== currentHash) {
    event.preventDefault();
    guardedNavigate(link.hash);
  }
});
$('#confirm-dialog').addEventListener('close', () => {
  if ($('#confirm-dialog').returnValue === 'leave' && pendingNavigation) {
    dirty = false;
    location.hash = pendingNavigation;
  }
  pendingNavigation = null;
});
window.addEventListener('hashchange', () => {
  if (dirty) {
    const next = location.hash;
    history.replaceState(null, '', currentHash);
    guardedNavigate(next);
    return;
  }
  route();
});
window.addEventListener('beforeunload', (event) => {
  if (dirty) {
    event.preventDefault();
    event.returnValue = '';
  }
});
$('#refresh').onclick = () => {
  if (dirty) {
    toast('请先保存或撤销当前修改');
    return;
  }
  route();
};
$('#menu').onclick = () => $('#sidebar').classList.toggle('open');
api('/api/config/user')
  .then(applyUserProfile)
  .catch(() => {});
// 先读取运行平台（路径写法因系统而异），再渲染第一个页面。
loadPlatform().then(() => {
  $('#platform-name').textContent = `${platformName()} 本地版`;
  $('#app-version').textContent = platformVersion() ? `v${platformVersion()}` : '';
  route();
});

import { api, escapeHTML as h } from './api.js';
import { onJobEvents } from './job-events.js';

// 首页概览：指标、任务队列、最近完成、近 7 天趋势、需要关注、监控目录和数据源。
// 推送事件到来后只重新拉取数据并替换有变化的区块，海报墙等内容不变时不会重绘。

const phases = [
  ['identify', '识别', '#c9d5f5'],
  ['source', '数据源', '#8fa8ec'],
  ['image', '图片', '#5f84e6'],
  ['write', '写入', '#3d63d6'],
  ['organize', '整理', '#2a4bb0'],
];
const phaseNames = Object.fromEntries(phases.map(([key, name]) => [key, name]));
const modeNames = { 2: '移动', 3: '原地整理', 4: 'STRM' };
const coverColors = [
  ['#8aa4e8', '#3b4f8f'], ['#e7a48a', '#8f4a3b'], ['#93cfb5', '#2f6d55'], ['#c3a6e6', '#5a3d86'],
  ['#e6cf8f', '#8a6d24'], ['#9fc3d9', '#2f5a73'], ['#b9c3d6', '#4b5870'], ['#e0b0c4', '#7d3c58'],
];
const QUEUE_ROWS = 5;
const POSTERS = 24;
const ATTENTION_ROWS = 5;
const SOURCE_SAMPLE = 50;

// Go 输出的时间可能有 7 位小数，浏览器只稳定支持 3 位，先截断。
const parseTime = (value) => (value ? Date.parse(String(value).replace(/(\.\d{3})\d+/, '$1')) : NaN);
const dayKey = (time) => {
  const date = new Date(time);
  return `${date.getFullYear()}-${date.getMonth() + 1}-${date.getDate()}`;
};
const pad = (n) => String(n).padStart(2, '0');
const clock = (value) => {
  const time = parseTime(value);
  if (Number.isNaN(time)) return '—';
  const date = new Date(time);
  return `${pad(date.getHours())}:${pad(date.getMinutes())}`;
};
const mmss = (seconds) => {
  const value = Math.max(0, Math.floor(seconds));
  return value >= 3600 ? `${Math.floor(value / 3600)}:${pad(Math.floor((value % 3600) / 60))}:${pad(value % 60)}` : `${pad(Math.floor(value / 60))}:${pad(value % 60)}`;
};
function ago(value) {
  const time = parseTime(value);
  if (Number.isNaN(time)) return '';
  const seconds = Math.max(0, (Date.now() - time) / 1000);
  if (seconds < 60) return '刚刚';
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`;
  if (seconds < 86400 * 7) return `${Math.floor(seconds / 86400)} 天前`;
  const date = new Date(time);
  return `${date.getMonth() + 1} 月 ${date.getDate()} 日`;
}
const elapsedSeconds = (job) => {
  const started = parseTime(job.started_at);
  return Number.isNaN(started) ? (job.duration_ms || 0) / 1000 : (Date.now() - started) / 1000;
};

async function load() {
  const [jobs, watch, common, system, providers] = await Promise.all([
    api('/api/scrape/jobs'),
    api('/api/watcher/status'),
    api('/api/config/common'),
    api('/api/config/system'),
    api('/api/providers'),
  ]);
  return { jobs: jobs.data || [], watch, common, system, providers: providers.data || [] };
}

// 记录详情的地址：目录已从配置中删除的记录归入「其他」记录库。
function recordURL(job, directoryIDs) {
  const key = directoryIDs.has(job.directory_id) ? job.directory_id : '__unmatched__';
  return `#/tasks/${encodeURIComponent(key)}/records/${encodeURIComponent(job.id)}`;
}

function summarize(data) {
  const { jobs } = data;
  const now = new Date();
  const days = [];
  for (let offset = 6; offset >= 0; offset -= 1) {
    const date = new Date(now.getFullYear(), now.getMonth(), now.getDate() - offset);
    days.push({ key: dayKey(date), date, done: 0, failed: 0 });
  }
  const byDay = new Map(days.map((day) => [day.key, day]));
  for (const job of jobs) {
    if (job.status !== 2 && job.status !== -1) continue;
    const time = parseTime(job.completed_at);
    if (Number.isNaN(time)) continue;
    const day = byDay.get(dayKey(time));
    if (!day) continue;
    if (job.status === 2) day.done += 1;
    else day.failed += 1;
  }
  const running = jobs.filter((job) => job.status === 1);
  const waiting = jobs.filter((job) => job.status === 0);
  const failed = jobs.filter((job) => job.status === -1);
  const partial = jobs.filter((job) => job.status === 2 && job.notify_failed_sources?.length);
  const finished = jobs.filter((job) => job.status === 2);
  const byCompletion = (a, b) => (parseTime(b.completed_at) || 0) - (parseTime(a.completed_at) || 0);
  return {
    days,
    today: days[6],
    yesterday: days[5],
    running,
    waiting,
    failed,
    partial,
    finished,
    recent: [...finished].sort(byCompletion),
    directoryIDs: new Set((data.common.watch_dirs || []).map((dir) => dir.id)),
    threads: Number(data.system.scrape_thread_count) || 1,
    timeout: Number(data.system.scrape_timeout) > 0 ? Number(data.system.scrape_timeout) : 180,
  };
}

function sparkHTML(values, failed = false) {
  const max = Math.max(1, ...values);
  return `<div class="ov-spark${failed ? ' ov-spark-fail' : ''}" aria-hidden="true">${values.map((value) => `<i style="height:${Math.max(4, Math.round((value / max) * 100))}%"></i>`).join('')}</div>`;
}

function statsHTML(sum) {
  const diff = sum.today.done - sum.yesterday.done;
  const diffTag = diff === 0 ? '<span class="ov-tag ov-mute">与昨日持平</span>' : `<span class="ov-tag ${diff > 0 ? 'ov-up' : 'ov-mute'}">较昨日 ${diff > 0 ? '+' : ''}${diff}</span>`;
  const card = (label, value, spark, note, tag, extra = '') =>
    `<div class="ov-stat${extra}"><div><div class="ov-stat-label">${label}</div><div class="ov-stat-number">${h(value)}</div></div>${spark}<div class="ov-stat-note"><span>${note}</span>${tag}</div></div>`;
  const busy = sum.running.length >= sum.threads;
  return [
    card('今日完成', sum.today.done, sparkHTML(sum.days.map((day) => day.done)), `累计 ${h(sum.finished.length.toLocaleString())} 条成功记录`, diffTag),
    card('处理中', sum.running.length, '<div></div>', `已用 ${h(Math.min(sum.running.length, sum.threads))} / ${h(sum.threads)} 个线程`, sum.running.length ? `<span class="ov-tag ov-info">${busy ? '线程已满' : '运行中'}</span>` : '<span class="ov-tag ov-mute">空闲</span>'),
    card('排队等待', sum.waiting.length, '<div></div>', sum.waiting.length ? '等待空闲线程' : '没有排队的文件', sum.waiting.length ? '<span class="ov-tag ov-warn">排队</span>' : ''),
    card('失败待处理', sum.failed.length, sparkHTML(sum.days.map((day) => day.failed), true), sum.partial.length ? `另有 ${h(sum.partial.length)} 条部分数据源失败` : '失败的记录可以直接重试', sum.failed.length ? '<a class="ov-tag ov-bad" href="#ov-attention">去处理 →</a>' : '', ' ov-stat-fail'),
  ].join('');
}

function queueHTML(sum) {
  const { running, waiting, threads, timeout } = sum;
  const head = `<div class="panel-head"><h2>任务队列<small>处理中 ${h(running.length)} / ${h(threads)} 线程 · 排队 ${h(waiting.length)}</small></h2><a href="#/tasks">刮削记录 ↗</a></div>`;
  if (!running.length && !waiting.length) return `${head}<div class="ov-queue-idle">当前没有刮削任务</div>`;
  const counts = Object.fromEntries(phases.map(([key]) => [key, 0]));
  for (const job of running) counts[job.phase in counts ? job.phase : 'identify'] += 1;
  const dist = running.length
    ? `<div class="ov-dist"><div class="ov-dist-bar">${phases.filter(([key]) => counts[key]).map(([key, name, color]) => `<i style="flex:${counts[key]};background:${color}" title="${h(name)} ${counts[key]}"></i>`).join('')}</div><div class="ov-dist-legend">${phases.map(([key, name, color]) => `<span class="${counts[key] ? '' : 'ov-zero'}"><i style="background:${color}"></i>${h(name)} <b>${counts[key]}</b></span>`).join('')}</div></div>`
    : '';
  const slowest = [...running].sort((a, b) => elapsedSeconds(b) - elapsedSeconds(a)).slice(0, QUEUE_ROWS);
  const rows = slowest.map((job) => {
    const seconds = elapsedSeconds(job);
    const ratio = Math.min(1, seconds / timeout);
    const stage = [phaseNames[job.phase] || '识别', job.stop_requested ? '正在终止…' : job.progress].filter(Boolean).join(' · ');
    return `<div class="ov-row${ratio >= 0.5 ? ' ov-slow' : ''}" data-ov-id="${h(job.id)}" data-ov-start="${h(job.started_at || '')}" data-ov-timeout="${timeout}"><a href="${h(recordURL(job, sum.directoryIDs))}" title="${h(job.path)}">${h(job.number || '未识别番号')}</a><span class="ov-row-stage" title="${h(stage)}">${h(stage)}</span><div class="ov-mini"><i style="width:${(ratio * 100).toFixed(1)}%"></i></div><span class="ov-row-time"><b>${mmss(seconds)}</b><small>/ ${h(timeout)} 秒</small></span></div>`;
  }).join('');
  const table = running.length ? `<div class="ov-rows-head"><span>${running.length > QUEUE_ROWS ? `耗时最长的 ${QUEUE_ROWS} 个` : '处理中'}</span><span>当前阶段</span><span></span><span>已运行</span></div>${rows}` : '<div class="ov-queue-idle">所有线程空闲，排队的文件即将开始处理</div>';
  const restRunning = Math.max(0, running.length - QUEUE_ROWS);
  const rest = [restRunning ? `另外 ${restRunning} 个处理中` : '', waiting.length ? `${waiting.length} 个排队` : ''].filter(Boolean).join('，');
  return `${head}${dist}${table}${rest ? `<div class="ov-foot"><span>${h(rest)}</span><a href="#/tasks">查看全部 →</a></div>` : ''}`;
}

function postersHTML(sum) {
  const head = '<div class="panel-head"><h2>最近完成</h2><a href="#/tasks">刮削记录 ↗</a></div>';
  if (!sum.recent.length) return `${head}<div class="ov-empty">还没有完成的刮削记录。监控目录发现新视频后，整理完成的影片会显示在这里。</div>`;
  const cards = sum.recent.slice(0, POSTERS).map((job, index) => {
    const [from, to] = coverColors[index % coverColors.length];
    const actors = job.actors || [];
    const people = actors.length > (job.actor_limit || 3) ? '多人作品' : actors.join('、') || '未知演员';
    const warn = job.notify_failed_sources?.length ? `<span class="ov-warnmark" title="${h(`失败的数据源：${job.notify_failed_sources.join('、')}`)}">!</span>` : '';
    const image = job.poster ? `<img src="${h(job.poster)}" alt="${h(job.number || '')} 海报" loading="lazy" decoding="async" data-ov-poster>` : '';
    return `<a class="ov-poster" href="${h(recordURL(job, sum.directoryIDs))}" title="${h(job.output || job.path)}"><div class="ov-cover" style="background:linear-gradient(170deg,${from},${to})">${image}${warn}<span class="ov-cover-num">${h(job.number || '未识别')}</span></div><p title="${h(actors.join('、'))}">${h(people)}</p><small>${h(ago(job.completed_at))}</small></a>`;
  }).join('');
  return `${head}<div class="ov-posters">${cards}</div>`;
}

function chartHTML(sum) {
  const done = sum.days.reduce((total, day) => total + day.done, 0);
  const failed = sum.days.reduce((total, day) => total + day.failed, 0);
  const max = Math.max(1, ...sum.days.map((day) => day.done + day.failed));
  const weekday = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];
  const cols = sum.days.map((day, index) => {
    const total = day.done + day.failed;
    const today = index === sum.days.length - 1;
    const stack = total
      ? `<div class="ov-stack" style="height:${Math.max(3, Math.round((total / max) * 100))}%" title="完成 ${day.done} · 失败 ${day.failed}">${day.done ? `<i style="flex:${day.done}" class="ov-c-done${today ? ' ov-today' : ''}"></i>` : ''}${day.failed ? `<i style="flex:${day.failed}" class="ov-c-fail${today ? ' ov-today' : ''}"></i>` : ''}</div>`
      : '<div class="ov-stack ov-stack-empty"></div>';
    return `<div class="ov-col"><span class="ov-col-num">${total || ''}</span>${stack}<small class="${today ? 'ov-today-label' : ''}">${today ? '今天' : weekday[day.date.getDay()]}</small></div>`;
  }).join('');
  return `<div class="panel-head"><h2>近 7 天<small>完成 ${done} · 失败 ${failed}</small></h2></div><div class="ov-chart">${cols}</div><div class="ov-legend"><span><i class="ov-c-done"></i>完成</span><span><i class="ov-c-fail"></i>失败</span></div>`;
}

function attentionHTML(sum) {
  // 失败的记录排在前面，部分数据源失败的记录随后；各自按完成时间从新到旧。
  const newest = (a, b) => (parseTime(b.completed_at) || 0) - (parseTime(a.completed_at) || 0);
  const items = [
    ...[...sum.failed].sort(newest).map((job) => ({ job, kind: 'fail' })),
    ...[...sum.partial].sort(newest).map((job) => ({ job, kind: 'warn' })),
  ];
  const total = items.length;
  const head = `<div class="panel-head"><h2>需要关注</h2><a href="#/tasks">${total > ATTENTION_ROWS ? `全部 ${total} 条` : '刮削记录'} ↗</a></div>`;
  if (!total) return `${head}<div class="ov-empty ov-ok">没有失败的记录，一切正常。</div>`;
  const rows = items.slice(0, ATTENTION_ROWS).map(({ job, kind }) => {
    const reason = kind === 'fail'
      ? job.error || '处理失败'
      : `${job.notify_failed_sources.join('、')} 抓取失败，已用其他数据源完成`;
    const action = kind === 'fail'
      ? `<button type="button" class="button small" data-ov-retry="${h(job.id)}">重试</button>`
      : `<a class="button small" href="${h(recordURL(job, sum.directoryIDs))}">查看</a>`;
    return `<div class="ov-alert"><span class="ov-ic ov-${kind === 'fail' ? 'bad' : 'warn'}">!</span><div><a href="${h(recordURL(job, sum.directoryIDs))}"><b>${h(job.number || '未识别番号')}</b></a><small title="${h(reason)}">${h(reason)}</small></div>${action}</div>`;
  }).join('');
  return head + rows;
}

function watchHTML(data, sum) {
  const { watch, common } = data;
  const dirs = common.watch_dirs || [];
  const head = '<div class="panel-head"><h2>监控目录</h2><a href="#/settings/watch-dir">设置 ↗</a></div>';
  let line;
  if (!watch.enabled) line = '<div class="ov-watch-line"><span><span class="ov-dot ov-dot-off"></span>监控未开启</span><a href="#/settings/watch-dir">去开启</a></div>';
  else if (watch.running) line = '<div class="ov-watch-line"><span><span class="ov-dot ov-dot-run"></span>正在扫描…</span></div>';
  else line = `<div class="ov-watch-line"><span><span class="ov-dot"></span>监控中${watch.last_scan ? ` · 上次 ${clock(watch.last_scan)}` : ''}${watch.next_scan ? ` · 下次 ${clock(watch.next_scan)}` : ''}</span><button type="button" class="ov-link" data-ov-scan>立即扫描</button></div>`;
  if (!dirs.length) return `${head}${line}<div class="ov-empty">还没有监控目录。<a href="#/settings/watch-dir">添加监控目录 →</a></div>`;
  const statuses = new Map((watch.directories || []).map((item) => [item.id, item]));
  const rows = dirs.map((dir) => {
    const status = statuses.get(dir.id);
    const mode = modeNames[(dir.use_global_config ? common : dir.common || common).link_mode ?? common.link_mode] || '';
    const jobs = sum.jobsByDir.get(dir.id) || { ok: 0, fail: 0, other: 0 };
    const total = jobs.ok + jobs.fail + jobs.other;
    let tag;
    let meta;
    if (!dir.enabled) {
      tag = '<span class="ov-tag ov-mute">已停用</span>';
      meta = `<span>${h(mode)}</span>`;
    } else if (status?.error) {
      tag = '<span class="ov-tag ov-bad">异常</span>';
      meta = `<span class="ov-err" title="${h(status.error)}">${h(status.error)}</span>`;
    } else if (!watch.enabled) {
      tag = '<span class="ov-tag ov-mute">未监控</span>';
      meta = `<span>${h(mode)}</span>`;
    } else if (!status) {
      tag = '<span class="ov-tag ov-mute">等待扫描</span>';
      meta = `<span>${h(mode)}</span>`;
    } else {
      tag = '<span class="ov-tag ov-up">正常</span>';
      meta = `<span>匹配 <strong>${h(status.matched)}</strong></span><span>本次新增 <strong>${h(status.new_files)}</strong></span><span>${h(mode)}</span>${status.truncated ? '<span class="ov-warn-text">文件过多，未扫描完</span>' : ''}`;
    }
    const bar = total
      ? `<div class="ov-bar" title="成功 ${jobs.ok} · 失败 ${jobs.fail} · 其他 ${jobs.other}"><i style="flex:${jobs.ok};background:#a9bdf1"></i><i style="flex:${jobs.fail};background:var(--ov-red)"></i><i style="flex:${jobs.other};background:#e3e7ee"></i></div>`
      : '';
    return `<a class="ov-dir" href="#/tasks/${encodeURIComponent(dir.id)}"><div class="ov-dir-head"><b title="${h(dir.path)}">${h(dir.path || '未命名监控目录')}</b>${tag}</div><div class="ov-dir-meta">${meta}</div>${bar}</a>`;
  }).join('');
  return head + line + rows;
}

function sourcesHTML(data, sum) {
  const enabled = data.providers.filter((item) => item.enabled);
  const head = `<div class="panel-head"><h2>数据源<small>近 ${SOURCE_SAMPLE} 条记录的成功率</small></h2><a href="#/providers">管理 ↗</a></div>`;
  if (!enabled.length) return `${head}<div class="ov-empty">还没有启用数据源。<a href="#/providers">去启用 →</a></div>`;
  // 成功的记录一定经过了数据源抓取；失败的记录只统计有数据源失败信息的（排除番号识别失败等未抓取的情况）。
  const sample = [...sum.finished, ...sum.failed.filter((job) => job.source_errors && Object.keys(job.source_errors).length)]
    .sort((a, b) => (parseTime(b.completed_at) || 0) - (parseTime(a.completed_at) || 0))
    .slice(0, SOURCE_SAMPLE);
  const rows = enabled.map((item) => {
    if (!sample.length) return `<div class="ov-src"><div><b>${h(item.name)}</b><small>已启用</small></div><div class="ov-meter"></div><span>—</span></div>`;
    const failures = sample.filter((job) => job.source_errors?.[item.id]).length;
    const rate = Math.round(((sample.length - failures) / sample.length) * 100);
    const color = rate >= 90 ? '#5fae8e' : rate >= 60 ? 'var(--ov-orange)' : 'var(--ov-red)';
    return `<a class="ov-src" href="#/providers/${encodeURIComponent(item.id)}"><div><b>${h(item.name)}</b><small>${failures ? `失败 ${failures} 次` : '已启用'}</small></div><div class="ov-meter"><i style="width:${rate}%;background:${color}"></i></div><span>${rate}%</span></a>`;
  }).join('');
  return head + rows;
}

function subtitleText(sum) {
  const parts = [`今天已整理 ${sum.today.done} 部影片`];
  if (sum.failed.length) parts.push(`另有 ${sum.failed.length} 条需要处理`);
  return parts.join('，') + '。';
}

function sections(data) {
  const sum = summarize(data);
  sum.jobsByDir = new Map();
  for (const job of data.jobs) {
    const entry = sum.jobsByDir.get(job.directory_id) || { ok: 0, fail: 0, other: 0 };
    if (job.status === 2) entry.ok += 1;
    else if (job.status === -1) entry.fail += 1;
    else entry.other += 1;
    sum.jobsByDir.set(job.directory_id, entry);
  }
  return {
    subtitle: h(subtitleText(sum)),
    stats: statsHTML(sum),
    queue: queueHTML(sum),
    posters: postersHTML(sum),
    chart: chartHTML(sum),
    attention: attentionHTML(sum),
    watch: watchHTML(data, sum),
    sources: sourcesHTML(data, sum),
  };
}

// 海报加载失败时只保留渐变底色和番号。
function bindPosterFallback(root) {
  root.querySelectorAll('img[data-ov-poster]').forEach((image) => {
    image.onerror = () => image.remove();
  });
}

// 已运行时间和超时进度每秒更新一次，不必等推送。
function tickQueue() {
  document.querySelectorAll('.ov-row[data-ov-start]:not([data-ov-start=""])').forEach((row) => {
    const started = parseTime(row.dataset.ovStart);
    if (Number.isNaN(started)) return;
    const seconds = (Date.now() - started) / 1000;
    const ratio = Math.min(1, seconds / Number(row.dataset.ovTimeout || 180));
    row.querySelector('.ov-row-time b').textContent = mmss(seconds);
    row.querySelector('.ov-mini i').style.width = `${(ratio * 100).toFixed(1)}%`;
    row.classList.toggle('ov-slow', ratio >= 0.5);
  });
}

export async function overviewPage({ heading, toast }) {
  let data = await load();
  const first = sections(data);
  const date = new Intl.DateTimeFormat('zh-CN', { month: 'long', day: 'numeric', weekday: 'long' }).format(new Date());
  // 版本号读取失败时不显示，不影响概览其它内容。
  const version = await api('/api/version').then((value) => value?.version || '').catch(() => '');
  const versionTag = version ? `<span class="ov-version" title="MDC Go 版本">v${h(version)}</span>` : '';
  const html = '<div class="ov-page">'
    + heading('媒体工作台', '', `<span class="ov-heading-meta"><span class="content-count">${h(date)}</span>${versionTag}</span>`)
    + `<div class="ov-stats" data-ov="stats">${first.stats}</div>`
    + `<div class="dashboard-grid ov-grid"><div>`
    + `<section class="panel" data-ov="queue">${first.queue}</section>`
    + `<section class="panel" data-ov="posters">${first.posters}</section>`
    + `<section class="panel" data-ov="chart">${first.chart}</section>`
    + `</div><div>`
    + `<section class="panel" id="ov-attention" data-ov="attention">${first.attention}</section>`
    + `<section class="panel" data-ov="watch">${first.watch}</section>`
    + `<section class="panel" data-ov="sources">${first.sources}</section>`
    + `</div></div></div>`;
  return {
    html,
    bind() {
      // 事件都挂在本页的容器上，离开页面后随容器一起移除。
      const root = document.querySelector('#main .ov-page');
      const subtitle = root.querySelector('.page-heading .subtitle');
      subtitle.innerHTML = first.subtitle;
      const rendered = { ...first };
      bindPosterFallback(root);
      // 只替换内容有变化的区块；任务队列每秒自行刷新时间，内容比较时忽略它。
      const apply = (next) => {
        if (next.subtitle !== rendered.subtitle) subtitle.innerHTML = next.subtitle;
        for (const key of Object.keys(next)) {
          if (key === 'subtitle' || next[key] === rendered[key]) continue;
          const host = root.querySelector(`[data-ov="${key}"]`);
          if (!host) continue;
          host.innerHTML = next[key];
          if (key === 'posters') bindPosterFallback(host);
        }
        Object.assign(rendered, next);
      };
      let timer = null;
      let refreshing = false;
      const refresh = async () => {
        timer = null;
        if (!document.contains(subtitle)) return;
        if (refreshing) {
          timer = setTimeout(refresh, 500);
          return;
        }
        refreshing = true;
        try {
          data = await load();
          if (document.contains(subtitle)) apply(sections(data));
        } catch {
          // 暂时连不上服务时保留当前内容，下次推送再试。
        } finally {
          refreshing = false;
        }
      };
      const schedule = (delay) => {
        if (timer) return;
        timer = setTimeout(refresh, delay);
      };
      onJobEvents((kind, event) => {
        if (kind === 'jobs-changed' && !event.list) {
          // 只是日志或阶段变化：直接改任务队列里那一行，阶段分布等到下一次整体刷新。
          const row = [...root.querySelectorAll('.ov-row[data-ov-id]')].find((item) => item.dataset.ovId === event.id);
          if (row && event.progress) {
            const stage = [phaseNames[event.phase] || '识别', event.progress].join(' · ');
            const cell = row.querySelector('.ov-row-stage');
            cell.textContent = stage;
            cell.title = stage;
          }
          schedule(3000);
          return;
        }
        // 状态变化、任务结束、监控扫描或重连：合并 300 毫秒内的事件后刷新。
        schedule(300);
      });
      const ticker = setInterval(() => {
        if (!document.contains(subtitle)) {
          clearInterval(ticker);
          return;
        }
        tickQueue();
      }, 1000);
      root.addEventListener('click', async (event) => {
        const retry = event.target.closest('[data-ov-retry]');
        if (retry) {
          retry.disabled = true;
          try {
            await api(`/api/scrape/jobs/${encodeURIComponent(retry.dataset.ovRetry)}/retry`, { method: 'POST' });
            toast('已加入处理队列');
            schedule(0);
          } catch (error) {
            toast(error.message, true);
            retry.disabled = false;
          }
          return;
        }
        const scan = event.target.closest('[data-ov-scan]');
        if (scan) {
          scan.disabled = true;
          try {
            await api('/api/watcher/scan', { method: 'POST' });
            toast('已开始扫描监控目录');
          } catch (error) {
            toast(error.message, true);
          } finally {
            scan.disabled = false;
          }
          return;
        }
        const anchor = event.target.closest('a[href="#ov-attention"]');
        if (anchor) {
          event.preventDefault();
          root.querySelector('#ov-attention')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
        }
      });
    },
  };
}

import { api, escapeHTML as h } from './api.js';
import { imageGallery } from './gallery.js';
import { recordActionButtons, bindRecordActions } from './record-actions.js';
import { providerStyle } from './provider-color.js';
import { durationHTML } from './duration.js';

// 详细数据标题下方列出本次抓取失败的数据源，例如「avbase 失败」；悬停显示失败原因。
function failureNotes(job) {
  const entries = Object.entries(job.source_errors || {});
  if (!entries.length) return '';
  return `<div class="scrape-source-failures">${entries.map(([id, reason]) => `<span class="scrape-source-failed" title="${h(reason)}">${h(id)} 失败</span>`).join('')}</div>`;
}
import { onJobEvents } from './job-events.js';
import { sortRecords, sortFromQuery } from './records.js';
import { bindArrowPaging } from './pager-keys.js';

const statuses = {0: '等待中', 1: '处理中', 2: '成功', '-1': '失败', '-2': '已终止'};
// 上一条、下一条按列表当前顺序计算；筛选条件来自地址栏的 filter 和 q。
let neighborNav = '';
// 「返回列表」保留列表的页码、每页条数、视图和排序。
let backQuery = '';
async function loadNeighbors(job, back) {
  const query = new URLSearchParams(location.hash.split('?')[1] || '');
  const statuses = query.getAll('status').map(Number).filter(Number.isFinite);
  const search = (query.get('q') || '').trim().toLowerCase();
  try {
    // 上一条、下一条沿用列表的排序（地址栏的 sort 参数）。
    const rows = sortRecords((await api('/api/scrape/jobs')).data.filter((row) =>
      row.directory_id === job.directory_id &&
      (!statuses.length || statuses.includes(row.status)) &&
      JSON.stringify(row).toLowerCase().includes(search),
    ), sortFromQuery(query));
    const index = rows.findIndex((row) => row.id === job.id);
    const suffix = query.toString() ? `?${query.toString()}` : '';
    const link = (row, label, direction) => `<a class="button small" data-neighbor="${direction}" href="#${h(back)}/records/${encodeURIComponent(row.id)}${h(suffix)}">${label}</a>`;
    const previous = index > 0 ? rows[index - 1] : null;
    const next = index >= 0 && index < rows.length - 1 ? rows[index + 1] : null;
    neighborNav = index < 0 ? '' : `<span class="record-position">${index + 1} / ${rows.length}</span>${previous ? link(previous, '← 上一条', 'prev') : '<span class="button small" aria-disabled="true">← 上一条</span>'}${next ? link(next, '下一条 →', 'next') : '<span class="button small" aria-disabled="true">下一条 →</span>'}`;
  } catch {
    neighborNav = '';
  }
}
const linkModes = {2:'移动',3:'原地整理',4:'STRM'};
const fields = [['number','番号'],['publish_number','发行码'],['title','标题'],['original_title','原标题'],['actors','演员'],['outline','简介'],['tags','标签'],['cover','封面'],['poster','海报'],['release','发行日期'],['runtime','时长（分钟）'],['director','导演'],['studio','制作'],['publisher','发行商'],['series','系列'],['rating','评分']];
const value = (v) => Array.isArray(v) ? v.join('、') || '—' : v || '—';
const date = (v) => v ? new Date(v).toLocaleString('zh-CN', {hour12:false}) : '—';
const mosaicNames = {is_ltx_hack:'LTX 破解',is_lada_hack:'LADA 破解',is_jav_hack:'JAV 破解',is_other_hack:'其他破解',is_uncensored:'无码',is_censored:'有码（无破解标记）'};

function detailValue(input) {
  const text = String(value(input));
  if (!/^https?:\/\//i.test(text)) return h(text);
  return `<a class="scrape-field-link" href="${h(text)}" target="_blank" rel="noopener noreferrer" title="打开地址：${h(text)}">${h(text)}<span aria-hidden="true">↗</span></a>`;
}

function providerTag(provider, sources) {
  const id = String(provider || '').trim();
  if (!id) return '<span class="scrape-provider"></span>';
  const sourceURL = String(sources?.[id]?.source_url || '').trim();
  const style = providerStyle(id);
  if (!/^https?:\/\//i.test(sourceURL)) return `<span class="scrape-provider" style="${style}">${h(id)}</span>`;
  return `<a class="scrape-provider scrape-provider-link" style="${style}" href="${h(sourceURL)}" target="_blank" rel="noopener noreferrer" title="打开 ${h(id)} 来源网站">${h(id)}<span aria-hidden="true">↗</span></a>`;
}

function content(job, back, nfo) {
  const meta = job.metadata || {}, provenance = job.provenance || {}, sources = job.sources || {}, previews = meta.image_previews || {};
  const poster = meta.local_images?.poster || meta.poster || meta.cover;
  const cover = meta.local_images?.cover || meta.cover;
  const options = job.download_options || {}, local = meta.local_images || {};
  const gallery = [];
  if (options.thumb) gallery.push(imageGallery(cover, '缩略图', previews));
  if (options.fanart) gallery.push(imageGallery(local.fanart || meta.cover, '背景图', previews));
  if (options.extra_fanarts) {
    const samples = Object.keys(local).filter(key => /^sample_\d+$/.test(key))
      .sort((a,b) => Number(a.slice(7)) - Number(b.slice(7))).map(key => local[key]);
    gallery.push(imageGallery(samples.length ? samples : meta.samples, '剧照', previews));
  }
  if (options.trailer) {
    const trailer = local.trailer || (/^https?:\/\//i.test(meta.trailer || '') ? meta.trailer : '');
    gallery.push(trailer ? `<figure class="record-trailer-card"><video class="record-trailer" controls preload="none" src="${h(trailer)}"></video><figcaption>预告片</figcaption></figure>` : '<span class="image-empty">暂无预告片</span>');
  }
  const groups = [];
  for (const event of job.logs || []) {
    if (groups.at(-1)?.stage !== event.stage) groups.push({stage:event.stage, events:[]});
    groups.at(-1).events.push(event);
  }
  const tags = (meta.tags || []).map(tag => `<span class="record-tag">${h(tag)}</span>`).join('');
  const info = [
    ['原文件',job.path], ['整理后',job.output], ['字幕',job.has_subtitle ? `有字幕（文件名：${job.file_info?.subtitle_marker || '字幕标记'}）` : '无字幕标记（文件名）'],
    ['影片类型',mosaicNames[job.file_info?.mosaic || meta.mosaic] || '—'],
    ['类型标记',job.file_info?.mosaic_marker || '—'],
    ['整理模式',linkModes[job.link_mode] || job.link_mode],
    ['创建时间',date(job.created_at)], ['开始时间',date(job.started_at)], ['完成时间',date(job.completed_at)],
    ['用时',''],
  ];
  return `<div class="record-nav-row"><a class="button back-button" href="#${h(back)}${h(backQuery)}"><span class="back-arrow">←</span>返回列表</a><div class="record-neighbors">${neighborNav}</div></div>
  <section class="scrape-hero">
    <div class="scrape-poster">${poster ? imageGallery([poster], '海报', previews) : '<div class="scrape-poster-empty">暂无海报</div>'}</div>
    <div class="scrape-overview"><h1>${h(meta.number || job.number || '未识别番号')} ${h(meta.title || '')}</h1>
    <div class="record-status-actions"><span class="record-status record-status-${h(job.status)}">${h(statuses[job.status] || '未知状态')}</span>${recordActionButtons(job)}</div>
    <p class="scrape-byline">${[meta.rating ? '★ '+meta.rating : '',value(nfo?.exists && nfo.fields ? listOf(nfo.fields.actors) : meta.actors || job.actors),meta.release,meta.runtime ? meta.runtime+' 分钟' : ''].filter(Boolean).map(h).join(' · ')}</p>
    <div class="scrape-tags">${tags}</div>
    <p class="muted">导演：${h(value(meta.director))}　制作：${h(value(meta.studio))}　系列：${h(value(meta.series))}</p>
    ${job.error ? `<div class="scrape-error" role="alert">${h(job.error)}</div>` : ''}
    ${(job.warnings || []).length ? `<div class="notice">${job.warnings.map(h).join('<br>')}</div>` : ''}
    ${!job.metadata ? '<p class="muted">暂无已获取的影片元数据。</p>' : ''}</div>
  </section>
  ${gallery.length ? `<section class="scrape-gallery"><h2>画廊</h2><div class="record-gallery-media">${gallery.join('')}</div></section>` : ''}
  <div class="record-layers">
  <section class="record-layer"><div class="record-layer-head"><h2>详细数据</h2><span class="muted">数据源合并结果（转换前）</span>${failureNotes(job)}</div>
  <div class="record-layer-split"><dl class="scrape-fields">${fields.map(([key,label]) => { const raw = meta[key] || (key === 'number' ? job.number : ''); const shown = key === 'cover' || key === 'poster' ? h(value(raw)) : detailValue(raw); return `<div><dt>${h(label)}</dt><dd>${shown}</dd>${providerTag(provenance[key], sources)}</div>`; }).join('')}</dl>
  <div><h3 class="record-layer-sub">记录信息</h3><dl class="scrape-fields scrape-job-fields">${info.map(([label,v])=>label==='用时'?`<div><dt>${h(label)}</dt><dd id="record-duration">${durationHTML(job)}</dd></div>`:`<div><dt>${h(label)}</dt><dd>${h(value(v))}</dd></div>`).join('')}</dl></div></div></section>
  ${nfoLayer(job, nfo)}
  <section class="record-layer"><div class="record-layer-head"><h2>刮削日志</h2></div><ol class="scrape-timeline record-log-columns">${groups.map(group=>`<li><h3>${h(group.stage)}</h3><ul>${group.events.map(event=>`<li><time>${h(new Date(event.time).toLocaleTimeString('zh-CN',{hour12:false}))}</time><span>${h(event.message)}</span></li>`).join('')}</ul></li>`).join('')}</ol>${!groups.length ? '<p class="muted">该记录尚无执行日志。</p>' : ''}</section></div>`;
}

// ---- NFO 写入数据 ----
const listOf = (v) => (Array.isArray(v) ? v : [v]).map(x => String(x ?? '').trim()).filter(Boolean);
const same = (a, b) => listOf(a).join('\u0001') === listOf(b).join('\u0001');

// [标签, NFO 取值, 刮削取值, 映射字段]。刮削取值为 undefined：由 NFO 模板生成，标「生成」；
// 为 null：由其它字段直接派生（年份、图片等），不标记。
function nfoRows(n, meta, job) {
  const actors = listOf(meta.actors), withMale = [...actors, ...listOf(meta.male_actors)];
  return [
    ['标题', n.title, meta.title],
    ['原标题', n.originaltitle, meta.original_title],
    ['排序标题', n.sorttitle, null],
    ['副标题', n.tagline],
    ['番号', n.num, meta.number || job.number],
    ['发行码', n.publishnumber, meta.publish_number],
    ['演员', n.actors, same(n.actors, withMale) ? withMale : actors, 'actors'],
    ['导演', n.director, meta.director],
    ['制作', n.studio, meta.studio],
    ['发行商', n.publisher, meta.publisher],
    ['系列', n.series, meta.series],
    ['合集', n.sets, null],
    ['简介', n.plot || n.outline, meta.outline],
    ['发行日期', n.premiered || n.releasedate || n.release, meta.release],
    ['年份', n.year, null],
    ['时长', n.runtime, meta.runtime],
    ['评分', n.rating, meta.rating],
    ['图片', [n.poster, n.thumb, n.fanart].filter(Boolean).join(' · '), null],
    ['预告片', n.trailer, null],
    ['网站', n.website, meta.source_url],
  ];
}

function nfoValue(label, written, field, mappings) {
  const items = listOf(written);
  if (label === '网站' && /^https?:\/\//i.test(items[0] || '')) return detailValue(items[0]);
  return items.map(item => {
    const hit = mappings.find(m => m.field === field && m.to === item);
    return hit ? `<span class="nfo-mapped" title="${h(`映射：${hit.from} → ${hit.to}`)}">${h(item)}</span>` : h(item);
  }).join('、');
}

function nfoRow([label, written, scraped, field], mappings) {
  const generated = scraped === undefined, derived = scraped === null;
  if (!listOf(written).length) {
    const reason = generated || derived ? '' : listOf(scraped).length ? '（NFO 设置未开启）' : '（无数据）';
    return `<div><dt>${h(label)}</dt><dd><span class="nfo-off">未写入${reason}</span></dd></div>`;
  }
  let badge = '';
  if (field && mappings.some(m => m.field === field)) badge = `<span class="nfo-badge" title="${h('映射：' + mappings.filter(m => m.field === field).map(m => `${m.from} → ${m.to}`).join('；'))}">已转换</span>`;
  else if (generated) badge = '<span class="nfo-badge nfo-badge-new" title="由 NFO 规则生成，没有对应的刮削字段">生成</span>';
  else if (!derived && !same(written, scraped)) badge = `<span class="nfo-badge" title="${h('原值：' + (listOf(scraped).join('、') || '（空）'))}">已转换</span>`;
  return `<div><dt>${h(label)}</dt><dd>${nfoValue(label, written, field, mappings)}</dd>${badge}</div>`;
}

const tagOrigins = {
  serial_name: '番号前缀', actor: '演员', resolution: '按影片实际分辨率生成', mosaic: '马赛克类型',
  hack_type: '破解类型', subtitle: '字幕', series: '系列格式', studio: '片商格式', publisher: '发行格式', custom: '自定义规则',
};

// 优先使用生成 NFO 时记录的标签来源；旧记录或文件被外部修改时才退回按文字比对。
function tagEntries(n, meta, origins) {
  const written = listOf(n.tags);
  const recorded = origins?.tags || [];
  if (recorded.length === written.length && recorded.every((entry, i) => entry.tag === written[i])) {
    return {entries: recorded, dropped: origins.dropped || [], recorded: true};
  }
  const scraped = listOf(meta.tags);
  return {
    entries: written.map(tag => ({tag, origin: scraped.includes(tag) ? 'scraped' : ''})),
    dropped: scraped.filter(tag => !written.includes(tag)).map(tag => ({tag, origin: ''})),
    recorded: false, stale: recorded.length > 0,
  };
}

function nfoTags(n, meta, origins) {
  const written = listOf(n.tags), genres = listOf(n.genres);
  const {entries, dropped, recorded, stale} = tagEntries(n, meta, origins);
  const generated = entries.filter(entry => entry.origin !== 'scraped');
  const chips = entries.map(({tag, origin}) => {
    if (origin === 'scraped') return `<span class="record-tag" title="数据源刮削标签">${h(tag)}</span>`;
    const why = tagOrigins[origin] || 'NFO 附加标签';
    return `<span class="record-tag nfo-tag-added${origin === 'resolution' ? ' nfo-tag-resolution' : ''}" title="${h(why)}">${h(tag)}</span>`;
  }).join('');
  const notes = [];
  if (generated.length) notes.push(recorded ? '蓝色为 NFO 附加标签，悬停可看生成规则；绿色为按影片实际分辨率生成' : '蓝色为刮削数据中没有的标签');
  if (dropped.length) notes.push('未写入的刮削标签：' + dropped.map(d => h(d.tag) + (d.origin === 'resolution' ? '（分辨率以影片实际为准）' : '')).join('、'));
  if (!recorded) notes.push(stale ? 'NFO 文件已在程序外被修改，来源按文字比对推断' : '该记录生成时未保存标签来源，按文字比对推断；重试或重新整理后显示准确来源');
  const badge = generated.length || dropped.length ? '<span class="nfo-badge" title="与刮削标签不同">已转换</span>' : '';
  let html = `<div class="nfo-wide"><dt>标签</dt><dd>${written.length ? `<div class="scrape-tags nfo-tags">${chips}</div>` : '<span class="nfo-off">未写入</span>'}${notes.length ? `<small class="muted">${notes.join('；')}</small>` : ''}</dd>${badge}</div>`;
  if (!same(genres, written)) html += `<div class="nfo-wide"><dt>类型</dt><dd>${genres.length ? `<div class="scrape-tags nfo-tags">${genres.map(g => `<span class="record-tag">${h(g)}</span>`).join('')}</div>` : '<span class="nfo-off">未写入</span>'}</dd></div>`;
  else if (written.length) html += '<div class="nfo-wide"><dt>类型</dt><dd class="muted">与标签相同</dd></div>';
  return html;
}

function nfoLayer(job, nfo) {
  const head = (status, cls) => `<div class="record-layer-head"><h2>NFO 写入数据</h2>${nfo?.name ? `<span class="nfo-file">${h(nfo.name)}</span>` : ''}<span class="record-status ${cls}">${h(status)}</span></div>`;
  if (!nfo) return `<section class="record-layer">${head(job.status === 2 ? '读取失败' : '尚未整理完成', job.status === 2 ? 'record-status--1' : 'nfo-status-muted')}<p class="muted">${job.status === 2 ? '无法读取 NFO 信息。' : '整理成功后显示实际写入的 NFO 内容。'}</p></section>`;
  if (!nfo.exists) {
    const text = job.status !== 2 ? '尚未整理完成' : !nfo.enabled ? '未开启 NFO' : '文件缺失';
    const hint = job.status !== 2 ? '整理成功后显示实际写入的 NFO 内容。' : !nfo.enabled ? '该记录执行时 NFO 写入处于关闭状态。' : '输出目录中找不到该 NFO 文件，可能已被移动或删除。';
    return `<section class="record-layer">${head(text, job.status === 2 && nfo.enabled ? 'record-status--1' : 'nfo-status-muted')}<p class="muted">${h(hint)}</p></section>`;
  }
  if (!nfo.fields) return `<section class="record-layer">${head('读取失败', 'record-status--1')}<div class="scrape-error">${h(nfo.error || 'NFO 解析失败')}</div>${nfo.raw ? `<details class="nfo-raw"><summary>查看原始 XML</summary><pre>${h(nfo.raw)}</pre></details>` : ''}</section>`;
  const meta = job.metadata || {}, mappings = nfo.mappings || [];
  return `<section class="record-layer">${head('已写入', 'record-status-2')}
  <p class="muted nfo-hint">磁盘上 NFO 文件的实际内容。「已转换」表示与刮削数据不同，鼠标悬停可看原值；「生成」表示由 NFO 规则产生的字段。</p>
  <dl class="scrape-fields nfo-grid">${nfoRows(nfo.fields, meta, job).filter(([label, written]) => label !== '预告片' || listOf(written).length).map(row => nfoRow(row, mappings)).join('')}</dl>
  <dl class="scrape-fields nfo-tag-fields">${nfoTags(nfo.fields, meta, nfo.tag_origins)}</dl>
  <details class="nfo-raw"><summary>查看原始 XML</summary><pre>${h(nfo.raw)}</pre></details></section>`;
}

const loadNFO = (job) => job.status === 2 ? api('/api/scrape/jobs/' + encodeURIComponent(job.id) + '/nfo').catch(() => null) : Promise.resolve(null);

export async function scrapeRecordPage(path) {
  const split = path.lastIndexOf('/records/'), back = path.slice(0,split), id = decodeURIComponent(path.slice(split+9));
  const listQuery = new URLSearchParams(location.hash.split('?')[1] || '');
  const backParams = new URLSearchParams();
  for (const key of ['page', 'page_size', 'view', 'sort']) if (listQuery.has(key)) backParams.set(key, listQuery.get(key));
  for (const status of listQuery.getAll('status')) backParams.append('status', status);
  backQuery = backParams.toString() ? `?${backParams}` : '';
  let job, nfo;
  try { job = await api('/api/scrape/jobs/'+encodeURIComponent(id)); nfo = await loadNFO(job); await loadNeighbors(job, back); }
  catch (error) { return {html:`<a class="button back-button" href="#${h(back)}"><span class="back-arrow">←</span>返回列表</a><section class="empty"><h2>无法打开记录</h2><p>${h(error.message)}</p></section>`}; }
  return {
    html:`<article class="scrape-detail-page" id="scrape-detail-page">${content(job,back,nfo)}</article>`,
    bind() {
      const root = document.querySelector('#scrape-detail-page');
      let timer;
      const reload = async () => {
        clearTimeout(timer);
        timer = null;
        const next = await api('/api/scrape/jobs/'+encodeURIComponent(id));
        const nextNFO = await loadNFO(next);
        await loadNeighbors(next, back);
        if (!root?.isConnected) return;
        job = next; nfo = nextNFO;
        root.innerHTML = content(job,back,nfo);
      };
      bindRecordActions(root,()=>job,reload,back);
      // 方向键切换上一条、下一条。注册在 root 上：内容重绘时 root 不变，按钮每次按键时重新查找。
      bindArrowPaging(root, () => root.querySelector('.record-neighbors a[data-neighbor="prev"]'), () => root.querySelector('.record-neighbors a[data-neighbor="next"]'));
      const refresh = async () => {
        // 定时器已经执行或被清除，重置标记，之后的推送才能再次安排刷新。
        timer = null;
        if (!root?.isConnected) return;
        // 图片预览或编辑框打开时先不刷新，保持阅读位置；本地检查后再重试，不请求服务器。
        if (document.querySelector('.image-viewer[open], .record-editor[open]')) { timer=setTimeout(refresh,1000); return; }
        try {
          const next = await api('/api/scrape/jobs/'+encodeURIComponent(id));
          if (!root.isConnected) return;
          // 内容没有变化时保持页面不动。
          const changed = JSON.stringify({...next,duration_ms:0}) !== JSON.stringify({...job,duration_ms:0});
          if (changed) {
            nfo = await loadNFO(next);
            if (!root.isConnected) return;
            root.innerHTML = content(next,back,nfo);
          } else if (next.duration_ms !== job.duration_ms) {
            // 只有用时变化时只更新这一格，避免整页重绘。
            const cell = root.querySelector('#record-duration');
            if (cell) cell.innerHTML = durationHTML(next);
          }
          job = next;
        } catch (error) {
          if (root.isConnected) { const status=document.createElement('p'); status.className='scrape-error'; status.textContent=error.message; root.prepend(status); }
          return;
        }
      };
      // 只有这条记录变化，或连接重连后，才重新拉取。
      onJobEvents((kind, data) => {
        if (!root?.isConnected) return;
        const mine = kind === 'resync' || ((kind === 'jobs-changed' || kind === 'job-finished') && data.id === job.id);
        if (!mine || timer) return;
        // 处理中的记录每条日志都会推送一次。最多每秒重绘一次，避免页面不停闪动。
        timer = setTimeout(() => { timer = null; refresh(); }, 1000);
      });
    },
  };
}

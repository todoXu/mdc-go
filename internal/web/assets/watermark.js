import { api, escapeHTML as h } from './api.js';

const corners = [[5, '自动'], [0, '左上角'], [1, '右上角'], [2, '右下角'], [3, '左下角']];
const marks = [['watermark_subtitle', '字幕'], ['watermark_ltx', 'LTX'], ['watermark_lada', 'LADA'], ['watermark_jav', 'JAV'], ['watermark_hack', '其他破解'], ['watermark_uncensored', '无码'], ['watermark_censored', '有码'], ['watermark_resolution', '4K/8K']];
const images = { watermark_subtitle: 'sub.png', watermark_ltx: 'ltx.png', watermark_lada: 'lada.png', watermark_jav: 'jav.png', watermark_hack: 'hack.png', watermark_uncensored: 'wuma.png', watermark_censored: 'youma.png', watermark_resolution: '4k.png' };
const section = (title, desc) => `<div class="download-section-title"><h2>${title}</h2><p>${desc}</p></div>`;
const options = (items, value) => items.map(([id, name]) => `<option value="${h(id)}" ${String(value) === String(id) ? 'selected' : ''}>${h(name)}</option>`).join('');
const field = (config, key, title, desc, control, hidden = false) => `<div class="field${key === 'custom_folder' ? ' mode-option' : ''}" ${key === 'custom_folder' ? 'id="watermark-custom-folder-row"' : ''} ${hidden ? 'hidden' : ''}><label class="field-label" for="watermark-${key}">${title}<small>${desc}</small></label>${control}</div>`;
const select = (config, key, title, desc, items) => field(config, key, title, desc, `<select id="watermark-${key}" data-watermark="${key}">${options(items, config[key])}</select>`);
const number = (config, key, title, desc, min = 0, max = 1000, step = 1) => field(config, key, title, desc, `<input id="watermark-${key}" data-watermark="${key}" type="number" min="${min}" max="${max}" step="${step}" value="${h(config[key])}">`);
const check = (config, key, title, desc) => field(config, key, title, desc, `<input id="watermark-${key}" data-watermark="${key}" class="switch" type="checkbox" ${config[key] ? 'checked' : ''}>`);

function render(config) {
  return `${section('基本设置', '控制是否为下载的图片添加系统水印。')}
    ${check(config, 'enabled', '启用系统水印', '按文件名识别的字幕、破解类型、分辨率等添加水印。自定义规则的水印由「自定义规则」页的开关控制，不受此开关影响。')}
    ${section('系统水印图片', '已内置一套 PNG 水印。')}
    <div class="watermark-default-gallery">${[['sub.png', '字幕'], ['ltx.png', 'LTX'], ['lada.png', 'LADA'], ['jav.png', 'JAV'], ['hack.png', '破解'], ['wuma.png', '无码'], ['youma.png', '有码'], ['4k.png', '4K'], ['8k.png', '8K']].map(([file, name]) => `<div><img src="/watermarks/${file}?v=rect2" alt="${name}水印"><span>${name}</span></div>`).join('')}</div>
    <div class="field"><label class="field-label" for="watermark-custom-enabled">替换系统水印图片<small>开启后，从本机选择一个目录，用其中的同名图片替换内置的系统水印。</small></label><input id="watermark-custom-enabled" class="switch" type="checkbox" ${config.custom_folder ? 'checked' : ''}></div>
    ${field(config, 'custom_folder', '系统水印图片目录', '图片需命名为 youma.png、wuma.png、ltx.png、lada.png、jav.png、hack.png、sub.png、4k.png、8k.png。', `<div class="folder-control"><input id="watermark-custom_folder" data-watermark="custom_folder" type="text" value="${h(config.custom_folder ?? '')}" placeholder="选择水印目录"><button id="watermark-choose-folder" class="button" type="button">选择目录</button></div>`, !config.custom_folder)}
    ${section('布局与位置', '设置水印在图片上的排列方式；自定义规则的水印也按此排列。')}
    ${select(config, 'mode', '布局方式', '堆叠：横向排列；顺时针 / 逆时针：从固定位置沿对应方向排列。', [['Stack', '堆叠'], ['Clockwise', '顺时针'], ['CounterClockwise', '逆时针']])}
    ${select(config, 'start', '起始位置', '第一个水印的添加位置。', corners.slice(1))}
    ${number(config, 'scale', '缩放倍率', '图片与水印高度的比例。', 1, 100, 0.1)}
    ${number(config, 'offset_x', '横向偏移', '水印距离图片左右边缘的最小距离。')}
    ${number(config, 'offset_y', '纵向偏移', '水印距离图片上下边缘的最小距离。')}
    ${number(config, 'gap', '间距', '水印之间的距离。')}
    ${section('添加水印的图片类型', '只会为选中的图片添加水印，对系统水印和自定义规则的水印都生效。')}
    <div class="watermark-checks">${[['poster', '封面图 (poster)'], ['thumb', '缩略图 (thumb)'], ['fanart', '背景图 (fanart)']].map(([key, name]) => `<label><input type="checkbox" data-watermark="${key}" ${config[key] ? 'checked' : ''}>${name}</label>`).join('')}</div>
    ${section('添加的水印类型', '按文件名中的字幕、LTX、LADA、JAV 等标记识别（关键词在整理页设置）；只有“破解”时使用其他破解水印。')}
    <div class="watermark-checks">${marks.map(([key, name]) => `<label><input type="checkbox" data-watermark="${key}" ${config[key] ? 'checked' : ''}>${name}</label>`).join('')}</div>
    ${section('固定位置', '先定位到指定角落，再按布局方向排列；自动则按起始位置沿四角依次定位。')}
    <div class="watermark-fixed">${marks.map(([key, name]) => `<label>${name}<select data-watermark="${key}_fixed">${options(corners, config[`${key}_fixed`])}</select></label>`).join('')}</div>
    <div class="watermark-preview"><div><h3>系统水印预览</h3><p>展示位置和样式示意；实际刮削按文件名识别结果合成到所选图片。重新整理会从保留的原图重新生成，避免重复叠加。</p></div><div id="watermark-preview-image" class="watermark-preview-image"></div><label class="watermark-show-all"><input id="watermark-show-all" type="checkbox">显示全部已启用水印</label><button id="watermark-preview-refresh" class="button small" type="button">刷新预览</button></div>`;
}

function read(form, original, validateFolder = false) {
  const result = structuredClone(original);
  for (const input of form.querySelectorAll('[data-watermark]')) {
    const key = input.dataset.watermark;
    let value = input.type === 'checkbox' ? input.checked : input.type === 'number' ? Number(input.value) : input.value;
    if (input.type === 'number' && (!Number.isFinite(value) || value < Number(input.min) || value > Number(input.max))) throw Error(`${input.closest('.field')?.querySelector('.field-label')?.childNodes[0]?.textContent || key}超出有效范围`);
    if (key === 'custom_folder') value = value.trim() || null;
    if (input.tagName === 'SELECT' && (key === 'start' || key.endsWith('_fixed'))) value = Number(value);
    result[key] = value;
  }
  if (!form.querySelector('#watermark-custom-enabled').checked) result.custom_folder = null;
  else if (validateFolder && !result.custom_folder) throw Error('请先选择系统水印图片目录');
  return result;
}

function drawPreview(config, showAll) {
  const image = document.querySelector('#watermark-preview-image');
  const type = ['watermark_ltx', 'watermark_lada', 'watermark_jav', 'watermark_hack', 'watermark_uncensored', 'watermark_censored'].find((key) => config[key]);
  const chosen = showAll ? marks.filter(([key]) => config[key]) : marks.filter(([key]) => config[key] && (key === 'watermark_subtitle' || key === 'watermark_resolution' || key === type));
  const sizes = new Map();
  const factor = Math.max(0.65, Math.min(1.3, 14 / (Number(config.scale) || 14)));
  const width = Math.round(90 * factor), height = Math.round(36 * factor);
  const x = Math.min(40, Number(config.offset_x) || 0) + 10;
  const y = Math.min(40, Number(config.offset_y) || 0) + 10;
  const gap = Math.min(20, Number(config.gap) || 0) + 4;
  image.innerHTML = chosen.map(([key, label]) => {
    const fixed = Number(config[`${key}_fixed`]);
    const corner = fixed === 5 ? Number(config.start) || 0 : fixed;
    const index = sizes.get(corner) || 0;
    sizes.set(corner, index + 1);
    const horizontal = corner === 1 || corner === 2 ? `right:${x}px` : `left:${x}px`;
    const vertical = corner === 2 || corner === 3 ? `bottom:${y + index * (height + gap)}px` : `top:${y + index * (height + gap)}px`;
    return `<img class="watermark-badge" src="/watermarks/${images[key]}?v=rect2" alt="${h(label)}水印" style="${horizontal};${vertical};width:${width}px">`;
  }).join('');
}

export function renderWatermarkFields(config) {
  return render(config);
}

export function readWatermarkFields(root, original, validateFolder = false) {
  return read(root, original, validateFolder);
}

export function bindWatermarkFields(root, config, onChange) {
  const syncCustomFolder = () => {
    const row = root.querySelector('#watermark-custom-folder-row');
    if (row) row.hidden = !root.querySelector('#watermark-custom-enabled').checked;
  };
  const refresh = () => {
    try { drawPreview(read(root, config), root.querySelector('#watermark-show-all').checked); }
    catch { /* retain the last valid preview */ }
  };
  syncCustomFolder();
  refresh();
  root.addEventListener('input', (event) => { syncCustomFolder(); if (event.target.dataset.watermark || event.target.id === 'watermark-custom-enabled') onChange(); refresh(); });
  root.addEventListener('change', (event) => { syncCustomFolder(); if (event.target.dataset.watermark || event.target.id === 'watermark-custom-enabled') onChange(); refresh(); });
  root.querySelector('#watermark-preview-refresh').onclick = refresh;
}

export async function watermarkSettings({ heading, toast, markDirty, clearDirty, reload }) {
  const config = await api('/api/config/watermark');
  return {
    html: heading('设置', '配置系统水印的样式、类型和位置。') + `<div class="settings-layout watermark-settings"><nav class="settings-nav"><a href="#/settings/common">整理</a><a href="#/settings/watch-dir">监控</a><a href="#/settings/download">下载</a><a href="#/settings/naming">命名</a><a class="active" href="#/settings/watermark">系统水印</a><a href="#/settings/custom">自定义规则</a><a href="#/settings/metadata">元数据</a><a href="#/settings/nfo">NFO</a><a href="#/settings/emby">头像</a><a href="#/settings/system">系统</a><a href="#/settings/user">用户</a></nav><form id="watermark-form" class="panel">${render(config)}<div class="savebar"><span id="watermark-save-status">修改后请保存</span><div><button id="watermark-reset" class="button" type="button">撤销修改</button> <button id="watermark-save" class="button primary" type="submit">保存设置</button></div></div></form></div><dialog id="watermark-folder-dialog" class="folder-dialog"><div class="folder-dialog-head"><h2>选择水印目录</h2><button id="watermark-folder-close" class="icon-button" type="button" aria-label="关闭">×</button></div><div class="folder-location"><button id="watermark-folder-up" class="button small" type="button">↑ 上一级</button><input id="watermark-folder-path" type="text" aria-label="目录路径" placeholder="完整目录路径"><button id="watermark-folder-go" class="button small" type="button">转到</button></div><div id="watermark-folder-list" class="folder-list" role="list"></div><p id="watermark-folder-status" class="folder-status" role="status"></p><div class="folder-dialog-actions"><button id="watermark-folder-cancel" class="button" type="button">取消</button><button id="watermark-folder-select" class="button primary" type="button" disabled>选择此目录</button></div></dialog>`,
    bind() {
      const form = document.querySelector('#watermark-form');
      const $ = (selector) => document.querySelector(selector);
      const syncCustomFolder = () => { $('#watermark-custom-folder-row').hidden = !$('#watermark-custom-enabled').checked; };
      const refresh = () => { try { drawPreview(read(form, config), $('#watermark-show-all').checked); } catch { /* retain the last valid preview */ } };
      syncCustomFolder();
      refresh();
      form.oninput = form.onchange = (event) => { syncCustomFolder(); if (event.target.dataset.watermark || event.target.id === 'watermark-custom-enabled') { markDirty(); $('#watermark-save-status').textContent = '有尚未保存的修改'; } refresh(); };
      $('#watermark-preview-refresh').onclick = refresh;
      $('#watermark-reset').onclick = () => { clearDirty(); reload(); toast('已撤销未保存的修改'); };
      form.onsubmit = async (event) => { event.preventDefault(); const button = $('#watermark-save'); button.disabled = true; try { await api('/api/config/watermark', { method: 'PUT', body: read(form, config, true) }); clearDirty(); $('#watermark-save-status').textContent = '系统水印设置已保存'; toast('系统水印设置已保存'); } catch (error) { toast(error.message, true); } finally { button.disabled = false; } };
      const dialog = $('#watermark-folder-dialog'); let folder = { path: '', parent: '' };
      const browse = async (path = '') => { $('#watermark-folder-status').textContent = '正在读取文件夹…'; $('#watermark-folder-list').innerHTML = ''; $('#watermark-folder-select').disabled = true; try { folder = await api('/api/system/folders?path=' + encodeURIComponent(path)); $('#watermark-folder-path').value = folder.path; $('#watermark-folder-up').disabled = !folder.path; $('#watermark-folder-select').disabled = !folder.path; $('#watermark-folder-list').innerHTML = folder.folders.length ? folder.folders.map(item => `<button type="button" class="folder-item" data-path="${h(item.path)}"><span aria-hidden="true">▰</span>${h(item.name)}</button>`).join('') : '<div class="folder-empty">没有子文件夹</div>'; $('#watermark-folder-list').querySelectorAll('[data-path]').forEach(button => button.onclick = () => browse(button.dataset.path)); $('#watermark-folder-status').textContent = folder.path || '请选择起始目录'; } catch (error) { $('#watermark-folder-status').textContent = error.message; } };
      $('#watermark-choose-folder').onclick = () => { dialog.showModal(); browse($('#watermark-custom_folder').value.trim()); };
      $('#watermark-folder-up').onclick = () => browse(folder.parent || '');
      $('#watermark-folder-go').onclick = () => browse($('#watermark-folder-path').value.trim());
      $('#watermark-folder-path').onkeydown = (event) => { if (event.key === 'Enter') { event.preventDefault(); browse(event.target.value.trim()); } };
      $('#watermark-folder-select').onclick = () => { $('#watermark-custom_folder').value = folder.path; $('#watermark-custom_folder').dispatchEvent(new Event('input', { bubbles: true })); dialog.close(); };
      $('#watermark-folder-cancel').onclick = $('#watermark-folder-close').onclick = () => dialog.close();
    },
  };
}

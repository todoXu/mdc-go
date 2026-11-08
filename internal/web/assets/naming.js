import { api, escapeHTML as h } from './api.js';
import { pathSeparator } from './platform.js';

const mosaicTypes = [
  ['is_ltx_hack', 'LTX破解'], ['is_lada_hack', 'LADA破解'],
  ['is_jav_hack', 'JAV破解'], ['is_other_hack', '破解（其他破解方式）'],
  ['is_uncensored', '无码'], ['is_censored', '有码'],
];
const resolutions = [['resolution_hd', '720P'], ['resolution_fhd', '1080P'], ['resolution_uhd', '4K'], ['resolution_uhd8', '8K']];
const variables = ['number', 'publish_number', 'series_name', 'serial_number', 'category', 'actor', 'first_actor', 'title', 'year', 'studio', 'publisher'];
const pathKey = (path) => JSON.stringify(path);
const get = (source, path) => path.reduce((value, key) => value?.[key], source);
const set = (source, path, value) => { let parent = source; for (const key of path.slice(0, -1)) parent = parent[key]; parent[path.at(-1)] = value; };
const group = (title, desc) => `<div class="download-section-title naming-section-title"><h2>${title}</h2><p>${desc}</p></div>`;
const field = (config, path, title, desc = '', inputType = 'text') => {
  const value = get(config, path);
  const id = `naming-${path.join('-')}`;
  const attr = `id="${h(id)}" data-naming-path='${h(pathKey(path))}'`;
  let control;
  if (inputType === 'checkbox') control = `<input class="switch" type="checkbox" ${attr} ${value ? 'checked' : ''}>`;
  else if (inputType === 'number') control = `<input type="number" min="0" step="1" value="${h(value ?? 0)}" ${attr}>`;
  else if (inputType === 'resolution-type') control = `<select ${attr}><option value="0" ${Number(value) === 0 ? 'selected' : ''}>4K / 1080P / 720P</option><option value="1" ${Number(value) === 1 ? 'selected' : ''}>2160P / 1080P / 720P</option></select>`;
  else control = `<input type="text" spellcheck="false" value="${h(value ?? '')}" ${attr}>`;
  return `<div class="field"><label class="field-label" for="${h(id)}">${title}${desc ? `<small>${desc}</small>` : ''}</label>${control}</div>`;
};
const compactFields = (config, parent, items, desc) => items.map(([key, label]) => field(config, [parent, key], label, desc)).join('');
const resolutionChecks = (config, parent) => `<div class="field"><div class="field-label">生效的分辨率<small>关闭后不添加对应后缀。</small></div><div class="naming-check-row">${resolutions.map(([key, label]) => `<label><input type="checkbox" data-naming-path='${h(pathKey([parent, key]))}' ${config[parent]?.[key] ? 'checked' : ''}>${label}</label>`).join('')}</div></div>`;

function render(config) {
  return `${group('模板语法', '基础变量使用 {number}；原版还支持 Jinja2 的 {{ number }} 和条件语法，当前 Go 版执行器仍在接入。')}
    <div class="naming-variable-list">${variables.map((name) => `<code>{${name}}</code>`).join('')}</div>
    ${group('基本命名', '设置目录、媒体标题和视频文件名主体；影片类型与中文字幕标记统一在下方的视频后缀中设置。')}
    ${field(config, ['folder'], '目录命名规则', '例如 {actor}/{number}')}
    ${field(config, ['max_folder_length'], '目录最大长度', '0 表示不限制。', 'number')}
    ${field(config, ['title'], 'Emby/Plex 标题', '视频在媒体服务器中的显示标题。')}
    ${field(config, ['file'], '视频命名规则', '文件名主体，不含扩展名和下面的视频命名后缀。')}
    ${field(config, ['image_with_prefix'], '图片使用视频文件名前缀', '开启后图片名包含整理后的视频文件名。', 'checkbox')}
    ${field(config, ['max_actor_count'], '演员显示数量上限', '0 表示使用默认值 3。', 'number')}
    ${group('视频命名后缀', '后缀单独追加在视频命名规则之后，例如“ABC-123” + “-4K-无码-中文” → “ABC-123-4K-无码-中文.mp4”。')}
    ${field(config, ['suffix'], '视频命名后缀', '可使用 {resolution}、{mosaic}、{subtitle}，按书写顺序拼接；对应值在下方配置。')}
    ${group('后缀 - 马赛克', '按识别结果选择对应后缀；“破解”用于 LTX、LADA、JAV 以外的破解方式。')}
    ${compactFields(config, 'suffixs', mosaicTypes, '当前类型在视频后缀中写入的文字。')}
    ${group('后缀 - 中文字幕', '视频命名后缀中的 {subtitle}。')}
    ${field(config, ['suffixs', 'has_subtitle'], '有中文字幕')}
    ${field(config, ['suffixs', 'add_chs_to_subtitle'], '为字幕添加 .chs 后缀', '例如 ABC-123.chs.srt。', 'checkbox')}
    ${group('后缀 - 分集', '检测到多段视频时追加。')}
    ${field(config, ['suffixs', 'part_naming'], '分集后缀模板', '可使用 {part} 和 {part_letter}。')}
    ${group('后缀 - 分辨率', '视频命名后缀中的 {resolution}。')}
    ${field(config, ['suffixs', 'resolution_naming'], '分辨率后缀', '可使用 {resolution_text}。')}
    ${field(config, ['suffixs', 'resolution_type'], '分辨率显示方式', '', 'resolution-type')}
    ${resolutionChecks(config, 'suffixs')}
    ${group('分辨率信息获取方式', '信息冲突时决定优先使用视频内容还是文件名。')}
    <div class="naming-radio-row"><label><input type="radio" name="resolution-detect" value="Real" ${config.suffixs.resolution_detect_method === 'Real' ? 'checked' : ''}> 优先视频真实分辨率</label><label><input type="radio" name="resolution-detect" value="ByPath" ${config.suffixs.resolution_detect_method !== 'Real' ? 'checked' : ''}> 优先文件名和路径</label></div>
    ${field(config, ['suffixs', 'resolution_detect_fallback'], '无法识别时尝试另一种方式', '', 'checkbox')}
    ${'resolution_detect_skip_strm' in config.suffixs ? field(config, ['suffixs', 'resolution_detect_skip_strm'], 'STRM 不探测分辨率', '', 'checkbox') : ''}
    <div class="naming-preview"><h3>完整命名示例</h3><p>下方是演示素材，不会保存。修改素材或上方命名设置即可查看结果。</p>
      <div class="naming-preview-inputs">
        <label>番号<input id="naming-preview-number" type="text" value="ABC-123"></label>
        <label>演员（逗号分隔）<input id="naming-preview-actors" type="text" value="演员甲,演员乙"></label>
        <label>标题<input id="naming-preview-title" type="text" value="示例标题"></label>
        <label>分类<input id="naming-preview-category" type="text" value="日本无码"></label>
        <label>影片类型<select id="naming-preview-mosaic">${mosaicTypes.map(([key, label]) => `<option value="${key}" ${key === 'is_uncensored' ? 'selected' : ''}>${label}</option>`).join('')}</select></label>
        <label>分辨率<select id="naming-preview-resolution"><option value="">未识别</option><option value="resolution_hd">720P</option><option value="resolution_fhd">1080P</option><option value="resolution_uhd" selected>4K</option><option value="resolution_uhd8">8K</option></select></label>
        <label>分集<select id="naming-preview-part"><option value="0">单集</option><option value="1">第 1 集</option><option value="2">第 2 集</option></select></label>
        <label class="naming-preview-checkbox"><input id="naming-preview-subtitle" type="checkbox" checked> 有中文字幕</label>
      </div>
      <div id="naming-preview-result"></div>
      <p class="naming-preview-note">预览支持基础变量；高级 Jinja2 模板、目录截断和分辨率自动检测将在实际命名功能中处理。</p>
    </div>`;
}

function readForm(form, original) {
  const next = structuredClone(original);
  for (const input of form.querySelectorAll('[data-naming-path]')) {
    const path = JSON.parse(input.dataset.namingPath);
    let value = input.type === 'checkbox' ? input.checked : input.type === 'number' || input.tagName === 'SELECT' ? Number(input.value) : input.value;
    if (input.type === 'number' && (!Number.isSafeInteger(value) || value < 0)) throw Error('数量须为有效的非负整数');
    set(next, path, value);
  }
  next.suffixs.resolution_detect_method = form.querySelector('input[name="resolution-detect"]:checked')?.value || 'Real';
  return next;
}

function preview(config, form) {
  const value = (id) => form.querySelector('#naming-preview-' + id).value.trim();
  const number = value('number') || '未知';
  const actors = value('actors').split(/[,，]/).map((item) => item.trim()).filter(Boolean);
  const actorLimit = config.max_actor_count || 3;
  const actor = actors.length > actorLimit ? '多人作品' : actors.join('、') || '未知';
  const title = value('title') || '未知';
  const category = value('category') || '未知';
  const hasSubtitle = form.querySelector('#naming-preview-subtitle').checked;
  const resolutionKey = value('resolution');
  const resolutionLabels = { resolution_hd: '720P', resolution_fhd: '1080P', resolution_uhd: config.suffixs.resolution_type === 1 ? '2160P' : '4K', resolution_uhd8: config.suffixs.resolution_type === 1 ? '4320P' : '8K' };
  const resolutionText = resolutionLabels[resolutionKey] || '';
  const resolutionSuffix = resolutionKey && config.suffixs[resolutionKey]
    ? String(config.suffixs.resolution_naming || '').replaceAll('{resolution_text}', resolutionText) : '';
  const rawPart = Number(value('part'));
  const part = rawPart > 0 ? String(config.suffixs.part_naming || '').replaceAll('{part}', String(rawPart)).replaceAll('{part_letter}', String.fromCharCode(64 + rawPart)) : '';
  const sample = {
    number, title, category, actor, first_actor: actors[0] || '未知',
    publish_number: '118abc123', series_name: number.split('-')[0] || 'ABC',
    serial_number: number.split('-').at(-1) || '123', first_letter: number[0] || 'A',
    year: '2026', studio: '示例片商', publisher: '示例发行商',
    series: '示例系列', director: '示例导演', release: '2026-01-01', runtime: '120',
    source_filename: number, filename: number,
  };
  const apply = (template, data) => String(template || '').replace(/\{([a-z_]+)\}/g, (_, key) => data[key] ?? `{${key}}`);
  const folder = apply(config.folder, sample);
  // 预览按程序运行所在系统的分隔符显示路径。
  const separator = pathSeparator();
  const folderPath = folder.replace(/[\\/]+/g, separator).replace(/[\\/]+$/, '');
  const mediaTitle = apply(config.title, sample);
  const body = apply(config.file, sample);
  const mosaicSuffix = config.suffixs[value('mosaic')] || '';
  const subtitleSuffix = hasSubtitle ? config.suffixs.has_subtitle || '' : '';
  const suffixTemplate = String(config.suffix || '');
  const suffix = apply(suffixTemplate, {
    mosaic: mosaicSuffix,
    subtitle: subtitleSuffix,
    resolution: resolutionSuffix,
  });
  const videoStem = body + suffix + part;
  const videoName = videoStem + '.mp4';
  const relativePath = [folderPath, videoName].filter(Boolean).join(separator);
  const shown = (token, value) => suffixTemplate.includes(`{${token}}`) ? value || '（无）' : '后缀模板未使用此项';
  const outputs = [
    ['整理后目录', folderPath || '（原目录）'], ['Emby/Plex 标题', mediaTitle || '（空）'],
    ['马赛克后缀', shown('mosaic', mosaicSuffix)],
    ['中文字幕后缀', shown('subtitle', subtitleSuffix)],
    ['分辨率后缀', shown('resolution', resolutionSuffix)],
    ['分集后缀', part || '（无）'],
    ['视频文件名主体', body || '（空）'], ['合成后缀', suffix + part || '（无）'],
    ['完整视频文件名', videoName],
    ['最终相对路径', relativePath],
    ['图片名前缀', config.image_with_prefix ? videoStem : '不添加视频文件名前缀'],
  ];
  if (config.max_folder_length > 0 && folder.length > config.max_folder_length) outputs.push(['目录长度提示', `当前 ${folder.length} 字，超过上限 ${config.max_folder_length}；实际整理时需截断`]);
  return outputs.map(([label, result]) => `<div><span>${h(label)}</span><b>${h(result)}</b></div>`).join('');
}

export function renderNamingFields(config) {
  return render(config);
}

export function readNamingFields(root, original) {
  return readForm(root, original);
}

export function bindNamingFields(root, config, onChange) {
  const update = () => {
    try { root.querySelector('#naming-preview-result').innerHTML = preview(readForm(root, config), root); }
    catch (error) { root.querySelector('#naming-preview-result').innerHTML = `<div class="naming-preview-error">${h(error.message)}</div>`; }
  };
  update();
  root.addEventListener('input', (event) => { if (!event.target.closest('.naming-preview')) onChange(); update(); });
  root.addEventListener('change', (event) => { if (!event.target.closest('.naming-preview')) onChange(); update(); });
  return update;
}

export async function namingSettings({ heading, toast, markDirty, clearDirty, reload }) {
  const config = await api('/api/config/naming');
  return {
    html: heading('设置', '配置目录、视频文件和媒体标题的命名方式。') + `<div class="settings-layout"><nav class="settings-nav"><a href="#/settings/common">整理</a><a href="#/settings/watch-dir">监控</a><a href="#/settings/download">下载</a><a class="active" href="#/settings/naming">命名</a><a href="#/settings/watermark">系统水印</a><a href="#/settings/custom">自定义规则</a><a href="#/settings/metadata">元数据</a><a href="#/settings/nfo">NFO</a><a href="#/settings/emby">头像</a><a href="#/settings/system">系统</a><a href="#/settings/user">用户</a></nav><form id="naming-form" class="panel">${render(config)}<div class="savebar"><span id="naming-save-status">修改后请保存</span><div><button class="button" id="naming-reset" type="button">撤销修改</button> <button class="button primary" id="naming-save" type="submit">保存设置</button></div></div></form></div>`,
    bind() {
      const form = document.querySelector('#naming-form');
      const update = () => { try { document.querySelector('#naming-preview-result').innerHTML = preview(readForm(form, config), form); } catch (error) { document.querySelector('#naming-preview-result').innerHTML = `<div class="naming-preview-error">${h(error.message)}</div>`; } };
      update();
      form.oninput = form.onchange = (event) => { if (!event.target.closest('.naming-preview')) { markDirty(); document.querySelector('#naming-save-status').textContent = '有尚未保存的修改'; } update(); };
      document.querySelector('#naming-reset').onclick = () => { clearDirty(); reload(); toast('已撤销未保存的修改'); };
      form.onsubmit = async (event) => {
        event.preventDefault(); const button = document.querySelector('#naming-save'); button.disabled = true;
        try { await api('/api/config/naming', { method: 'PUT', body: readForm(form, config) }); clearDirty(); document.querySelector('#naming-save-status').textContent = '命名设置已保存'; toast('命名设置已保存'); }
        catch (error) { toast(error.message, true); }
        finally { button.disabled = false; }
      };
    },
  };
}

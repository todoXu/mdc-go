import { api, escapeHTML as h } from './api.js';

// 国家代码写入 <countrycode>；分级（mpaa / customrating）仍由数据源或手动设置决定。
const countryOptions = [['JP', '日本'], ['CN', '国产'], ['US', '欧美']];

const section = (title, desc = '') => `<div class="download-section-title"><h2>${title}</h2>${desc ? `<p>${desc}</p>` : ''}</div>`;
const checkItem = (config, [key, label, note]) => {
  const path = key.split('.');
  const value = path.reduce((current, part) => current?.[part], config);
  return `<label><input type="checkbox" data-nfo="${h(key)}" ${value ? 'checked' : ''}><span>${label}${note ? `<small>${note}</small>` : ''}</span></label>`;
};
const choices = (config, entries) => `<div class="nfo-checks">${entries.map((entry) => checkItem(config, entry)).join('')}</div>`;
const field = (config, key, title, desc) => {
  const path = key.split('.');
  const value = path.reduce((current, part) => current?.[part], config);
  return `<div class="field"><label class="field-label" for="nfo-${h(key.replaceAll('.', '-'))}">${title}<small>${desc}</small></label><input id="nfo-${h(key.replaceAll('.', '-'))}" data-nfo="${h(key)}" type="text" value="${h(value ?? '')}" spellcheck="false"></div>`;
};

function render(config) {
  return `${section('基本设置')}
    <div class="field"><label class="field-label" for="nfo-enabled">启用 NFO<small>根据刮削结果生成 .nfo 文件。</small></label><input id="nfo-enabled" class="switch" type="checkbox" data-nfo="enabled" ${config.enabled ? 'checked' : ''}></div>
    ${section('标题')}
    ${choices(config, [['enable_sorttitle', '类标题 (sorttitle)'], ['enable_originaltitle', '原标题 (originaltitle)'], ['add_part_to_title', '标题后添加分集信息']])}
    ${section('简介')}
    ${choices(config, [['enable_outline', '简短简介 (outline)'], ['enable_plot', '完整简介 (plot)'], ['enable_originalplot', '原简介 (originalplot)'], ['add_translated_by', '简介后添加翻译来源信息', '例如“由 Google 提供翻译”']])}
    ${section('演员 / 导演')}
    ${choices(config, [['enable_actor', '演员 (actor)'], ['include_male_actors', '写入男演员', '关闭时优先仅写入女演员；性别未知的演员仍保留。'], ['enable_director', '导演 (director)']])}
    ${section('发行日期')}
    ${choices(config, [['enable_release', '发行日期 (release)'], ['enable_releasedate', '发行日期 (releasedate)'], ['enable_premiered', '发行日期 (premiered)']])}
    ${field(config, 'tagline_format', '副标题格式（tagline）', '可使用命名页的字段，例如“发行日期: {release}”；留空则不写入副标题。')}
    ${section('国家 / 分级')}
    <div class="nfo-checks nfo-country-checks"><div class="nfo-country-cell">${checkItem(config, ['enable_country', '国家 (countrycode)'])}<select class="nfo-country-select" data-nfo="country_code" aria-label="国家代码（countrycode）">${countryOptions.map(([code, label]) => `<option value="${code}" ${(config.country_code || 'JP') === code ? 'selected' : ''}>${label}</option>`).join('')}</select></div>${checkItem(config, ['enable_mpaa', '分级信息 (mpaa)'])}${checkItem(config, ['enable_customrating', '自定义分级 (customrating)'])}</div>
    ${section('年份 / 时长 / 评分人数')}
    ${choices(config, [['enable_year', '年份 (year)'], ['enable_runtime', '时长 (runtime)'], ['enable_votes', '评分人数 (votes)']])}
    ${section('评分')}
    ${choices(config, [['enable_score', '公众评分 (score)'], ['enable_criticrating', '影评人评分 (criticrating)']])}
    ${section('系列 / 标签 / 风格')}
    ${choices(config, [['enable_series', '系列 (series)'], ['enable_tag', '标签 (tag)'], ['enable_genre', '风格 (genre)', '使用标签数据生成。']])}
    <div class="nfo-subtitle">附加标签内容</div>
    ${choices(config, [['additional_tags.serial_name', '番号前缀'], ['additional_tags.actors', '演员'], ['additional_tags.resolution', '分辨率'], ['additional_tags.has_subtitle', '中文字幕'], ['additional_tags.mosaic', '有码 / 无码'], ['additional_tags.hack_type', '破解类型'], ['additional_tags.series', '系列'], ['additional_tags.studio', '片商'], ['additional_tags.publisher', '发行商']])}
    ${field(config, 'additional_tags.subtitle_format', '标签格式：中文字幕', '例如“中文字幕”。')}
    ${field(config, 'additional_tags.series_format', '标签格式：系列', '可使用 {series}。')}
    ${field(config, 'additional_tags.studio_format', '标签格式：片商', '可使用 {studio}。')}
    ${field(config, 'additional_tags.publisher_format', '标签格式：发行商', '可使用 {publisher}。')}
    ${section('片商 / 发行商')}
    ${choices(config, [['enable_studio', '片商 (studio)'], ['enable_maker', '片商 (maker)'], ['enable_publisher', '发行商 (publisher)'], ['enable_label', '发行商 (label)']])}
    ${section('海报 / 封面 / 网址')}
    ${choices(config, [['enable_poster', '海报 (poster)'], ['enable_cover', '封面等 (cover/thumb/fanart)'], ['enable_trailer', '预告片 (trailer)'], ['enable_website', '网址 (website)']])}
    ${section('合集', '根据对应的元数据信息创建合集。')}
    ${choices(config, [['set_by_actress', '使用演员字段'], ['set_by_series', '使用系列字段'], ['set_by_serial_name', '使用番号前缀']])}`;
}

function read(form, original) {
  const result = structuredClone(original);
  for (const control of form.querySelectorAll('[data-nfo]')) {
    const path = control.dataset.nfo.split('.');
    let parent = result;
    for (const part of path.slice(0, -1)) parent = parent[part];
    parent[path.at(-1)] = control.type === 'checkbox' ? control.checked : control.value;
  }
  return result;
}

export function renderNFOFields(config) {
  return render(config);
}

export function readNFOFields(root, original) {
  return read(root, original);
}

export async function nfoSettings({ heading, toast, markDirty, clearDirty, reload }) {
  const config = await api('/api/config/nfo');
  return {
    html: heading('设置', '配置 NFO 文件中写入的字段和附加标签。') + `<div class="settings-layout nfo-settings"><nav class="settings-nav"><a href="#/settings/common">整理</a><a href="#/settings/watch-dir">监控</a><a href="#/settings/download">下载</a><a href="#/settings/naming">命名</a><a href="#/settings/watermark">水印</a><a href="#/settings/metadata">元数据</a><a class="active" href="#/settings/nfo">NFO</a><a href="#/settings/emby">头像</a><a href="#/settings/system">系统</a><a href="#/settings/user">用户</a></nav><form id="nfo-form" class="panel">${render(config)}<div class="savebar"><span id="nfo-save-status">修改后请保存</span><div><button id="nfo-reset" class="button" type="button">撤销修改</button> <button id="nfo-save" class="button primary" type="submit">保存设置</button></div></div></form></div>`,
    bind() {
      const form = document.querySelector('#nfo-form');
      form.oninput = form.onchange = () => { markDirty(); document.querySelector('#nfo-save-status').textContent = '有尚未保存的修改'; };
      document.querySelector('#nfo-reset').onclick = () => { clearDirty(); reload(); toast('已撤销未保存的修改'); };
      form.onsubmit = async (event) => {
        event.preventDefault(); const button = document.querySelector('#nfo-save'); button.disabled = true;
        try { await api('/api/config/nfo', { method: 'PUT', body: read(form, config) }); clearDirty(); document.querySelector('#nfo-save-status').textContent = 'NFO 设置已保存'; toast('NFO 设置已保存'); }
        catch (error) { toast(error.message, true); }
        finally { button.disabled = false; }
      };
    },
  };
}

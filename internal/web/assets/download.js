import { api } from './api.js';

const choices = [
  ['poster', '海报/封面图 (poster)', '保存独立海报；缺少可用海报时可从缩略图裁剪。'],
  ['thumb', '缩略图 (thumb)', '建议开启，背景图与海报可使用缩略图。'],
  ['fanart', '背景图 (fanart)', '复用选中的缩略图原图，按背景图水印设置保存。'],
  ['extra_fanarts', '剧照 (extrafanart)', '下载数据源提供的剧照，按视频名前缀编号保存。'],
  ['trailer', '预告片 (trailer)', '下载数据源提供的 MP4/WebM 预告片；未提供时记录提示并跳过。'],
];
const strategies = [
  ['Priority', '根据字段优先级', '按数据源页面的字段优先级依次下载，当前来源无法获取时尝试下一来源。'],
  ['Quality', '根据图片质量', '比较全部候选的实际像素总数，选择最高的图片。'],
];
const cropMethods = [['None', '不裁剪'], ['Right', '右侧裁剪'], ['Center', '中心裁剪'], ['Face', '人脸识别（未检测到时中心裁剪）']];
const cropTypes = [
  ['crop_type_censored', '常规作品', '设置常规作品的海报裁剪方式，建议右侧裁剪。'],
  ['crop_type_fc2', 'FC2', '建议人脸识别。'],
];
const checkbox = (key, title, desc, config) => `<div class="field"><label class="field-label" for="download-${key}">${title}<small>${desc}</small></label><input class="switch" id="download-${key}" data-download="${key}" type="checkbox" ${config[key] ? 'checked' : ''}></div>`;
const select = (key, title, desc, options, config) => `<div class="field"><label class="field-label" for="download-${key}">${title}<small>${desc}</small></label><select id="download-${key}" data-download="${key}">${options.map(([value, label]) => `<option value="${value}" ${config[key] === value ? 'selected' : ''}>${label}</option>`).join('')}</select></div>`;
const section = (title, desc) => `<div class="download-section-title"><h2>${title}</h2><p>${desc}</p></div>`;

export function renderDownloadFields(config) {
  const selectedStrategy = config.thumb_download_strategy === 'Quality' ? 'Quality' : 'Priority';
  return `<div class="download-fields">
    ${section('下载内容', '选择从刮削源下载的资源类型。')}
    ${choices.map(([key, title, desc]) => checkbox(key, title, desc, config)).join('')}
    ${section('缩略图下载策略', '全部候选不可用时任务失败；背景图复用同一张原图，避免重复下载。')}
    <div class="download-strategies">${strategies.map(([value, title, desc]) => `<label class="download-strategy"><input type="radio" name="thumb-download-strategy" value="${value}" ${selectedStrategy === value ? 'checked' : ''}><span><b>${title}</b><small>${desc}</small></span></label>`).join('')}</div>
    ${section('海报裁剪', '独立海报不达标时，可从选中的缩略图裁剪。')}
    ${cropTypes.map(([key, title, desc]) => select(key, title, desc, cropMethods, config)).join('')}
    ${select('crop_aspect', '裁剪比例', '选择完整海报或 Emby 常用比例。', [['Full', '完整海报 (2.12:3)'], ['Emby', 'Emby 比例 (2:3)']], config)}
    ${checkbox('crop_poster', '独立海报裁剪', '对独立海报进行居中裁剪。', config)}
    ${checkbox('crop_when_poster_low_res', '优先选择裁剪结果', '比较独立海报与缩略图裁剪结果的分辨率。', config)}
  </div>`;
}

export function readDownloadFields(root) {
  const result = {};
  root.querySelectorAll('[data-download]').forEach((input) => { result[input.dataset.download] = input.type === 'checkbox' ? input.checked : input.type === 'number' ? Number(input.value) : input.value; });
  result.thumb_download_strategy = root.querySelector('input[name="thumb-download-strategy"]:checked')?.value;
  if (!strategies.some(([value]) => value === result.thumb_download_strategy)) throw Error('请选择缩略图下载策略');
  return result;
}

export async function downloadSettings({ heading, toast, markDirty, clearDirty, reload }) {
  const config = await api('/api/config/download');
  return {
    html: heading('设置', '配置图片与预告片的下载方式。') + `<div class="settings-layout"><nav class="settings-nav"><a href="#/settings/common">整理</a><a href="#/settings/watch-dir">监控</a><a class="active" href="#/settings/download">下载</a><a href="#/settings/naming">命名</a><a href="#/settings/watermark">水印</a><a href="#/settings/metadata">元数据</a><a href="#/settings/nfo">NFO</a><a href="#/settings/emby">头像</a><a href="#/settings/system">系统</a><a href="#/settings/user">用户</a></nav><form id="download-form" class="panel">${renderDownloadFields(config)}<div class="savebar"><span id="download-save-status">修改后请保存</span><div><button class="button" id="download-reset" type="button">撤销修改</button> <button class="button primary" id="download-save" type="submit">保存设置</button></div></div></form></div>`,
    bind() {
      const form = document.querySelector('#download-form');
      form.oninput = form.onchange = () => { markDirty(); document.querySelector('#download-save-status').textContent = '有尚未保存的修改'; };
      document.querySelector('#download-reset').onclick = () => { clearDirty(); reload(); toast('已撤销未保存的修改'); };
      form.onsubmit = async (event) => {
        event.preventDefault();
        const button = document.querySelector('#download-save'); button.disabled = true;
        try {
          await api('/api/config/download', { method: 'PUT', body: readDownloadFields(form) });
          clearDirty(); document.querySelector('#download-save-status').textContent = '下载设置已保存'; toast('下载设置已保存');
        } catch (error) { toast(error.message, true); }
        finally { button.disabled = false; }
      };
    },
  };
}

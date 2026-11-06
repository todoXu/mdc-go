import { api, escapeHTML as h } from './api.js';

const section = (title, desc) => `<div class="download-section-title"><h2>${title}</h2><p>${desc}</p></div>`;
const field = (key, title, desc, control, extra = '') => `<div class="field ${extra}" id="metadata-row-${key}"><label class="field-label" for="metadata-${key}">${title}${desc ? `<small>${desc}</small>` : ''}</label>${control}</div>`;
const input = (config, key, title, desc, type = 'text', extra = '') => field(key, title, desc, `<input id="metadata-${key}" data-metadata="${key}" type="${type}" value="${h(config[key] ?? '')}" spellcheck="false">`, extra);
const check = (config, key, title, desc, extra = '') => field(key, title, desc, `<input id="metadata-${key}" data-metadata="${key}" class="switch" type="checkbox" ${config[key] ? 'checked' : ''}>`, extra);
const select = (config, key, title, desc, items) => field(key, title, desc, `<select id="metadata-${key}" data-metadata="${key}">${items.map(([value, label]) => `<option value="${h(value)}" ${config[key] === value ? 'selected' : ''}>${label}</option>`).join('')}</select>`);

function render(config) {
  return `${section('数据源连接', '刮削源需要验证时使用。')}
    ${input(config, 'flaresolverr', 'FlareSolverr / Trawl 地址（可选）', 'FlareSolverr 地址以 /v1 结尾；Trawl 地址以 /scrape 结尾。刮削源遇到 Cloudflare 验证时使用。')}
    ${section('数据校验', '刮削后检查元数据的完整性。')}
    ${check(config, 'strict_mode', '启用严格字段模式', '刮削源出错时检查关键字段是否缺失。')}
    ${check(config, 'image_strict_mode', '强制校验图片结果', '封面或缩略图未获取到时视为刮削失败。')}
    ${section('元数据优化', '刮削后对演员、标签和简介进行处理。')}
    ${check(config, 'use_actor_mapping', '启用演员数据映射', '使用演员映射表规范化演员名称。')}
    ${check(config, 'use_tag_mapping', '启用标签数据映射', '使用内置数据表规范化标签描述。')}
    ${check(config, 'outline_trim_multiple_br', '精简简介中的多余换行', '将多个连续换行精简为单个。')}
    ${check(config, 'actor_mapping_custom', '自定义演员映射仓库', '开启后从指定地址获取演员映射表。')}
    <div id="metadata-custom-fields" ${config.actor_mapping_custom ? '' : 'hidden'}>
      ${input(config, 'actor_mapping_repository', '演员映射仓库地址', '支持 GitHub 仓库地址或 XML 文件直链；修改地址后需重新验证。', 'text', 'mode-option')}
      <div class="metadata-verify"><button id="metadata-verify-button" class="button small" type="button">验证地址</button><button id="metadata-check-button" class="button small" type="button">检查更新</button><span id="metadata-verify-status" role="status">${config.actor_mapping_repository ? '已保存的地址；修改后请重新验证。' : '填写地址后点击验证。'}</span></div>
    </div>
    ${select(config, 'static_data_lang', '数据映射语言', '演员名、标签等静态数据映射使用的语言。', [['zh_cn', '简体中文'], ['zh_tw', '繁体中文'], ['jp', '日文']])}
    ${section('自动翻译', '刮削后翻译选中的元数据字段。')}
    ${check(config, 'auto_translate_title', '翻译标题', '自动翻译影片标题。')}
    ${check(config, 'auto_translate_outline', '翻译简介', '自动翻译影片简介。')}
    ${select(config, 'translate_engine', '翻译引擎', 'OpenAI 兼容接口的参数可在系统设置中配置。', [['Google', 'Google'], ['DeepL', 'DeepL'], ['OpenAI', 'OpenAI 兼容']])}
    <div id="metadata-deepl-fields" ${config.translate_engine === 'DeepL' ? '' : 'hidden'}>
      ${input(config, 'deepl_endpoint', 'DeepL 接口地址', '留空时使用 https://api-free.deepl.com/v2/translate。')}
      ${input(config, 'deepl_api_key', 'DeepL API Key', '用于访问 DeepL 翻译接口。', 'password')}
    </div>
    <div id="metadata-openai-fields" ${config.translate_engine === 'OpenAI' ? '' : 'hidden'}>
      ${field('openai_custom_prompt', '自定义 System Prompt（可选）', '供 OpenAI 兼容翻译接口使用。', `<textarea id="metadata-openai_custom_prompt" data-metadata="openai_custom_prompt">${h(config.openai_custom_prompt ?? '')}</textarea>`)}
    </div>`;
}

function read(form, original) {
  const result = structuredClone(original);
  for (const control of form.querySelectorAll('[data-metadata]')) {
    result[control.dataset.metadata] = control.type === 'checkbox' ? control.checked : control.value.trim();
  }
  const endpoint = result.flaresolverr;
  if (endpoint) {
    let parsed;
    try { parsed = new URL(endpoint); } catch { throw Error('FlareSolverr / Trawl 地址格式无效'); }
    if (!['http:', 'https:'].includes(parsed.protocol)) throw Error('FlareSolverr / Trawl 地址须使用 HTTP 或 HTTPS');
  }
  return result;
}

export async function metadataSettings({ heading, toast, markDirty, clearDirty, reload }) {
  const config = await api('/api/config/metadata');
  return {
    html: heading('设置', '校验、优化和翻译刮削得到的元数据。') + `<div class="settings-layout metadata-settings"><nav class="settings-nav"><a href="#/settings/common">整理</a><a href="#/settings/watch-dir">监控</a><a href="#/settings/download">下载</a><a href="#/settings/naming">命名</a><a href="#/settings/watermark">水印</a><a class="active" href="#/settings/metadata">元数据</a><a href="#/settings/nfo">NFO</a><a href="#/settings/emby">头像</a><a href="#/settings/system">系统</a><a href="#/settings/user">用户</a></nav><form id="metadata-form" class="panel">${render(config)}<div class="savebar"><span id="metadata-save-status">修改后请保存</span><div><button id="metadata-reset" class="button" type="button">撤销修改</button> <button id="metadata-save" class="button primary" type="submit">保存设置</button></div></div></form></div>`,
    bind() {
      const form = document.querySelector('#metadata-form');
      const $ = (selector) => form.querySelector(selector);
      let verifiedURL = config.actor_mapping_custom ? config.actor_mapping_repository : '';
      const sync = () => {
        $('#metadata-custom-fields').hidden = !$('#metadata-actor_mapping_custom').checked;
        $('#metadata-deepl-fields').hidden = $('#metadata-translate_engine').value !== 'DeepL';
        $('#metadata-openai-fields').hidden = $('#metadata-translate_engine').value !== 'OpenAI';
      };
      sync();
      form.oninput = form.onchange = (event) => {
        if (event.target.id === 'metadata-actor_mapping_repository') {
          verifiedURL = '';
          $('#metadata-verify-status').textContent = '地址已修改，请重新验证。';
        }
        if (event.target.dataset.metadata) { markDirty(); $('#metadata-save-status').textContent = '有尚未保存的修改'; }
        sync();
      };
      $('#metadata-check-button').onclick = async () => {
        const button = $('#metadata-check-button'); button.disabled = true;
        $('#metadata-verify-status').textContent = '正在检查远端映射表…';
        try {
          const result = await api('/api/config/metadata/check-actor-mapping', { method: 'POST', timeout: 45000 });
          $('#metadata-verify-status').textContent = result.updated ? '已下载远端更新，自定义表已替换。' : '远端映射表没有变化。';
          toast(result.updated ? '自定义演员映射表已更新' : '已是最新的映射表');
        } catch (error) { $('#metadata-verify-status').textContent = error.message; toast(error.message, true); }
        finally { button.disabled = false; }
      };
      $('#metadata-verify-button').onclick = async () => {
        const address = $('#metadata-actor_mapping_repository').value.trim();
        if (!address) { toast('请先填写演员映射仓库地址', true); return; }
        const button = $('#metadata-verify-button'); button.disabled = true;
        $('#metadata-verify-status').textContent = '正在检查地址和 XML…';
        try {
          const result = await api('/api/config/metadata/verify-actor-mapping', { method: 'POST', body: { url: address }, timeout: 45000 });
          verifiedURL = address;
          $('#metadata-verify-status').textContent = `验证通过，已下载自定义表${result.updated ? '（有更新）' : '（与远端一致）'}：${result.document_url}`;
          toast('演员映射地址验证通过');
        } catch (error) { verifiedURL = ''; $('#metadata-verify-status').textContent = error.message; toast(error.message, true); }
        finally { button.disabled = false; }
      };
      $('#metadata-reset').onclick = () => { clearDirty(); reload(); toast('已撤销未保存的修改'); };
      form.onsubmit = async (event) => {
        event.preventDefault(); const button = $('#metadata-save'); button.disabled = true;
        try {
          const next = read(form, config);
          if (next.actor_mapping_custom && (!next.actor_mapping_repository || verifiedURL !== next.actor_mapping_repository)) throw Error('请先验证演员映射仓库地址');
          await api('/api/config/metadata', { method: 'PUT', body: next });
          clearDirty(); $('#metadata-save-status').textContent = '元数据设置已保存'; toast('元数据设置已保存');
        } catch (error) { toast(error.message, true); }
        finally { button.disabled = false; }
      };
    },
  };
}

import { api, escapeHTML as h } from './api.js';
import { renderDownloadFields, readDownloadFields } from './download.js';
import { renderNamingFields, readNamingFields, bindNamingFields } from './naming.js';
import { renderWatermarkFields, readWatermarkFields, bindWatermarkFields } from './watermark.js';
import { renderNFOFields, readNFOFields } from './nfo.js';
import { mountDirectoryProviders, readDirectoryProviders } from './directory-providers.js';
import { onJobEvents } from './job-events.js';
import { isAbsolutePath, absolutePathHint, isWindows } from './platform.js';

const commonKeys = [
  'target_folder', 'delete_metadata_folder_when_failed', 'link_mode', 'strm_path_from', 'strm_path_to',
  'delete_empty_parent_after_move', 'target_overwrite_video', 'target_overwrite_pic',
  'file_size_filter', 'file_ext_filter', 'file_name_blacklist',
  'subtitle_keywords', 'jav_hack_keywords', 'lada_hack_keywords', 'ltx_hack_keywords', 'other_hack_keywords', 'uncensored_keywords',
];
const modes = [['2', '移动'], ['3', '原地整理'], ['4', 'STRM']];
const $ = (selector, root = document) => root.querySelector(selector);

// 新监控目录的 ID（UUID v4 格式）。crypto.randomUUID 只在 HTTPS 或 localhost 下可用，
// 通过局域网 IP 用 http 打开页面时没有这个函数，所以退回用 crypto.getRandomValues 生成。
function newDirectoryID() {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = [...bytes].map((value) => value.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

function copyCommon(source) {
  return Object.fromEntries(commonKeys.map((key) => [key, structuredClone(source[key] ?? (key.endsWith('_keywords') || key.endsWith('_blacklist') || key === 'file_ext_filter' ? [] : key === 'target_folder' ? '' : 0))]));
}

function tags(id, items, label) {
  return `<div class="tag-editor" id="${id}"><div class="tag-items">${(items || []).map((item) => `<span class="tag-chip" data-value="${h(item)}"><span>${h(item)}</span><button type="button" class="tag-remove" aria-label="移除 ${h(item)}">×</button></span>`).join('')}</div><button type="button" class="tag-add">＋ 添加</button><span class="tag-entry" hidden><input type="text" spellcheck="false" aria-label="添加${h(label)}" placeholder="输入后按回车"><button type="button" class="tag-confirm" aria-label="确认添加">✓</button><button type="button" class="tag-cancel" aria-label="取消添加">×</button></span></div>`;
}

function readTags(id) {
  return [...$('#' + id).querySelectorAll('.tag-chip')].map((chip) => chip.dataset.value);
}

function bindTags(root, toast) {
  for (const editor of root.querySelectorAll('.tag-editor')) {
    const input = $('.tag-entry input', editor);
    const add = $('.tag-add', editor);
    const close = () => { input.value = ''; $('.tag-entry', editor).hidden = true; add.hidden = false; };
    const commit = () => {
      const entries = input.value.split(/[,，]/).map((item) => {
        const trimmed = item.trim();
        return editor.id === 'watch-extensions' ? trimmed.replace(/^\./, '').toLowerCase() : trimmed;
      }).filter(Boolean);
      if (editor.id === 'watch-extensions' && entries.some((item) => !/^[a-z0-9]+$/.test(item))) throw Error('白名单中有无效的扩展名');
      const existing = new Set(readTags(editor.id).map((item) => item.toLowerCase()));
      for (const item of entries) {
        if (existing.has(item.toLowerCase())) continue;
        existing.add(item.toLowerCase());
        const chip = document.createElement('span');
        chip.className = 'tag-chip';
        chip.dataset.value = item;
        const text = document.createElement('span');
        text.textContent = item;
        const remove = document.createElement('button');
        remove.type = 'button';
        remove.className = 'tag-remove';
        remove.setAttribute('aria-label', '移除 ' + item);
        remove.textContent = '×';
        chip.append(text, remove);
        $('.tag-items', editor).append(chip);
      }
      close();
    };
    add.onclick = () => { add.hidden = true; $('.tag-entry', editor).hidden = false; input.focus(); };
    $('.tag-confirm', editor).onclick = () => { try { commit(); } catch (error) { toast(error.message, true); } };
    $('.tag-cancel', editor).onclick = close;
    input.onkeydown = (event) => {
      if (event.key === 'Enter') { event.preventDefault(); try { commit(); } catch (error) { toast(error.message, true); } }
      if (event.key === 'Escape') close();
    };
    $('.tag-items', editor).onclick = (event) => event.target.closest('.tag-remove')?.closest('.tag-chip').remove();
    editor.commit = commit;
  }
}

export async function watchSettings({ heading, toast, markDirty, clearDirty, reload }) {
  const [global, globalDownload, globalNaming, globalWatermark, globalNFO, providerList, globalPriorities] = await Promise.all([
    api('/api/config/common'), api('/api/config/download'), api('/api/config/naming'),
    api('/api/config/watermark'), api('/api/config/nfo'), api('/api/providers'), api('/api/providers/priorities'),
  ]);
  const globalProviderConfigs = Object.fromEntries(await Promise.all(providerList.data.map(async (provider) => [provider.id, await api('/api/providers/' + encodeURIComponent(provider.id) + '/config')])));
  const draft = {
    watcher_enabled: !!global.watcher_enabled,
    watcher_poll_interval_seconds: Number(global.watcher_poll_interval_seconds || 30),
    watch_dirs: structuredClone(global.watch_dirs || []),
  };
  const stableState = (value) => JSON.stringify(value, (_key, item) => item && !Array.isArray(item) && typeof item === 'object'
    ? Object.fromEntries(Object.entries(item).sort(([a], [b]) => a.localeCompare(b)))
    : item);
  let savedState = stableState(draft);
  let status = await api('/api/watcher/status');
  const recentHTML = (items) => items?.length ? items.slice(-8).reverse().map((item) => `<div class="watch-recent-item" title="${h(item.path)}"><b>${h(item.path.split(/[\\/]/).pop())}</b><span>${h(item.path)}</span></div>`).join('') : '<div class="watch-recent-empty">暂无新发现的文件</div>';
  const row = (dir, index) => {
    const scanned = status.directories?.find((item) => item.id === dir.id);
    const summary = `${dir.use_global_config ? '使用全局整理配置' : `独立整理配置 · ${h(dir.common?.target_folder || '尚未设置目标目录')}`} · ${dir.use_global_download === false ? '独立下载配置' : '使用全局下载配置'}`;
    return `<article class="watch-card" data-watch-id="${h(dir.id)}"><div class="watch-card-head"><b>监控目录 ${index + 1}</b><span class="watch-dir-status">${h(scanned?.error || (scanned ? `匹配 ${scanned.matched} 个文件，本次新发现 ${scanned.new_files} 个` : '尚未扫描'))}</span></div><div class="watch-card-controls"><input class="watch-path" type="text" aria-label="监控路径 ${index + 1}" value="${h(dir.path || '')}" placeholder="选择或输入完整监控路径"><button class="button small watch-browse" type="button">选择目录…</button><button class="button small watch-config" type="button">设置</button><button class="button small watch-remove" type="button">移除</button></div><div class="watch-card-summary">${summary}</div></article>`;
  };
  return {
    html: heading('设置', '按固定周期完整扫描监控目录，并自动创建整理任务。') +
      `<div class="settings-layout"><nav class="settings-nav"><a href="#/settings/common">整理</a><a class="active" href="#/settings/watch-dir">监控</a><a href="#/settings/download">下载</a><a href="#/settings/naming">命名</a><a href="#/settings/watermark">水印</a><a href="#/settings/metadata">元数据</a><a href="#/settings/nfo">NFO</a><a href="#/settings/emby">头像</a><a href="#/settings/system">系统</a><a href="#/settings/user">用户</a></nav><section class="panel watch-panel"><div class="download-section-title"><h2>监控设置</h2><p>按检查周期扫描已配置的目录，发现新视频后自动刮削。</p></div><div class="field"><label class="field-label" for="watch-enabled">启用目录监控<small>开启后定时扫描所有已配置目录并自动创建任务。</small></label><input id="watch-enabled" class="switch" type="checkbox" ${draft.watcher_enabled ? 'checked' : ''}></div><div class="field"><div class="field-label">扫描方式<small>按检查周期完整扫描目录及子目录，适用于本地磁盘和已挂载的网络共享。</small></div><strong class="watch-mode-label">定时完整扫描</strong></div><div class="field"><label class="field-label" for="watch-interval">检查周期（秒）<small>每隔多久完整扫描一次；范围 1～86400 秒，默认 30 秒。</small></label><input id="watch-interval" type="number" min="1" max="86400" step="1" value="${h(draft.watcher_poll_interval_seconds)}"></div><div class="download-section-title watch-list-heading"><h2>监控目录</h2><button id="watch-add" class="button" type="button">＋ 添加监控目录</button></div><div id="watch-list">${draft.watch_dirs.map(row).join('')}</div><div class="watch-status-bar"><span id="watch-status">${status.running ? '正在扫描…' : status.last_scan ? `上次扫描：${new Date(status.last_scan).toLocaleString()}` : '尚未扫描'}</span><button class="button small" id="watch-scan" type="button">立即扫描</button></div><div class="download-section-title"><h2>最近发现</h2><p>命中过滤规则的新视频会自动进入刮削任务；数据库已有成功记录的视频不会重复处理。</p></div><div class="watch-recent"><div id="watch-recent-items">${recentHTML(status.recent)}</div></div><div class="savebar"><span id="watch-save-status">修改后请保存</span><div><button class="button" id="watch-reset" type="button">撤销修改</button> <button class="button primary" id="watch-save" type="button">保存修改</button></div></div></section></div><dialog id="watch-config-dialog" class="watch-config-dialog"><div id="watch-config-content"></div></dialog><dialog id="watch-folder-dialog" class="folder-dialog"><div class="folder-dialog-head"><h2>选择文件夹</h2><button class="icon-button" id="watch-folder-close" type="button" aria-label="关闭">×</button></div><div class="folder-location"><button class="button small" id="watch-folder-up" type="button">↑ 上一级</button><input id="watch-folder-path" type="text" aria-label="目录路径" placeholder="完整目录路径"><button class="button small" id="watch-folder-go" type="button">转到</button></div><div id="watch-folder-list" class="folder-list" role="list"></div><p id="watch-folder-status" class="folder-status" role="status"></p><div class="folder-dialog-actions"><button class="button" id="watch-folder-cancel" type="button">取消</button><button class="button primary" id="watch-folder-select" type="button" disabled>选择此目录</button></div></dialog>`,
    bind() {
      const mark = () => {
        const changed = stableState(draft) !== savedState;
        markDirty(changed);
        $('#watch-save-status').textContent = changed ? '有尚未保存的修改' : '监控设置已保存';
        $('#watch-save-status').classList.toggle('unsaved', changed);
      };
      const list = $('#watch-list');
      const renderRows = () => { list.innerHTML = draft.watch_dirs.length ? draft.watch_dirs.map(row).join('') : '<div class="watch-empty">尚未添加监控目录。</div>'; bindRows(); };
      const picker = $('#watch-folder-dialog');
      let folderState = { path: '', parent: '' }, chooseFolder = null;
      const browse = async (path = '') => {
        $('#watch-folder-status').textContent = '正在读取文件夹…';
        $('#watch-folder-list').innerHTML = '';
        $('#watch-folder-select').disabled = true;
        try {
          folderState = await api('/api/system/folders?path=' + encodeURIComponent(path));
          $('#watch-folder-path').value = folderState.path;
          $('#watch-folder-up').disabled = !folderState.path;
          $('#watch-folder-select').disabled = !folderState.path;
          $('#watch-folder-list').innerHTML = folderState.folders.length ? folderState.folders.map((item) => `<button type="button" class="folder-item" data-path="${h(item.path)}"><span aria-hidden="true">▰</span>${h(item.name)}</button>`).join('') : '<div class="folder-empty">没有子文件夹</div>';
          $('#watch-folder-status').textContent = folderState.path || '请选择起始目录';
          $('#watch-folder-list').querySelectorAll('[data-path]').forEach((button) => button.onclick = () => browse(button.dataset.path));
        } catch (error) { $('#watch-folder-status').textContent = error.message; }
      };
      const openPicker = (path, callback) => { chooseFolder = callback; picker.showModal(); browse(path); };
      $('#watch-folder-up').onclick = () => browse(folderState.parent || '');
      $('#watch-folder-go').onclick = () => browse($('#watch-folder-path').value.trim());
      $('#watch-folder-path').onkeydown = (event) => { if (event.key === 'Enter') { event.preventDefault(); browse(event.target.value.trim()); } };
      $('#watch-folder-select').onclick = () => { chooseFolder?.(folderState.path); picker.close(); };
      $('#watch-folder-cancel').onclick = () => picker.close();
      $('#watch-folder-close').onclick = () => picker.close();

      const modal = $('#watch-config-dialog');
      const openConfig = (dir) => {
        const config = { ...copyCommon(global), ...structuredClone(dir.common || {}) };
        const content = $('#watch-config-content');
        content.innerHTML = `<div class="watch-modal-head"><div><h2>整理配置</h2><p>${h(dir.path || '新监控目录')}</p></div><button id="watch-config-close" class="icon-button" type="button" aria-label="关闭目录配置">×</button></div><div class="watch-modal-tabs"><span class="active">整理</span></div><div class="watch-modal-body"><div class="field"><label class="field-label" for="watch-use-global">使用全局整理配置<small>关闭后，此目录使用下面的独立设置。</small></label><input class="switch" id="watch-use-global" type="checkbox" ${dir.use_global_config ? 'checked' : ''}></div><div id="watch-override-fields"><div class="field"><label class="field-label" for="watch-target">整理目录<small>移动模式下的目标目录</small></label><div class="folder-control"><input id="watch-target" type="text" value="${h(config.target_folder || '')}" placeholder="请选择目录"><button id="watch-target-browse" class="button small" type="button">选择目录…</button></div></div><div class="field"><label class="field-label" for="watch-mode">整理模式</label><select id="watch-mode">${modes.map(([id, title]) => `<option value="${id}" ${Number(config.link_mode) === Number(id) ? 'selected' : ''}>${title}</option>`).join('')}</select></div><div class="field mode-option" id="watch-strm-option"><label class="field-label" for="watch-strm-from">STRM 路径映射（可选）<small>把 mdc-go 看到的源路径前缀替换为播放器看到的前缀。两项都留空则不映射。</small></label><div class="strm-map-row"><input id="watch-strm-from" type="text" value="${h(config.strm_path_from || '')}" placeholder="mdc-go 中的前缀，如 /data/av" spellcheck="false"><span aria-hidden="true">→</span><input id="watch-strm-to" type="text" value="${h(config.strm_path_to || '')}" placeholder="播放器中的前缀，如 /media/av" spellcheck="false"></div></div><div class="field mode-option" id="watch-move-option"><label class="field-label" for="watch-move-cleanup">移动后删除源文件夹<small>移动完成后，若原视频所在的源文件夹已没有任何文件，则删除该文件夹</small></label><input id="watch-move-cleanup" class="switch" type="checkbox" ${config.delete_empty_parent_after_move ? 'checked' : ''}></div><div class="field mode-option" id="watch-cleanup-meta-option"><label class="field-label" for="watch-cleanup-meta">刮削出错时删除元数据目录</label><input id="watch-cleanup-meta" class="switch" type="checkbox" ${config.delete_metadata_folder_when_failed ? 'checked' : ''}></div><div class="field mode-option" id="watch-overwrite-video-option"><label class="field-label" for="watch-overwrite-video">覆盖目标目录视频和字幕</label><input id="watch-overwrite-video" class="switch" type="checkbox" ${config.target_overwrite_video ? 'checked' : ''}></div><div class="field mode-option" id="watch-overwrite-pic-option"><label class="field-label" for="watch-overwrite-pic">覆盖目标目录图片</label><input id="watch-overwrite-pic" class="switch" type="checkbox" ${config.target_overwrite_pic ? 'checked' : ''}></div><div class="field"><label class="field-label" for="watch-size">文件大小过滤（MB）</label><input id="watch-size" type="number" min="0" step="any" value="${h(Number(config.file_size_filter || 0) / 1048576)}"></div><div class="field"><div class="field-label">文件类型白名单<small>仅识别列表中的视频后缀；扫描触发只看此项。</small></div>${tags('watch-extensions', config.file_ext_filter, '扩展名')}</div><div class="field"><div class="field-label">文件名黑名单</div>${tags('watch-blacklist', config.file_name_blacklist, '黑名单')}</div><div class="field"><div class="field-label">中文字幕关键词<small>纯英文字母或数字的关键词（如 C、CHS）须单独成段。</small></div>${tags('watch-subtitle', config.subtitle_keywords, '中文字幕关键词')}</div><div class="field"><div class="field-label">LTX 破解关键词</div>${tags('watch-ltx', config.ltx_hack_keywords, 'LTX 关键词')}</div><div class="field"><div class="field-label">LADA 破解关键词</div>${tags('watch-lada', config.lada_hack_keywords, 'LADA 关键词')}</div><div class="field"><div class="field-label">JAV 破解关键词</div>${tags('watch-jav', config.jav_hack_keywords, 'JAV 关键词')}</div><div class="field"><div class="field-label">其他破解关键词</div>${tags('watch-other-hack', config.other_hack_keywords, '其他破解关键词')}</div><div class="field"><div class="field-label">无码关键词</div>${tags('watch-uncensored', config.uncensored_keywords, '无码关键词')}</div></div></div><div class="watch-modal-actions"><button class="button" id="watch-config-cancel" type="button">关闭</button><button class="button primary" id="watch-config-save" type="button">保存目录配置</button></div>`;
        const body = $('.watch-modal-body', content);
        const commonPane = document.createElement('div');
        commonPane.id = 'watch-common-pane';
        while (body.firstChild) commonPane.append(body.firstChild);
        const downloadPane = document.createElement('div');
        downloadPane.id = 'watch-download-pane';
        const downloadConfig = { ...globalDownload, ...structuredClone(dir.download || {}) };
        downloadPane.innerHTML = `<div class="field"><label class="field-label" for="watch-use-global-download">使用全局下载配置<small>关闭后，此目录使用独立的图片和预告片设置。</small></label><input class="switch" id="watch-use-global-download" type="checkbox" ${dir.use_global_download !== false ? 'checked' : ''}></div><div id="watch-download-override">${renderDownloadFields(downloadConfig)}</div>`;
        const namingConfig = { ...structuredClone(globalNaming), ...structuredClone(dir.naming || {}) };
        const namingPane = document.createElement('div');
        namingPane.id = 'watch-naming-pane';
        namingPane.innerHTML = `<div class="field"><label class="field-label" for="watch-use-global-naming">使用全局命名配置<small>关闭后，此目录使用独立的目录、视频和图片命名规则。</small></label><input class="switch" id="watch-use-global-naming" type="checkbox" ${dir.use_global_naming !== false ? 'checked' : ''}></div><div id="watch-naming-override">${renderNamingFields(namingConfig)}</div>`;
        const watermarkConfig = { ...structuredClone(globalWatermark), ...structuredClone(dir.watermark || {}) };
        const watermarkPane = document.createElement('div');
        watermarkPane.id = 'watch-watermark-pane';
        watermarkPane.innerHTML = `<div class="field"><label class="field-label" for="watch-use-global-watermark">使用全局水印配置<small>关闭后，此目录使用独立的水印开关、样式和位置。</small></label><input class="switch" id="watch-use-global-watermark" type="checkbox" ${dir.use_global_watermark !== false ? 'checked' : ''}></div><div id="watch-watermark-override">${renderWatermarkFields(watermarkConfig)}</div>`;
        const nfoConfig = { ...structuredClone(globalNFO), ...structuredClone(dir.nfo || {}) };
        const nfoPane = document.createElement('div');
        nfoPane.id = 'watch-nfo-pane';
        nfoPane.innerHTML = `<div class="field"><label class="field-label" for="watch-use-global-nfo">使用全局 NFO 配置<small>关闭后，此目录使用独立的 NFO 字段和标签规则。</small></label><input class="switch" id="watch-use-global-nfo" type="checkbox" ${dir.use_global_nfo !== false ? 'checked' : ''}></div><div id="watch-nfo-override">${renderNFOFields(nfoConfig)}</div>`;
        const providerOverride = dir.providers || {};
        const providerConfigs = { ...structuredClone(globalProviderConfigs), ...structuredClone(providerOverride.configs || {}) };
        const providerPriorities = structuredClone(providerOverride.priorities || globalPriorities.config);
        const providersPane = document.createElement('div');
        providersPane.id = 'watch-providers-pane';
        providersPane.innerHTML = `<div class="field"><label class="field-label" for="watch-use-global-providers">使用全局数据源配置<small>关闭后，此目录使用独立的数据源启用状态、连接参数和字段优先级。</small></label><input class="switch" id="watch-use-global-providers" type="checkbox" ${dir.use_global_providers !== false ? 'checked' : ''}></div><div id="watch-providers-override"></div>`;
        body.append(commonPane, downloadPane, namingPane, watermarkPane, nfoPane, providersPane);
        const tabs = $('.watch-modal-tabs', content);
        tabs.innerHTML = '<button class="active" type="button" data-pane="common">整理</button><button type="button" data-pane="download">下载</button><button type="button" data-pane="naming">命名</button><button type="button" data-pane="watermark">水印</button><button type="button" data-pane="nfo">NFO</button><button type="button" data-pane="providers">数据源</button>';
        const showPane = (pane) => {
          commonPane.hidden = pane !== 'common';
          downloadPane.hidden = pane !== 'download';
          namingPane.hidden = pane !== 'naming';
          watermarkPane.hidden = pane !== 'watermark';
          nfoPane.hidden = pane !== 'nfo';
          providersPane.hidden = pane !== 'providers';
          const titles = { common: '整理配置', download: '下载配置', naming: '命名配置', watermark: '水印配置', nfo: 'NFO 配置', providers: '数据源配置' };
          $('.watch-modal-head h2', content).textContent = titles[pane];
          tabs.querySelectorAll('button').forEach((button) => button.classList.toggle('active', button.dataset.pane === pane));
        };
        tabs.querySelectorAll('button').forEach((button) => { button.onclick = () => showPane(button.dataset.pane); });
        showPane('common');
        const showDownloadFields = () => { $('#watch-download-override', content).hidden = $('#watch-use-global-download', content).checked; };
        $('#watch-use-global-download', content).onchange = showDownloadFields;
        showDownloadFields();
        const namingOverride = $('#watch-naming-override', content);
        const watermarkOverride = $('#watch-watermark-override', content);
        const nfoOverride = $('#watch-nfo-override', content);
        const showNamingFields = () => { namingOverride.hidden = $('#watch-use-global-naming', content).checked; };
        const showWatermarkFields = () => { watermarkOverride.hidden = $('#watch-use-global-watermark', content).checked; };
        const showNFOFields = () => { nfoOverride.hidden = $('#watch-use-global-nfo', content).checked; };
        const providersOverride = $('#watch-providers-override', content);
        const showProviderFields = () => { providersOverride.hidden = $('#watch-use-global-providers', content).checked; };
        $('#watch-use-global-naming', content).onchange = showNamingFields;
        $('#watch-use-global-watermark', content).onchange = showWatermarkFields;
        $('#watch-use-global-nfo', content).onchange = showNFOFields;
        $('#watch-use-global-providers', content).onchange = showProviderFields;
        showNamingFields(); showWatermarkFields(); showNFOFields(); showProviderFields();
        bindNamingFields(namingOverride, namingConfig, mark);
        bindWatermarkFields(watermarkOverride, watermarkConfig, mark);
        nfoOverride.addEventListener('input', mark);
        nfoOverride.addEventListener('change', mark);
        mountDirectoryProviders(providersOverride, providerList.data, providerConfigs, providerPriorities, globalPriorities.fields, mark);
        $('#watermark-choose-folder', content).onclick = () => openPicker($('#watermark-custom_folder', content).value, (path) => {
          const input = $('#watermark-custom_folder', content);
          input.value = path;
          input.dispatchEvent(new Event('input', { bubbles: true }));
        });
        const showFields = () => { $('#watch-override-fields', content).hidden = $('#watch-use-global', content).checked; };
        const showMode = () => { const mode = $('#watch-mode', content).value; $('#watch-move-option', content).hidden = mode !== '2'; $('#watch-cleanup-meta-option', content).hidden = mode !== '2'; $('#watch-strm-option', content).hidden = mode !== '4'; $('#watch-overwrite-video-option', content).hidden = mode === '3'; $('#watch-overwrite-pic-option', content).hidden = mode === '3'; };
        $('#watch-use-global', content).onchange = showFields;
        $('#watch-mode', content).onchange = showMode;
        showFields(); showMode();
        bindTags(content, toast);
        $('#watch-target-browse', content).onclick = () => openPicker($('#watch-target', content).value, (path) => { $('#watch-target', content).value = path; });
        $('#watch-config-close', content).onclick = () => modal.close();
        $('#watch-config-cancel', content).onclick = () => modal.close();
        $('#watch-config-save', content).onclick = () => {
          try {
            content.querySelectorAll('.tag-editor').forEach((editor) => { if (!$('.tag-entry', editor).hidden) editor.commit(); });
            const useGlobal = $('#watch-use-global', content).checked;
            const mode = Number($('#watch-mode', content).value);
            const target = $('#watch-target', content).value.trim();
            // 整理目录可以留空；开启目录监控后保存时由后端检查是否可用。
            if (!useGlobal && target && !isAbsolutePath(target)) throw Error(absolutePathHint('整理目录'));
            const size = Math.round(Number($('#watch-size', content).value) * 1048576);
            if (!Number.isSafeInteger(size) || size < 0) throw Error('文件大小过滤须为有效的非负数');
            dir.use_global_config = useGlobal;
            dir.common = {
              ...config, target_folder: target, link_mode: mode,
              strm_path_from: $('#watch-strm-from', content).value.trim(),
              strm_path_to: $('#watch-strm-to', content).value.trim(),
              delete_metadata_folder_when_failed: $('#watch-cleanup-meta', content).checked,
              delete_empty_parent_after_move: $('#watch-move-cleanup', content).checked,
              target_overwrite_video: $('#watch-overwrite-video', content).checked,
              target_overwrite_pic: $('#watch-overwrite-pic', content).checked,
              file_size_filter: size,
              file_ext_filter: readTags('watch-extensions'),
              file_name_blacklist: readTags('watch-blacklist'),
              jav_hack_keywords: readTags('watch-jav'),
              lada_hack_keywords: readTags('watch-lada'),
              ltx_hack_keywords: readTags('watch-ltx'),
              subtitle_keywords: readTags('watch-subtitle'),
              other_hack_keywords: readTags('watch-other-hack'),
              uncensored_keywords: readTags('watch-uncensored'),
            };
            dir.use_global_download = $('#watch-use-global-download', content).checked;
            dir.download = { ...downloadConfig, ...readDownloadFields(downloadPane) };
            dir.use_global_naming = $('#watch-use-global-naming', content).checked;
            dir.naming = readNamingFields(namingOverride, namingConfig);
            dir.use_global_watermark = $('#watch-use-global-watermark', content).checked;
            dir.watermark = readWatermarkFields(watermarkOverride, watermarkConfig, !dir.use_global_watermark);
            dir.use_global_nfo = $('#watch-use-global-nfo', content).checked;
            dir.nfo = readNFOFields(nfoOverride, nfoConfig);
            dir.use_global_providers = $('#watch-use-global-providers', content).checked;
            dir.providers = readDirectoryProviders(providersOverride);
            mark(); renderRows(); modal.close(); toast('目录配置已暂存，请保存监控设置');
          } catch (error) { toast(error.message, true); }
        };
        modal.showModal();
      };

      function bindRows() {
        list.querySelectorAll('.watch-card').forEach((card) => {
          const dir = draft.watch_dirs.find((item) => item.id === card.dataset.watchId);
          $('.watch-path', card).oninput = (event) => { dir.path = event.target.value; mark(); };
          $('.watch-browse', card).onclick = () => openPicker(dir.path, (path) => { dir.path = path; $('.watch-path', card).value = path; mark(); });
          $('.watch-config', card).onclick = () => openConfig(dir);
          $('.watch-remove', card).onclick = () => { draft.watch_dirs = draft.watch_dirs.filter((item) => item !== dir); mark(); renderRows(); };
        });
      }
      renderRows();
      $('#watch-enabled').onchange = (event) => { draft.watcher_enabled = event.target.checked; mark(); };
      $('#watch-interval').oninput = (event) => { draft.watcher_poll_interval_seconds = Number(event.target.value); mark(); };
      $('#watch-add').onclick = () => { draft.watch_dirs.push({ id: newDirectoryID(), path: '', enabled: true, use_global_config: false, common: copyCommon(global), use_global_download: true, download: structuredClone(globalDownload), use_global_naming: true, naming: structuredClone(globalNaming), use_global_watermark: true, watermark: structuredClone(globalWatermark), use_global_nfo: true, nfo: structuredClone(globalNFO), use_global_providers: true, providers: { configs: structuredClone(globalProviderConfigs), priorities: structuredClone(globalPriorities.config) } }); mark(); renderRows(); list.lastElementChild?.querySelector('.watch-path')?.focus(); };
      $('#watch-reset').onclick = () => { reload(); toast('已撤销未保存的修改'); };
      $('#watch-save').onclick = async () => {
        const button = $('#watch-save'); button.disabled = true;
        try {
          if (!Number.isInteger(draft.watcher_poll_interval_seconds) || draft.watcher_poll_interval_seconds < 1 || draft.watcher_poll_interval_seconds > 86400) throw Error('检查周期必须是 1～86400 秒');
          const paths = new Set();
          for (const dir of draft.watch_dirs) {
            dir.path = dir.path.trim();
            if (!isAbsolutePath(dir.path)) throw Error(absolutePathHint('监控目录'));
            // Windows 路径不区分大小写，Linux 区分。
            const trimmed = dir.path.replace(/[\\/]+$/, '') || dir.path;
            const key = isWindows() ? trimmed.toLowerCase() : trimmed;
            if (paths.has(key)) throw Error('监控目录不能重复');
            paths.add(key);
          }
          await api('/api/config/common', { method: 'PUT', body: draft });
          savedState = stableState(draft);
          clearDirty();
          $('#watch-save-status').textContent = '监控设置已保存';
          $('#watch-save-status').classList.remove('unsaved');
          toast('监控设置已保存');
          refreshStatus();
        } catch (error) { toast(error.message, true); }
        finally { button.disabled = false; }
      };
      $('#watch-scan').onclick = async () => { try { await api('/api/watcher/scan', { method: 'POST' }); toast('已请求扫描'); refreshStatus(); } catch (error) { toast(error.message, true); } };
      async function refreshStatus() {
        try {
          status = await api('/api/watcher/status');
          $('#watch-status').textContent = status.running ? '正在扫描…' : status.last_scan ? `上次扫描：${new Date(status.last_scan).toLocaleString()}${status.next_scan ? ` · 下次：${new Date(status.next_scan).toLocaleString()}` : ''}` : '尚未扫描';
          $('#watch-recent-items').innerHTML = recentHTML(status.recent);
          list.querySelectorAll('.watch-card').forEach((card) => {
            const scanned = status.directories?.find((item) => item.id === card.dataset.watchId);
            $('.watch-dir-status', card).textContent = scanned?.error || (scanned ? `匹配 ${scanned.matched} 个文件，本次新发现 ${scanned.new_files} 个` : '尚未扫描');
          });
        } catch { /* Service availability is shown by the main shell. */ }
      }
      // 扫描开始和结束时服务端会推送 watch-changed；重新连上后补拉一次。
      const unsubscribe = onJobEvents((kind) => {
        if (!document.contains(list)) { unsubscribe(); return; }
        if (kind === 'watch-changed' || kind === 'resync') refreshStatus();
      });
    },
  };
}

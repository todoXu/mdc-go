import { api, escapeHTML as h } from './api.js';

const nav = `<nav class="settings-nav"><a href="#/settings/common">整理</a><a href="#/settings/watch-dir">监控</a><a href="#/settings/download">下载</a><a href="#/settings/naming">命名</a><a href="#/settings/watermark">水印</a><a href="#/settings/metadata">元数据</a><a href="#/settings/nfo">NFO</a><a class="active" href="#/settings/emby">头像</a><a href="#/settings/system">系统</a><a href="#/settings/user">用户</a></nav>`;
const field = (id, label, note, value, extra = '') => `<div class="field"><label class="field-label" for="emby-${id}">${label}<small>${note}</small></label><input id="emby-${id}" type="text" value="${h(value ?? '')}" ${extra}></div>`;

export async function embySettings({ heading, toast, markDirty, clearDirty, reload }) {
  const config = await api('/api/config/actress');
  const emby = config.emby_config;
  const selectedLibraries = new Set(String(emby.library_ids || '').split(',').map((id) => id.trim()).filter(Boolean));
  return {
    html: heading('设置', '连接 Emby，配置新演员入库后的刮削。') + `<div class="settings-layout emby-settings">${nav}<form id="emby-form" class="panel">
      <div class="download-section-title"><h2>Emby 连接</h2></div>
      ${field('server', 'Emby 服务器地址', '以 http:// 或 https:// 开头，例如 http://192.168.1.10:8096/', emby.server_address, 'placeholder="http://192.168.1.10:8096/" spellcheck="false"')}
      <div class="field"><label class="field-label" for="emby-key">Emby API Key<small>在 Emby 管理后台的 API 密钥页面创建。</small></label><div class="system-secret"><input id="emby-key" type="password" value="${h(emby.api_key)}" autocomplete="off" spellcheck="false"><button id="emby-key-toggle" class="button small" type="button">显示</button></div></div>
      ${field('user', 'Emby User ID', '可选；填写后仅处理该用户可访问的演员。', emby.user_id, 'spellcheck="false"')}
      <div class="field"><div class="field-label">选择媒体库<small>先保存 Emby 连接信息，再读取可用媒体库；不选择表示全部媒体库。</small></div><div class="emby-library-picker"><div class="emby-library-actions"><button id="emby-load-libraries" class="button" type="button">读取媒体库</button><button id="emby-all-libraries" class="button" type="button" hidden>设为全部</button><span id="emby-library-status">${selectedLibraries.size ? `已选择 ${selectedLibraries.size} 个媒体库` : '全部媒体库'}</span></div><div id="emby-library-list" class="emby-library-list" role="group" aria-label="选择媒体库"></div></div></div>
      <div class="field"><label class="field-label" for="emby-refresh">刮削完成后自动扫描媒体库<small>刮削、重新整理或编辑元数据完成后，让 Emby 扫描上面选择的媒体库（不选则扫描全部）。批量刮削时等整批结束后只扫描一次。</small></label><input id="emby-refresh" class="switch" type="checkbox" ${emby.refresh_after_scrape ? 'checked' : ''}></div>
      <div class="download-section-title"><h2>自动刮削</h2></div>
      <div class="field"><label class="field-label" for="emby-auto">新演员入库时自动刮削<small>只为新出现的演员创建一次刮削任务；已入库演员不重复处理。当前仅保存配置，自动流程尚未接入。</small></label><input id="emby-auto" class="switch" type="checkbox" ${emby.auto_scrape_new_actors ? 'checked' : ''}></div>
      <div class="download-section-title"><h2>刮削内容</h2></div>
      <div class="field"><label class="field-label" for="emby-info">演员资料<small>获取姓名、简介等元数据。</small></label><input id="emby-info" class="switch" type="checkbox" ${config.scrape_info ? 'checked' : ''}></div>
      <div class="field"><label class="field-label" for="emby-pics">演员图片<small>获取演员头像等图片。</small></label><input id="emby-pics" class="switch" type="checkbox" ${config.scrape_pics ? 'checked' : ''}></div>
      <div class="field"><label class="field-label" for="emby-overwrite">元数据覆盖模式<small>演员已有资料时如何处理。</small></label><select id="emby-overwrite"><option value="0" ${config.overwrite_mode === 0 ? 'selected' : ''}>仅刮削缺失的元数据</option><option value="1" ${config.overwrite_mode === 1 ? 'selected' : ''}>覆盖文本字段</option><option value="2" ${config.overwrite_mode === 2 ? 'selected' : ''}>覆盖图片</option><option value="3" ${config.overwrite_mode === 3 ? 'selected' : ''}>覆盖全部</option></select></div>
      <div class="savebar"><span id="emby-save-status">修改后请保存</span><div><button id="emby-reset" class="button" type="button">撤销修改</button> <button class="button primary" type="submit">保存修改</button></div></div>
    </form></div>`,
    bind() {
      const form = document.querySelector('#emby-form');
      const libraryList = document.querySelector('#emby-library-list');
      const libraryStatus = document.querySelector('#emby-library-status');
      const allLibraries = document.querySelector('#emby-all-libraries');
      const updateLibraryStatus = () => { libraryStatus.textContent = selectedLibraries.size ? `已选择 ${selectedLibraries.size} 个媒体库` : '全部媒体库'; };
      form.oninput = form.onchange = () => { markDirty(); document.querySelector('#emby-save-status').textContent = '有尚未保存的修改'; };
      document.querySelector('#emby-load-libraries').onclick = async () => {
        if (document.querySelector('#emby-server').value.trim() !== (emby.server_address || '') || document.querySelector('#emby-key').value.trim() !== (emby.api_key || '') || document.querySelector('#emby-user').value.trim() !== (emby.user_id || '')) {
          toast('请先保存 Emby 连接信息，再读取媒体库', true); return;
        }
        const button = document.querySelector('#emby-load-libraries');
        button.disabled = true; libraryStatus.textContent = '正在读取媒体库…';
        try {
          const result = await api('/api/emby/libraries', { timeout: 12000 });
          libraryList.innerHTML = result.data.map((item) => `<label class="emby-library-option"><input type="checkbox" value="${h(item.id)}" ${selectedLibraries.has(item.id) ? 'checked' : ''}><span>${h(item.name)}${item.type ? `<small>${h(item.type)}</small>` : ''}</span></label>`).join('');
          allLibraries.hidden = !result.data.length;
          updateLibraryStatus();
          if (!result.data.length) libraryStatus.textContent = '没有找到可用媒体库';
        } catch (error) { libraryStatus.textContent = error.message; toast(error.message, true); }
        finally { button.disabled = false; }
      };
      libraryList.onchange = (event) => {
        const checkbox = event.target.closest('input[type="checkbox"]');
        if (!checkbox) return;
        if (checkbox.checked) selectedLibraries.add(checkbox.value); else selectedLibraries.delete(checkbox.value);
        updateLibraryStatus();
      };
      allLibraries.onclick = () => {
        selectedLibraries.clear();
        libraryList.querySelectorAll('input[type="checkbox"]').forEach((checkbox) => { checkbox.checked = false; });
        updateLibraryStatus(); markDirty(); document.querySelector('#emby-save-status').textContent = '有尚未保存的修改';
      };
      document.querySelector('#emby-key-toggle').onclick = () => {
        const input = document.querySelector('#emby-key');
        input.type = input.type === 'password' ? 'text' : 'password';
        document.querySelector('#emby-key-toggle').textContent = input.type === 'password' ? '显示' : '隐藏';
      };
      document.querySelector('#emby-reset').onclick = () => { clearDirty(); reload(); toast('已撤销未保存的修改'); };
      form.onsubmit = async (event) => {
        event.preventDefault();
        const server = document.querySelector('#emby-server').value.trim();
        const key = document.querySelector('#emby-key').value.trim();
        const auto = document.querySelector('#emby-auto').checked;
        if (server && !/^https?:\/\//i.test(server)) { toast('Emby 服务器地址须以 http:// 或 https:// 开头', true); return; }
        if (auto && (!server || !key)) { toast('启用自动刮削前请填写 Emby 服务器地址和 API Key', true); return; }
        const refresh = document.querySelector('#emby-refresh').checked;
        if (refresh && (!server || !key)) { toast('启用自动扫描媒体库前请填写 Emby 服务器地址和 API Key', true); return; }
        const body = {
          enabled: auto,
          scrape_info: document.querySelector('#emby-info').checked,
          scrape_pics: document.querySelector('#emby-pics').checked,
          overwrite_mode: Number(document.querySelector('#emby-overwrite').value),
          emby_config: {
            server_address: server,
            api_key: key,
            user_id: document.querySelector('#emby-user').value.trim() || null,
            library_ids: [...selectedLibraries].join(','),
            auto_scrape_new_actors: auto,
            refresh_after_scrape: refresh,
          },
        };
        try { await api('/api/config/actress', { method: 'PUT', body }); Object.assign(emby, body.emby_config); clearDirty(); document.querySelector('#emby-save-status').textContent = '头像设置已保存'; toast('头像设置已保存'); }
        catch (error) { toast(error.message, true); }
      };
    },
  };
}

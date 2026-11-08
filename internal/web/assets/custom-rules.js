// 设置 → 自定义规则：文件名命中关键词时，加后缀、写 NFO 标签、加水印。
import { api, escapeHTML as h } from './api.js';

const nav = `<nav class="settings-nav"><a href="#/settings/common">整理</a><a href="#/settings/watch-dir">监控</a><a href="#/settings/download">下载</a><a href="#/settings/naming">命名</a><a href="#/settings/watermark">系统水印</a><a class="active" href="#/settings/custom">自定义规则</a><a href="#/settings/metadata">元数据</a><a href="#/settings/nfo">NFO</a><a href="#/settings/emby">头像</a><a href="#/settings/system">系统</a><a href="#/settings/user">用户</a></nav>`;
const corners = [[5, '自动'], [0, '左上角'], [1, '右上角'], [2, '右下角'], [3, '左下角']];
const markLimit = 2 * 1024 * 1024;

// 与后端 splitKeywordTokens 一致：按 - _ . 空格 括号拆词，不区分大小写，中文不拆。
const separators = /[-_.\s()[\]【】（）]+/;
const tokens = (value) => String(value || '').toLowerCase().split(separators).filter(Boolean);
const tokenKey = (value) => tokens(value).join('-');
const containsSequence = (have, want) => {
  if (!want.length || want.length > have.length) return false;
  for (let i = 0; i + want.length <= have.length; i++) if (want.every((token, j) => have[i + j] === token)) return true;
  return false;
};
const conflicts = (a, b) => {
  const left = tokens(a), right = tokens(b);
  return Boolean(left.length && right.length) && (containsSequence(left, right) || containsSequence(right, left));
};
const sizeText = (bytes) => (bytes >= 1024 * 1024 ? `${(bytes / 1024 / 1024).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB`);
const blankRule = () => ({ keyword: '', suffix: '', position: 'after', tag: '', watermark: '', corner: 5 });
const markURL = (name, version) => `/api/custom-watermarks/${encodeURIComponent(name)}?v=${version}`;

// 返回规则的问题；没有问题返回空字符串。keyword 为 true 表示问题出在关键词上。
function ruleProblem(rules, index, groups, library) {
  const rule = rules[index];
  const key = tokenKey(rule.keyword);
  if (!key) return { keyword: true, text: '请填写关键词' };
  for (let i = 0; i < rules.length; i++) {
    if (i !== index && tokenKey(rules[i].keyword) === key) return { keyword: true, text: `与第 ${i + 1} 条规则重复` };
  }
  for (const group of groups) {
    for (const word of group.words) {
      if (tokenKey(word) === key) return { keyword: true, text: `与${group.name}关键词「${word}」重复` };
      if (conflicts(rule.keyword, word)) return { keyword: true, text: `会和${group.name}关键词「${word}」同时命中` };
    }
  }
  if (/[\\/:*?"<>|]/.test(rule.suffix)) return { field: 'suffix', text: '后缀不能包含 \\ / : * ? " < > |' };
  if (!rule.suffix.trim() && !rule.tag.trim() && !rule.watermark) return { field: 'suffix', text: '请至少设置后缀、NFO 标签或水印中的一项' };
  if (rule.watermark && !library.some((item) => item.name === rule.watermark)) return { field: 'watermark', text: '水印图片不存在，请重新选择' };
  return null;
}

export async function customRulesSettings({ heading, toast, markDirty, clearDirty, reload }) {
  const [config, keywordData, libraryData] = await Promise.all([
    api('/api/config/custom'),
    api('/api/custom-rules/keywords'),
    api('/api/custom-watermarks'),
  ]);
  const groups = keywordData.data || [];
  let library = libraryData.data || [];
  let saved = (config.rules || []).map((rule) => ({ ...blankRule(), ...rule }));
  let rules = structuredClone(saved);
  let savedEnabled = config.watermark_enabled !== false;
  let markEnabled = savedEnabled;
  let version = Date.now();

  return {
    html: heading('设置', '文件名命中自定义关键词时，添加后缀、NFO 标签和水印。') + `<div class="settings-layout custom-rules-settings">${nav}<form id="custom-form" class="panel" novalidate>
      <div class="download-section-title"><h2>自定义水印</h2><p>先在这里上传水印图片，再在规则里选择使用哪一张。图片保存在数据目录的 custom-watermarks 文件夹，与「系统水印」设置里的系统水印分开；上传和删除立即生效。</p></div>
      <div class="field"><label class="field-label" for="custom-mark-enabled">启用自定义水印<small>关闭后不再添加自定义规则的水印，后缀和 NFO 标签照常生效。与「系统水印」设置里的「启用系统水印」互相独立；系统水印设置里封面图、缩略图、背景图的勾选对两者都生效。</small></label><input id="custom-mark-enabled" class="switch" type="checkbox" ${markEnabled ? 'checked' : ''}></div>
      <div class="custom-library ${markEnabled ? '' : 'custom-disabled'}"><div class="custom-mark-grid" id="custom-mark-grid"></div><div class="custom-library-actions"><button class="button small" type="button" id="custom-upload">＋ 上传水印</button><span>支持 PNG（建议透明背景、宽高比约 2.5:1），可一次选择多张，单张不超过 2 MB；同名文件会被覆盖。</span></div><input type="file" id="custom-mark-file" accept="image/png" multiple hidden></div>
      <div class="download-section-title"><h2>规则列表</h2><p>关键词只匹配文件名（不含番号），需要完全匹配：按 - _ . 空格 括号拆分后整段相同，不区分大小写。一个文件命中几条规则就叠加几条；同一位置的多个后缀按列表顺序排列。NFO 标签需在 NFO 设置中启用「标签」；水印的缩放、偏移、间距和排列方式沿用「系统水印」设置。</p></div>
      <div class="custom-rule-table"><div class="custom-rule-head"><span>#</span><span>关键词</span><span>后缀</span><span>后缀位置</span><span>NFO 标签</span><span>水印图片</span><span>水印位置</span><span></span></div><div id="custom-rule-rows"></div><div class="custom-rule-add"><button class="button small" type="button" id="custom-add">＋ 添加规则</button></div></div>
      <div class="download-section-title"><h2>已有识别关键词</h2><p>来自「整理」设置（含各监控目录的独立整理配置）和分辨率识别，只读。自定义关键词不能与它们相同，也不能互相包含（例如 LADA 会和 -LADA-破解 同时命中同一个文件）。</p></div>
      <dl class="custom-system-words">${groups.map((group) => `<dt>${h(group.name)}</dt><dd>${group.words.length ? group.words.map((word) => `<span class="tag-chip">${h(word)}</span>`).join('') : '<span class="muted">未设置</span>'}${group.name === '分辨率' ? '' : ' <a href="#/settings/common">在整理设置中修改</a>'}</dd>`).join('')}</dl>
      <div class="download-section-title"><h2>命中预览</h2><p>输入一个文件名，按页面上的规则（包括尚未保存的修改）和当前的整理、命名、NFO、系统水印设置，查看番号后的后缀、NFO 标签和海报上的水印位置。</p></div>
      <div class="custom-preview"><div><div class="custom-preview-input"><label for="custom-preview-name">示例文件名</label><input id="custom-preview-name" type="text" value="IPZ-034-98T-LADA-破解-中文.mp4" spellcheck="false" autocomplete="off"></div><dl class="custom-preview-result"><dt>命中规则</dt><dd id="custom-pv-hit">—</dd><dt>生成文件名</dt><dd id="custom-pv-name">—</dd><dt>NFO 标签</dt><dd><div class="custom-preview-tags" id="custom-pv-tags"></div></dd></dl><div class="custom-legend"><span><i class="custom-legend-custom"></i>自定义规则</span><span><i class="custom-legend-system"></i>系统识别</span></div></div><div><div class="custom-poster" id="custom-pv-poster"></div><div class="custom-poster-note" id="custom-pv-note">海报水印示意，比例和间距为近似值。</div></div></div>
      <div class="savebar"><span id="custom-save-status">修改后请保存</span><div><button id="custom-reset" class="button" type="button">撤销修改</button> <button id="custom-save" class="button primary" type="submit">保存设置</button></div></div>
    </form></div>`,
    bind() {
      const $ = (selector) => document.querySelector(selector);
      const form = $('#custom-form');
      const rows = $('#custom-rule-rows');
      let previewTimer = null;
      let previewToken = 0;

      const problems = () => rules.map((_, i) => ruleProblem(rules, i, groups, library));
      const syncState = () => {
        const list = problems();
        const bad = list.some(Boolean);
        const changed = markEnabled !== savedEnabled || JSON.stringify(rules) !== JSON.stringify(saved);
        markDirty(changed);
        $('#custom-save').disabled = bad;
        $('#custom-save-status').textContent = bad ? '有规则需要修正，修正后才能保存' : changed ? '有尚未保存的修改' : '修改后请保存';
        document.querySelectorAll('.custom-rule-row').forEach((row) => {
          const problem = list[Number(row.dataset.index)];
          row.querySelector('[data-k="keyword"]').classList.toggle('invalid', Boolean(problem?.keyword));
          row.querySelector('[data-k="suffix"]').classList.toggle('invalid', problem?.field === 'suffix');
          row.querySelector('[data-k="watermark"]').classList.toggle('invalid', problem?.field === 'watermark');
          row.querySelector('.custom-rule-error').textContent = problem?.text || '';
        });
        schedulePreview();
      };

      const rowHTML = (rule, i) => `<div class="custom-rule-row" data-index="${i}">
        <span class="custom-rule-index">${i + 1}</span>
        <div class="custom-rule-cell"><input data-k="keyword" value="${h(rule.keyword)}" placeholder="例如 98T" maxlength="64" spellcheck="false" aria-label="第 ${i + 1} 条规则的关键词"><div class="custom-rule-error"></div></div>
        <input data-k="suffix" value="${h(rule.suffix)}" placeholder="例如 -98T" maxlength="64" spellcheck="false" aria-label="后缀">
        <div class="custom-position" role="group" aria-label="后缀位置"><button type="button" data-pos="before" class="${rule.position === 'before' ? 'active' : ''}">系统后缀前</button><button type="button" data-pos="after" class="${rule.position === 'after' ? 'active' : ''}">系统后缀后</button></div>
        <input data-k="tag" value="${h(rule.tag)}" placeholder="留空则不写入" maxlength="64" spellcheck="false" aria-label="NFO 标签">
        <div class="custom-mark-pick"><span class="custom-mark-mini ${rule.watermark ? 'custom-checker' : ''}">${rule.watermark && library.some((item) => item.name === rule.watermark) ? `<img src="${markURL(rule.watermark, version)}" alt="">` : '—'}</span><select data-k="watermark" aria-label="水印图片"><option value="">不添加水印</option>${library.map((item) => `<option value="${h(item.name)}" ${item.name === rule.watermark ? 'selected' : ''}>${h(item.name)}</option>`).join('')}${rule.watermark && !library.some((item) => item.name === rule.watermark) ? `<option value="${h(rule.watermark)}" selected>${h(rule.watermark)}（已删除）</option>` : ''}</select></div>
        <select data-k="corner" ${rule.watermark ? '' : 'disabled'} aria-label="水印位置">${corners.map(([value, name]) => `<option value="${value}" ${Number(rule.corner) === value ? 'selected' : ''}>${name}</option>`).join('')}</select>
        <button class="icon-button" type="button" data-remove aria-label="删除第 ${i + 1} 条规则" title="删除规则">×</button>
      </div>`;
      const renderRows = () => {
        rows.innerHTML = rules.length ? rules.map(rowHTML).join('') : '<div class="custom-rule-empty">还没有自定义规则，点击下方「添加规则」开始。</div>';
        syncState();
      };
      const renderLibrary = () => {
        $('#custom-mark-grid').innerHTML = library.length
          ? library.map((item) => {
              const used = rules.filter((rule) => rule.watermark === item.name).length;
              return `<div class="custom-mark-card" data-mark="${h(item.name)}"><div class="custom-mark-image custom-checker"><img src="${markURL(item.name, version)}" alt="${h(item.name)}"></div><div class="custom-mark-info"><div class="custom-mark-name" title="${h(item.name)}">${h(item.name)}</div><div class="custom-mark-meta">${item.width}×${item.height} · ${sizeText(item.size)} · ${used ? `${used} 条规则使用` : '未使用'}</div></div><button type="button" class="custom-mark-delete" data-delete-mark aria-label="删除 ${h(item.name)}" title="删除">×</button></div>`;
            }).join('')
          : '<div class="custom-mark-empty">还没有上传水印图片</div>';
      };

      const renderPreview = (result) => {
        const muted = (text) => `<span class="muted">${h(text)}</span>`;
        $('#custom-pv-hit').innerHTML = result.matched.length ? result.matched.map((word) => `<code>${h(word)}</code>`).join(' ') : muted('没有命中自定义规则');
        const name = $('#custom-preview-name').value.trim();
        const ext = (name.match(/\.[a-z0-9]+$/i) || [''])[0];
        const { before, system, after } = result.suffix;
        $('#custom-pv-name').innerHTML = result.number
          ? `<code>${h(result.number)}<span class="custom-seg-custom">${h(before)}</span><span class="custom-seg-system">${h(system)}</span><span class="custom-seg-custom">${h(after)}</span>${h(ext)}</code>`
          : muted('无法识别番号');
        $('#custom-pv-tags').innerHTML = result.tags.length
          ? result.tags.map((entry) => `<span class="tag-chip ${entry.origin === 'custom' ? 'custom' : ''}">${h(entry.tag)}</span>`).join('')
          : muted('—');
        const poster = $('#custom-pv-poster');
        const mode = result.watermark_mode || 'Stack';
        const used = {};
        poster.innerHTML = result.marks.map((mark) => {
          const corner = mark.corner;
          const index = used[corner] = (used[corner] || 0) + 1;
          const horizontal = mode === 'Stack' || (mode === 'Clockwise' && (corner === 0 || corner === 2)) || (mode === 'CounterClockwise' && (corner === 1 || corner === 3));
          const right = corner === 1 || corner === 2, bottom = corner === 2 || corner === 3;
          const x = 8 + (horizontal ? (index - 1) * 70 : 0), y = 8 + (horizontal ? 0 : (index - 1) * 30);
          const src = mark.custom ? markURL(mark.file, version) : `/watermarks/${encodeURIComponent(mark.file)}?v=rect2`;
          return `<img class="custom-poster-mark" src="${src}" alt="" style="${right ? 'right' : 'left'}:${x}px;${bottom ? 'bottom' : 'top'}:${y}px">`;
        }).join('');
        const systemOn = result.system_watermark_enabled, customOn = result.custom_watermark_enabled;
        $('#custom-pv-note').textContent = !result.poster_enabled ? '「系统水印」设置中未勾选封面图，海报不会添加水印。'
          : !systemOn && !customOn ? '系统水印和自定义水印都未启用，海报不会添加水印。'
          : !customOn ? '自定义水印已关闭，只显示系统水印。比例和间距为近似值。'
          : !systemOn ? '「系统水印」设置中未启用系统水印，只显示自定义水印。比例和间距为近似值。'
          : '海报水印示意，比例和间距为近似值。';
      };
      const schedulePreview = () => {
        clearTimeout(previewTimer);
        previewTimer = setTimeout(async () => {
          const name = $('#custom-preview-name').value.trim();
          if (!name) return;
          const token = ++previewToken;
          const list = problems();
          // 只用没有问题的规则做预览，避免一条填写中的规则让整个预览失败。
          const valid = rules.filter((_, i) => !list[i]).map((rule) => ({ ...rule, corner: Number(rule.corner) }));
          try {
            const result = await api('/api/custom-rules/preview', { method: 'POST', body: { name, rules: valid, watermark_enabled: markEnabled } });
            if (token === previewToken && document.contains(form)) renderPreview(result);
          } catch (error) {
            if (token === previewToken) $('#custom-pv-hit').innerHTML = `<span class="custom-error-text">${h(error.message)}</span>`;
          }
        }, 250);
      };

      rows.addEventListener('input', (event) => {
        const row = event.target.closest('.custom-rule-row');
        const key = event.target.dataset.k;
        if (!row || !key || key === 'watermark' || key === 'corner') return;
        rules[Number(row.dataset.index)][key] = event.target.value;
        syncState();
      });
      rows.addEventListener('change', (event) => {
        const row = event.target.closest('.custom-rule-row');
        if (!row) return;
        const i = Number(row.dataset.index);
        if (event.target.dataset.k === 'corner') { rules[i].corner = Number(event.target.value); syncState(); }
        if (event.target.dataset.k === 'watermark') { rules[i].watermark = event.target.value; renderRows(); renderLibrary(); }
      });
      rows.addEventListener('click', (event) => {
        const row = event.target.closest('.custom-rule-row');
        if (!row) return;
        const i = Number(row.dataset.index);
        if (event.target.dataset.pos) { rules[i].position = event.target.dataset.pos; renderRows(); }
        else if (event.target.closest('[data-remove]')) { rules.splice(i, 1); renderRows(); renderLibrary(); }
      });
      $('#custom-add').onclick = () => {
        rules.push(blankRule());
        renderRows();
        rows.querySelector('.custom-rule-row:last-child [data-k="keyword"]')?.focus();
      };

      const refreshLibrary = async () => {
        library = (await api('/api/custom-watermarks')).data || [];
        version = Date.now();
        renderLibrary();
        renderRows();
      };
      $('#custom-upload').onclick = () => $('#custom-mark-file').click();
      $('#custom-mark-file').onchange = async (event) => {
        const files = [...event.target.files];
        event.target.value = '';
        const failed = [];
        let uploaded = 0;
        for (const file of files) {
          if (file.type !== 'image/png' && !/\.png$/i.test(file.name)) { failed.push(`${file.name}：只支持 PNG`); continue; }
          if (file.size > markLimit) { failed.push(`${file.name}：超过 2 MB`); continue; }
          try {
            const response = await fetch(`/api/custom-watermarks/${encodeURIComponent(file.name)}`, { method: 'PUT', headers: { 'Content-Type': 'image/png' }, body: file, signal: AbortSignal.timeout(30000) });
            const result = await response.json().catch(() => ({}));
            if (!response.ok) throw Error(result.message || `HTTP ${response.status}`);
            uploaded++;
          } catch (error) {
            failed.push(`${file.name}：${error.message}`);
          }
        }
        try { await refreshLibrary(); } catch (error) { toast(error.message, true); }
        if (failed.length) toast(`${uploaded ? `已上传 ${uploaded} 张；` : ''}以下文件没有上传：${failed.join('；')}`, true);
        else if (uploaded) toast(`已上传 ${uploaded} 张水印`);
      };
      $('#custom-mark-grid').addEventListener('click', async (event) => {
        if (!event.target.closest('[data-delete-mark]')) return;
        const name = event.target.closest('.custom-mark-card').dataset.mark;
        const savedUse = saved.find((rule) => rule.watermark === name);
        if (savedUse) { toast(`「${name}」正被已保存的规则「${savedUse.keyword}」使用，请先在规则中改选并保存，再删除`, true); return; }
        const used = rules.filter((rule) => rule.watermark === name).length;
        if (!window.confirm(used ? `「${name}」正被 ${used} 条未保存的规则使用，删除后这些规则将不再添加水印。确定删除？` : `确定删除水印「${name}」？`)) return;
        try {
          await api(`/api/custom-watermarks/${encodeURIComponent(name)}`, { method: 'DELETE' });
          rules.forEach((rule) => { if (rule.watermark === name) rule.watermark = ''; });
          await refreshLibrary();
          toast(`已删除水印「${name}」`);
        } catch (error) {
          toast(error.message, true);
        }
      });

      $('#custom-mark-enabled').addEventListener('change', (event) => {
        markEnabled = event.target.checked;
        document.querySelector('.custom-library').classList.toggle('custom-disabled', !markEnabled);
        syncState();
      });
      $('#custom-preview-name').addEventListener('input', schedulePreview);
      $('#custom-reset').onclick = () => { clearDirty(); reload(); toast('已撤销未保存的修改'); };
      form.onsubmit = async (event) => {
        event.preventDefault();
        if (problems().some(Boolean)) { toast('有规则需要修正，修正后才能保存', true); return; }
        const button = $('#custom-save');
        button.disabled = true;
        try {
          const body = { watermark_enabled: markEnabled, rules: rules.map((rule) => ({ keyword: rule.keyword.trim(), suffix: rule.suffix.trim(), position: rule.position, tag: rule.tag.trim(), watermark: rule.watermark, corner: Number(rule.corner) })) };
          await api('/api/config/custom', { method: 'PUT', body });
          const next = await api('/api/config/custom');
          savedEnabled = next.watermark_enabled !== false;
          markEnabled = savedEnabled;
          saved = (next.rules || []).map((rule) => ({ ...blankRule(), ...rule }));
          rules = structuredClone(saved);
          clearDirty();
          renderRows();
          renderLibrary();
          $('#custom-save-status').textContent = '自定义规则已保存';
          toast('自定义规则已保存');
        } catch (error) {
          toast(error.message, true);
        } finally {
          button.disabled = problems().some(Boolean);
        }
      };

      renderLibrary();
      renderRows();
    },
  };
}

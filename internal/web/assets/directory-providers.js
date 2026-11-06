import { escapeHTML as h } from './api.js';
import { providerStyle } from './provider-color.js';

const fieldNames = {
  publish_number: '发行码', title: '标题', original_title: '原始标题', cover: '封面', poster: '海报',
  release: '发行日期', runtime: '时长', director: '导演', studio: '制作商', publisher: '发行商',
  series: '系列', actors: '演员', tags: '标签', samples: '剧照', trailer: '预告片', outline: '简介', rating: '评分',
};

const clone = (value) => structuredClone(value ?? {});

function providerConfigHTML(provider, config) {
  const id = h(provider.id);
  return `<article class="directory-provider" data-provider-id="${id}">
    <label class="directory-provider-head"><input type="checkbox" data-provider-enabled="${id}" ${config.enabled ? 'checked' : ''}><b>${h(provider.name || provider.id)}</b><small>${h(provider.id)}</small></label>
    <details><summary>连接参数</summary><div class="directory-provider-fields">
      <label>站点地址<input type="url" data-provider-field="base_url" value="${h(config.base_url || provider.base_url || '')}"></label>
      <label>Cookies<textarea rows="2" data-provider-field="cookies" spellcheck="false">${h(config.cookies || '')}</textarea></label>
      <label>User-Agent<input type="text" data-provider-field="user_agent" value="${h(config.user_agent || '')}"></label>
      <label>代理<input type="text" data-provider-field="proxy" value="${h(config.proxy || '')}"></label>
      <label>请求冷却（秒）<input type="number" min="0" step="1" data-provider-field="cooldown_seconds" value="${h(config.cooldown_seconds || 0)}"></label>
      <label>请求超时（秒）<input type="number" min="0" step="1" data-provider-field="request_timeout" value="${h(config.request_timeout || 0)}"></label>
      <label>预告片超时（秒）<input type="number" min="0" step="1" data-provider-field="trailer_timeout" value="${h(config.trailer_timeout || 0)}"></label>
      <label>失败重复次数<input type="number" min="0" max="5" step="1" data-provider-field="retry_times" value="${h(config.retry_times || 0)}"></label>
    </div></details>
  </article>`;
}

function priorityHTML(field, order, providers, configs, fields) {
  const names = (id) => providers.find((provider) => provider.id === id)?.name || id;
  const enabled = (id) => configs[id]?.enabled;
  return `<div class="directory-priority-row"><div><b>${h(fieldNames[field] || field)}</b><small>从左到右，优先级依次降低</small></div><div class="directory-priority-list" data-priority-field="${h(field)}">${order.length ? order.map((id, index) => `<span class="directory-priority-chip provider-colored" style="${providerStyle(id)}"><span>${index + 1}. ${h(names(id))}${enabled(id) ? '' : ' · 未启用'}</span><button type="button" data-priority-action="up" data-field="${h(field)}" data-index="${index}" ${index === 0 ? 'disabled' : ''}>←</button><button type="button" data-priority-action="down" data-field="${h(field)}" data-index="${index}" ${index === order.length - 1 ? 'disabled' : ''}>→</button><button type="button" data-priority-action="remove" data-field="${h(field)}" data-index="${index}">×</button></span>`).join('') : '<span class="directory-priority-empty">未配置，不抓取此字段</span>'}<select data-priority-add="${h(field)}"><option value="">＋ 添加数据源</option>${providers.filter((provider) => !order.includes(provider.id)).map((provider) => `<option value="${h(provider.id)}">${h(provider.name || provider.id)}${configs[provider.id]?.enabled ? '' : ' · 未启用'}</option>`).join('')}</select></div></div>`;
}

export function mountDirectoryProviders(container, providers, configs, priorities, fields, onChange) {
  const state = { configs: clone(configs), priorities: clone(priorities), fields: [...fields] };
  state.priorities.by_fields ||= {};
  const priorityHost = () => container.querySelector('#directory-priority-fields');
  const renderPriorities = () => {
    priorityHost().innerHTML = state.fields.map((field) => priorityHTML(field, state.priorities.by_fields[field] || [], providers, state.configs, state.fields)).join('');
  };
  container.innerHTML = `<div class="download-section-title"><h2>数据源连接</h2><p>关闭使用全局数据源后，此目录可以独立设置数据源启用状态、连接参数和字段优先级。</p></div>
    <div class="directory-provider-list">${providers.map((provider) => providerConfigHTML(provider, state.configs[provider.id] || {})).join('')}</div>
    <div class="download-section-title"><h2>字段优先级</h2><p>只使用本目录已启用的数据源。</p></div><div id="directory-priority-fields"></div>`;
  renderPriorities();

  container.addEventListener('input', (event) => {
    const article = event.target.closest('[data-provider-id]');
    if (!article) return;
    const id = article.dataset.providerId;
    state.configs[id] ||= {};
    if (event.target.matches('[data-provider-enabled]')) {
      state.configs[id].enabled = event.target.checked;
      renderPriorities();
    }
    if (event.target.dataset.providerField) {
      const key = event.target.dataset.providerField;
      state.configs[id][key] = event.target.type === 'number' ? Number(event.target.value) : event.target.value;
    }
    onChange();
  });
  container.addEventListener('change', (event) => {
    const add = event.target.closest('[data-priority-add]');
    if (!add || !add.value) return;
    const field = add.dataset.priorityAdd;
    state.priorities.by_fields[field] ||= [];
    state.priorities.by_fields[field].push(add.value);
    renderPriorities();
    onChange();
  });
  container.addEventListener('click', (event) => {
    const button = event.target.closest('[data-priority-action]');
    if (!button) return;
    const field = button.dataset.field;
    const order = state.priorities.by_fields[field] ||= [];
    const index = Number(button.dataset.index);
    if (button.dataset.priorityAction === 'remove') order.splice(index, 1);
    if (button.dataset.priorityAction === 'up' && index > 0) [order[index - 1], order[index]] = [order[index], order[index - 1]];
    if (button.dataset.priorityAction === 'down' && index < order.length - 1) [order[index + 1], order[index]] = [order[index], order[index + 1]];
    renderPriorities();
    onChange();
  });
  container.readDirectoryProviders = () => {
    // 冷却、超时、重复次数都不能为负数。
    for (const input of container.querySelectorAll('input[type="number"][data-provider-field]')) {
      const value = Number(input.value);
      if (input.value.trim() === '' || !Number.isFinite(value)) throw Error('数据源的数字设置须为有效数字');
      if (value < 0) throw Error('数据源的冷却、超时和重复次数不能小于 0');
    }
    return clone(state);
  };
}

export function readDirectoryProviders(container) {
  return container.readDirectoryProviders ? container.readDirectoryProviders() : null;
}

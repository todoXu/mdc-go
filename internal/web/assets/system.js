import { api, escapeHTML as h } from './api.js';

const nav = `<nav class="settings-nav"><a href="#/settings/common">整理</a><a href="#/settings/watch-dir">监控</a><a href="#/settings/download">下载</a><a href="#/settings/naming">命名</a><a href="#/settings/watermark">水印</a><a href="#/settings/metadata">元数据</a><a href="#/settings/nfo">NFO</a><a href="#/settings/emby">头像</a><a class="active" href="#/settings/system">系统</a><a href="#/settings/user">用户</a></nav>`;
const field = (key, label, note, type, value, extra = '') => `<div class="field"><label class="field-label" for="system-${key}">${label}<small>${note}</small></label><input id="system-${key}" type="${type}" value="${h(value)}" ${extra}></div>`;
// 密钥类输入框：默认隐藏内容，带「显示 / 隐藏」按钮。
const secret = (key, label, note, value, placeholder) => `<div class="field"><label class="field-label" for="system-${key}">${label}<small>${note}</small></label><div class="system-secret"><input id="system-${key}" type="password" value="${h(value ?? '')}" placeholder="${h(placeholder)}" autocomplete="off" spellcheck="false"><button class="button small" type="button" data-secret-toggle="system-${key}">显示</button></div></div>`;
const fileSize = (bytes) => {
  const value = Number(bytes) || 0;
  if (value < 1024) return `${value} B`;
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / 1024 ** 2).toFixed(1)} MB`;
};

export async function systemSettings({ heading, toast, markDirty, clearDirty, reload }) {
  const [config, database] = await Promise.all([api('/api/config/system'), api('/api/system/database')]);
  return {
    html: heading('设置', '配置任务运行参数、OpenAI 接口和第三方服务令牌。') + `<div class="settings-layout system-settings">${nav}<form id="system-form" class="panel">
      <div class="download-section-title"><h2>任务执行</h2></div>
      ${field('scrape_thread_count', '刮削任务线程数', '同时执行刮削任务的工作线程数量。', 'number', config.scrape_thread_count, 'min="1" max="128" step="1" required')}
      ${field('scrape_timeout', '刮削任务超时（秒）', '单个刮削任务允许的最长运行时间。', 'number', config.scrape_timeout, 'min="1" max="86400" step="1" required')}
      <div class="download-section-title"><h2>OpenAI 相关配置</h2></div>
      ${secret('openai_key', 'API Key', '密钥会保存到本机配置文件；当前接口测试和自动调用尚未接入。', config.openai_key, '')}
      ${field('openai_base', 'Base URL', '兼容 OpenAI API 的接口地址。', 'url', config.openai_base, 'placeholder="https://api.example.com" spellcheck="false"')}
      ${field('openai_model', 'Model', '模型名称，例如 gpt-4.1。', 'text', config.openai_model, 'spellcheck="false"')}
      <div class="system-note">测试连接将在 OpenAI 调用功能接入后启用。</div>
      <div class="download-section-title"><h2>第三方服务</h2><p>令牌只保存在本机配置文件，仅发送给对应的服务。</p></div>
      ${secret('github_token', 'GitHub Token', '用于更新自定义演员映射表，以及下载 gfriends 头像和 Jalbum 写真。可以访问私有仓库，也不容易触发 GitHub 匿名访问的频率限制；只需要仓库的只读权限。不填则匿名访问。', config.github_token, 'ghp_… 或 github_pat_…')}
      <div class="download-section-title"><h2>任务数据库</h2><p>刮削记录、执行状态、元数据和日志持久化在本地 SQLite 数据库。</p></div>
      <div class="system-list database-status">
        <div><span>当前状态</span><b class="system-storage">SQLite 已启用</b></div>
        <div><span>数据库文件</span><code class="database-path">${h(database.path)}</code></div>
        <div><span>记录数量</span><b>${h(database.record_count)} 条</b></div>
        <div><span>文件大小</span><b>${h(fileSize(database.size_bytes))}</b></div>
      </div>
      <div class="savebar"><span id="system-save-status">修改后请保存</span><div><button id="system-reset" class="button" type="button">撤销修改</button> <button class="button primary" type="submit">保存修改</button></div></div>
    </form></div>`,
    bind() {
      const form = document.querySelector('#system-form');
      form.oninput = () => { markDirty(); document.querySelector('#system-save-status').textContent = '有尚未保存的修改'; };
      form.querySelectorAll('[data-secret-toggle]').forEach((button) => {
        button.onclick = () => {
          const input = document.getElementById(button.dataset.secretToggle);
          input.type = input.type === 'password' ? 'text' : 'password';
          button.textContent = input.type === 'password' ? '显示' : '隐藏';
        };
      });
      document.querySelector('#system-reset').onclick = () => { clearDirty(); reload(); toast('已撤销未保存的修改'); };
      form.onsubmit = async (event) => {
        event.preventDefault();
        const next = {
          scrape_thread_count: Number(document.querySelector('#system-scrape_thread_count').value),
          scrape_timeout: Number(document.querySelector('#system-scrape_timeout').value),
          openai_key: document.querySelector('#system-openai_key').value,
          openai_base: document.querySelector('#system-openai_base').value.trim(),
          openai_model: document.querySelector('#system-openai_model').value.trim(),
          github_token: document.querySelector('#system-github_token').value.trim(),
        };
        try { await api('/api/config/system', { method: 'PUT', body: next }); clearDirty(); document.querySelector('#system-save-status').textContent = '系统设置已保存'; toast('系统设置已保存'); }
        catch (error) { toast(error.message, true); }
      };
    },
  };
}

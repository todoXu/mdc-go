import { api, escapeHTML as h } from './api.js';

const nav = `<nav class="settings-nav"><a href="#/settings/common">整理</a><a href="#/settings/watch-dir">监控</a><a href="#/settings/download">下载</a><a href="#/settings/naming">命名</a><a href="#/settings/watermark">水印</a><a href="#/settings/metadata">元数据</a><a href="#/settings/nfo">NFO</a><a href="#/settings/emby">头像</a><a href="#/settings/system">系统</a><a class="active" href="#/settings/user">用户</a></nav>`;

export function applyUserProfile(profile) {
  const badge = document.querySelector('#profile-avatar');
  if (!badge) return;
  badge.title = profile.username;
  badge.innerHTML = profile.avatar ? `<img src="${h(profile.avatar)}" alt="">` : h(profile.username.trim().slice(0, 1) || '用');
}

export async function userSettings({ heading, toast, markDirty, clearDirty, reload }) {
  const config = await api('/api/config/user');
  let avatar = config.avatar || '';
  return {
    html: heading('设置', '管理本机显示的用户资料。') + `<div class="settings-layout user-settings">${nav}<form id="user-form" class="panel">
      <div class="download-section-title"><h2>用户资料</h2></div>
      <div class="field"><div class="field-label">头像<small>支持 JPG、PNG、WebP，最大 1 MB。</small></div><div class="user-avatar-controls"><img id="user-avatar-preview" alt="用户头像预览" ${avatar ? `src="${h(avatar)}"` : 'hidden'}><span id="user-avatar-placeholder" ${avatar ? 'hidden' : ''}>用户</span><input id="user-avatar-file" type="file" accept="image/jpeg,image/png,image/webp" hidden><button id="user-avatar-select" class="button" type="button">选择头像</button><button id="user-avatar-remove" class="button" type="button" ${avatar ? '' : 'disabled'}>移除头像</button></div></div>
      <div class="field"><label class="field-label" for="user-name">用户名<small>1～64 个字符，仅用于本机页面显示。</small></label><input id="user-name" type="text" maxlength="64" value="${h(config.username)}" required autocomplete="off"></div>
      <div class="download-section-title"><h2>修改密码</h2><p>账号登录尚未接入，当前不能设置或修改密码。</p></div>
      <div class="field"><label class="field-label" for="user-current-password">当前密码</label><input id="user-current-password" type="password" disabled></div>
      <div class="field"><label class="field-label" for="user-new-password">新密码</label><input id="user-new-password" type="password" disabled></div>
      <div class="field"><label class="field-label" for="user-confirm-password">确认新密码</label><input id="user-confirm-password" type="password" disabled></div>
      <div class="savebar"><span id="user-save-status">修改后请保存</span><div><button id="user-reset" class="button" type="button">撤销修改</button> <button class="button primary" type="submit">保存修改</button></div></div>
    </form></div>`,
    bind() {
      const form = document.querySelector('#user-form');
      const file = document.querySelector('#user-avatar-file');
      const preview = document.querySelector('#user-avatar-preview');
      const placeholder = document.querySelector('#user-avatar-placeholder');
      const remove = document.querySelector('#user-avatar-remove');
      const changed = () => { markDirty(); document.querySelector('#user-save-status').textContent = '有尚未保存的修改'; };
      const showAvatar = () => { preview.hidden = !avatar; placeholder.hidden = !!avatar; remove.disabled = !avatar; if (avatar) preview.src = avatar; else preview.removeAttribute('src'); };
      form.oninput = changed;
      document.querySelector('#user-avatar-select').onclick = () => file.click();
      remove.onclick = () => { avatar = ''; showAvatar(); changed(); };
      file.onchange = async () => {
        const image = file.files?.[0];
        if (!image) return;
        if (!['image/jpeg', 'image/png', 'image/webp'].includes(image.type) || image.size > 1024 * 1024) { toast('请选择不超过 1 MB 的 JPG、PNG 或 WebP 图片', true); file.value = ''; return; }
        avatar = await new Promise((resolve, reject) => { const reader = new FileReader(); reader.onload = () => resolve(reader.result); reader.onerror = reject; reader.readAsDataURL(image); });
        showAvatar(); changed();
      };
      document.querySelector('#user-reset').onclick = () => { clearDirty(); reload(); toast('已撤销未保存的修改'); };
      form.onsubmit = async (event) => {
        event.preventDefault();
        const username = document.querySelector('#user-name').value.trim();
        if (!username || username.length > 64) { toast('用户名须为 1～64 个字符', true); return; }
        try { await api('/api/config/user', { method: 'PUT', body: { username, avatar } }); applyUserProfile({ username, avatar }); clearDirty(); document.querySelector('#user-save-status').textContent = '用户资料已保存'; toast('用户资料已保存'); }
        catch (error) { toast(error.message, true); }
      };
    },
  };
}

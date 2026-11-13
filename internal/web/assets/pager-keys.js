// 方向键翻页：左方向键点「上一页」，右方向键点「下一页」。
// 焦点在输入框、下拉框等可编辑控件里时不处理，避免影响输入。
// 弹窗打开时只响应弹窗内的翻页控件（例如修改头像里的写真翻页），页面上的列表翻页暂停。
// 翻页控件已禁用（已到首页或末页）时什么也不做，但仍然拦截按键，不会误翻其他列表。

const pagers = [];

const editable = (element) => element instanceof HTMLElement
  && (element.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(element.tagName));

// root 是翻页控件所在的容器，容器从页面移除后自动注销。
// prevButton、nextButton 是返回按钮的函数，每次按键时重新查找，因为列表重绘后按钮会被替换。
export function bindArrowPaging(root, prevButton, nextButton) {
  pagers.push({ root, prevButton, nextButton });
}

document.addEventListener('keydown', (event) => {
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
  if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey || event.defaultPrevented) return;
  if (editable(event.target)) return;
  const openDialog = document.querySelector('dialog[open]');
  // 后注册的优先：弹窗里的翻页控件晚于列表注册，所以打开弹窗时优先由它响应。
  for (let i = pagers.length - 1; i >= 0; i--) {
    const pager = pagers[i];
    if (!pager.root.isConnected) {
      pagers.splice(i, 1);
      continue;
    }
    if (openDialog && !openDialog.contains(pager.root)) continue;
    const button = event.key === 'ArrowLeft' ? pager.prevButton() : pager.nextButton();
    if (button && !button.disabled) {
      event.preventDefault();
      button.click();
      // 鼠标点过的按钮仍有焦点，按键后浏览器会给它显示默认的焦点框（黑框）。翻页后取消焦点，框就不再出现。
      const focused = document.activeElement;
      if (focused instanceof HTMLElement && pager.root.contains(focused)) focused.blur();
    }
    return;
  }
});

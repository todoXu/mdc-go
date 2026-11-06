// ==UserScript==
// @name         Emby 海报卡片样式
// @namespace    local.mdcng.emby
// @version      2.7.1
// @description  调整 Emby Web 海报卡片的圆角遮罩和聚焦边框
// @match        http://192.168.50.143:8097/web/*
// @match        http://192.168.50.143:8096/web/*
// @run-at       document-start
// @grant        none
// @noframes
// ==/UserScript==

(function () {
  'use strict';

  // 0 表示完全直角；想保留小圆角可改为 0.12em。
  const CARD_RADIUS = '0';
  // false 表示不显示键盘/点击聚焦边框。
  const SHOW_FOCUS_RING = false;

  const styleID = 'mdcng-emby-card-style';
  const focusRingCSS = SHOW_FOCUS_RING
    ? ''
    : `
.card:focus .cardContent-bxsborder::before,
.card:focus-visible .cardContent-bxsborder-fv::before,
.card:focus-within .cardContent-bxsborder::before,
.card:focus-within .cardContent-bxsborder-fv::before,
.cardContent-bxsborder::before,
.cardContent-bxsborder-fv::before {
  content: none !important;
  display: none !important;
  border: 0 !important;
}

.card:focus,
.card:focus-visible,
.card:focus-within,
.card:focus .cardBox-touchzoom,
.card:focus-visible .cardBox-touchzoom,
.card:focus-within .cardBox-touchzoom,
.card .cardContent-button:focus,
.card .cardContent-button:focus-visible,
.cardContent:focus,
.cardContent:focus-visible,
.cardContent-button:focus,
.cardContent-button:focus-visible {
  outline: none !important;
  box-shadow: none !important;
}

`;

  const css = `
/* 不依赖 card 父级；不同 Emby 主题的卡片包装层不一致。 */
.card:not(.backdropCard) .cardImageContainer,
.card:not(.backdropCard) .cardImage,
.card:not(.backdropCard) .cardContent,
.card:not(.backdropCard) .cardContent-button,
.card:not(.backdropCard) .cardOverlayContainer,
.card:not(.backdropCard) .visualCardBox,
.portraitCard:not(.backdropCard) .cardImageContainer,
.portraitCard:not(.backdropCard) .cardImage,
.portraitCard:not(.backdropCard) .cardContent,
.portraitCard:not(.backdropCard) .cardOverlayContainer {
  border-radius: ${CARD_RADIUS} !important;
}

.card:not(.backdropCard) .cardImageContainer,
.portraitCard:not(.backdropCard) .cardImageContainer {
  background-clip: border-box !important;
}

/* 卡片只负责布局，海报必须完整显示，不能被卡片比例裁剪。 */
.card:not(.backdropCard),
.card:not(.backdropCard) .cardImageContainer,
.card:not(.backdropCard) .cardImageContainer .cardImage,
.card:not(.backdropCard) .cardImageContainer img {
  overflow: hidden !important;
}

/* hover 缩放需要允许 touchzoom 层溢出；否则 Emby 的悬停动画会被截掉。 */
.card:not(.backdropCard),
.card:not(.backdropCard) .cardBox,
.card:not(.backdropCard) .cardBox-focustransform,
.card:not(.backdropCard) .cardBox-focustransform-titleadjust,
.card:not(.backdropCard) .cardBox-touchzoom,
.card:not(.backdropCard) .cardOverlayContainer {
  overflow: visible !important;
}

/* Emby 主题会给图片层预留内边距和背景色，造成上边/左边出现一圈灰框。 */
.card:not(.backdropCard) .cardImageContainer,
.card:not(.backdropCard) .cardImageContainer .cardImage,
.card:not(.backdropCard) .cardImageContainer picture,
.card:not(.backdropCard) .cardImageContainer img {
  margin: 0 !important;
  padding: 0 !important;
  border: 0 !important;
  background-color: transparent !important;
  box-shadow: none !important;
}

/* Emby 4.x 实际卡片层：cardContent 是外框按钮，cardImage 是海报图片。 */
.card.card-hoverable .cardContent.cardImageContainer,
.card.card-hoverable .cardContent.cardImageContainer.cardContent-background,
.card.card-hoverable .cardImage.cardImage-bxsborder-fv {
  background: transparent !important;
  background-color: transparent !important;
  border: 0 !important;
  box-shadow: none !important;
}

.card.card-hoverable .cardContent.cardImageContainer::before,
.card.card-hoverable .cardContent.cardImageContainer::after,
.card.card-hoverable .cardImage.cardImage-bxsborder-fv::before,
.card.card-hoverable .cardImage.cardImage-bxsborder-fv::after,
.card.card-hoverable .cardContent-bxsborder-fv::before,
.card.card-hoverable .cardContent-bxsborder-fv::after {
  content: none !important;
  display: none !important;
  border: 0 !important;
  box-shadow: none !important;
}

.card:not(.backdropCard) .cardImageContainer img,
.card:not(.backdropCard) .cardImageContainer picture,
.card:not(.backdropCard) .cardImageContainer .cardImage {
  width: 100% !important;
  height: auto !important;
  object-fit: contain !important;
  object-position: center center !important;
  background-size: contain !important;
  background-position: center center !important;
  background-repeat: no-repeat !important;
}

/* cardPadder-portrait 默认固定 2:3，会裁掉 375x531 这类实际海报。让图片
   自己决定高度，外框和海报使用完全相同的尺寸。 */
.card.card-hoverable .cardContent.cardImageContainer.cardPadder-portrait,
.card.card-hoverable .cardImage.cardImage-bxsborder-fv,
.card.card-hoverable .cardContent.cardImageContainer .cardImage {
  min-height: 0 !important;
  padding-bottom: 0 !important;
}

/* Emby 的图片是绝对定位元素，父按钮不能是 0 高度。当前海报接口返回
   375x531，按这个实际比例建立父层，图片完整显示且不会被父层裁掉。 */
.card.card-hoverable .cardContent.cardImageContainer.cardPadder-portrait {
  height: auto !important;
  aspect-ratio: 375 / 531 !important;
  min-height: 0 !important;
}

.card.card-hoverable .cardImage.cardImage-bxsborder-fv {
  height: 100% !important;
  aspect-ratio: auto !important;
  object-fit: contain !important;
}

/* 悬停遮罩不能继续使用 Emby 默认 2:3，否则会比实际海报多出一截。 */
.card.card-hoverable .cardOverlayContainer.cardPadder-portrait {
  top: 0 !important;
  right: auto !important;
  bottom: 2.8em !important;
  left: 0 !important;
  width: 100% !important;
  height: auto !important;
  aspect-ratio: auto !important;
  min-height: 0 !important;
  padding-bottom: 0 !important;
}

/* 详情页大封面没有标题区，遮罩必须覆盖整张大图。 */
.card.detailImageContainerCard .cardOverlayContainer.cardPadder-portrait {
  bottom: 0 !important;
}

/* 合集卡片只有一行标题，遮罩只避开这一行。 */
.card.card-hoverable:not(.detailImageContainerCard):not(:has(.cardText-secondary)) .cardOverlayContainer.cardPadder-portrait {
  bottom: 1.55em !important;
}

/* 有年份/副标题的卡片保留两行文字空间。 */
.card.card-hoverable:not(.detailImageContainerCard):has(.cardText-secondary) .cardOverlayContainer.cardPadder-portrait {
  bottom: 2.8em !important;
}

/* Emby 用这个伪元素撑出固定 2:3 高度；必须一并移除，否则父层仍会裁剪图片。 */
.card.card-hoverable .cardPadder-portrait::before,
.card.card-hoverable .cardPadder-portrait::after {
  content: none !important;
  display: none !important;
  height: 0 !important;
  padding: 0 !important;
  margin: 0 !important;
  border: 0 !important;
}

.card.card-hoverable .cardBox.cardBox-touchzoom {
  height: auto !important;
  min-height: 0 !important;
}

.card.card-hoverable .cardImage.cardImage-bxsborder-fv {
  display: block !important;
  width: 100% !important;
}

.card:not(.backdropCard) .cardImageContainer {
  display: flex !important;
  align-items: center !important;
  justify-content: center !important;
}

/* 仅修复“继续观看”等横向 backdrop 卡片的图片本身；不改容器、遮罩和其它卡片。 */
.card.backdropCard .cardImageContainer .cardImage,
.card.backdropCard .cardImageContainer img {
  width: 100% !important;
  height: 100% !important;
  object-fit: cover !important;
  object-position: center center !important;
}

/* 详情页/图片预览保持完整显示，不套用卡片的 cover 规则。 */
.detailImageContainer img[src*="/Items/"][src*="/Images/Primary"],
.detailImageContainer img[src*="/Items/"][src*="/Images/Backdrop"],
.detailImageContainer img[src*="/Items/"][src*="/Images/Thumb"] {
  object-fit: contain !important;
  object-position: center center !important;
}

[style*="/Items/"][style*="/Images/Primary"],
[style*="/Items/"][style*="/Images/Backdrop"],
[style*="/Items/"][style*="/Images/Thumb"],
.itemBackdrop,
.backdropImage,
.detailImage,
.itemDetailImage,
.detailPagePrimaryImage {
  background-size: contain !important;
  background-position: center center !important;
  background-repeat: no-repeat !important;
  object-fit: contain !important;
  object-position: center center !important;
}
${focusRingCSS}
`;

  function injectStyle() {
    const head = document.head || document.documentElement;
    if (!head) {
      return false;
    }
    let style = document.getElementById(styleID);
    if (!style) {
      style = document.createElement('style');
      style.id = styleID;
      head.appendChild(style);
    }
    if (style.textContent !== css) {
      style.textContent = css;
    }
    return true;
  }

  injectStyle();

  // Emby 是 SPA，主题和卡片会在登录后继续加载；保持样式节点不丢失。
  let scheduled = false;
  const observer = new MutationObserver(() => {
    if (scheduled) {
      return;
    }
    scheduled = true;
    requestAnimationFrame(() => {
      scheduled = false;
      injectStyle();
    });
  });
  observer.observe(document.documentElement, { childList: true, subtree: true });
})();

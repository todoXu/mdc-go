function fail(code, message) { throw {code: code, message: message}; }
function base() { return (config.BaseURL || manifest.BaseURL).replace(/\/$/, ""); }
function check(page) {
  if (page.status === 404) fail("not_found", "数据源没有找到这个番号");
  if (page.status === 403 || page.status === 429 || /cf-chl-|just a moment|captcha|driver-verify/i.test(page.body || "")) fail("verification_required", "官网需要验证，请在浏览器中处理后重试；也可在配置中填写 Cookies");
  if (page.status !== 200) fail("upstream_error", "数据源返回 HTTP " + page.status);
}
function nodes(page, selector) { return host.select(page.body, selector); }
function first(page, selector) { return nodes(page, selector)[0] || null; }
function description(page) {
  var node = first(page, "section:has(h2:contains('紹介文')) p");
  if (!node) node = first(page, "h2:contains('紹介文') + p");
  return node ? node.text.trim() : "";
}
function labeled(page, name) {
  var rows = nodes(page, "[role='tabpanel'] dl > div");
  for (var i=0; i<rows.length; i++) {
    var dt = host.select(rows[i].html, "dt"), dd = host.select(rows[i].html, "dd");
    if (dt.length && dd.length && dt[0].text.replace(/[：:\s]+$/, "") === name) return dd[0].text;
  }
  return "";
}
function list(page, selector, attribute) {
  var found = nodes(page, selector), output = [];
  for (var i=0; i<found.length; i++) {
    var item = attribute ? found[i].attrs[attribute] : found[i].text;
    if (attribute && item) item = host.resolve(page.url, item);
    if (item && output.indexOf(item) < 0) output.push(item);
  }
  return output;
}
function workNumber(id) { return host.normalize(id.split(":").pop()); }
function publishNumberFromCover(address) {
  var match = address.match(/\/(?:pics_dig\/)?(?:digital\/video|(?:dig\/)?mono\/movie(?:\/adult)?)\/([^/]+)\//i);
  return match ? match[1].toLowerCase() : "";
}
function posterFromCover(address) {
  // Digital covers use a zero-padded content ID; physical posters use a
  // separate directory and a three-digit-minimum product number.
  var match = address.match(/^https?:\/\/(?:pics\.dmm\.co\.jp|awsimgsrc\.dmm\.com)\/(.+)pl\.jpg(?:[?#].*)?$/i);
  if (!match) return "";
  var path = match[1];
  var digital = path.match(/^digital\/video\/([a-z]+)([0-9]+)\/([^/]+)$/i);
  if (digital) {
    if (digital[1] + digital[2] !== digital[3]) return "";
    var product = digital[1] + digital[2].replace(/^0+(?=[0-9]{3})/, "");
    return "https://awsimgsrc.dmm.com/dig/mono/movie/" + product + "/" + product + "ps.jpg";
  }
  return "https://awsimgsrc.dmm.com/" + path + "ps.jpg";
}
function locate(input) {
  input = input.trim();
  var qualified = input;
  if (/^https?:\/\//.test(input)) {
    var prefix = base() + "/works/";
    if (input.indexOf(prefix) !== 0 || /[?#]/.test(input)) fail("invalid_number", "作品链接必须属于当前数据源，并指向站内作品详情");
    qualified = decodeURIComponent(input.slice(prefix.length));
  }
  if (/^[A-Za-z0-9_-]+:[A-Za-z0-9_-]+$/.test(qualified))
    return {number: workNumber(qualified), path: "/works/" + encodeURIComponent(qualified)};
  var number = host.normalize(input);
  if (!number) fail("invalid_number", "请输入有效番号或站内作品链接");
  var search = host.request(base() + "/works?q=" + encodeURIComponent(number), {}); check(search);
  var matches = nodes(search, "a[href^='/works/']"), paths = [];
  for (var i=0; i<matches.length; i++) {
    var path = matches[i].attrs.href || "", id = path.split("/").pop();
    if (workNumber(id) === number && paths.indexOf(path) < 0) paths.push(path);
  }
  if (!paths.length) fail("not_found", "搜索结果中没有找到完全匹配的番号");
  if (paths.length > 1) fail("ambiguous_number", "存在多个同番号作品，请直接输入作品链接或厂商:番号");
  return {number: number, path: paths[0]};
}
var plugin = {
  test: function() {
    var page = host.request(base() + "/", {}); check(page);
    if (!nodes(page, "a[href^='/works/'], a[href='/search']").length) fail("unexpected_page", "已连接网站，但没有找到预期内容");
    return true;
  },
  fetch: function(input) {
    var work = locate(input);
    if (!work.number) fail("invalid_number", "作品编号无效");
    var page = host.request(host.resolve(base() + "/", work.path), {}); check(page);
    var canonical = first(page, "link[rel='canonical']"), title = first(page, "h1"), cover = first(page, "[role='tabpanel'] img[src*='pl.jpg']");
    var trailerNode = first(page, "video source[src], video[src], meta[property='og:video:url'][content], meta[property='og:video'][content]");
    var matched = canonical && (canonical.attrs.href || "").match(/([A-Z0-9]+-[0-9]+)$/i);
    var number = matched ? host.normalize(matched[1]) : "";
    if (!number || !title) fail("parse_error", "页面缺少番号或标题；可能是验证页或站点结构变化");
    if (number !== work.number) fail("number_mismatch", "返回页面的番号与查询不一致");
    var coverURL = cover ? host.resolve(page.url, cover.attrs.src || "") : "";
    return {
      provider: manifest.ID, source_url: page.url, number: number,
      title: title.text, original_title: title.text,
      outline: description(page),
      publish_number: publishNumberFromCover(coverURL),
      cover: coverURL, poster: posterFromCover(coverURL),
      trailer: trailerNode ? host.resolve(page.url, trailerNode.attrs.src || trailerNode.attrs.content || "") : "",
      release: labeled(page, "発売日").replace(/\//g, "-"),
      runtime: labeled(page, "収録分数"), studio: labeled(page, "メーカー"),
      publisher: labeled(page, "レーベル"), series: labeled(page, "シリーズ"),
      director: labeled(page, "監督"),
      // 页面的外层 section 同时包含「出演者・メモ」「タグ・説明文」「収録作品」，
      // 「収録作品」里是收录本片的合集及其演员、标签。只取不再嵌套 section 的最内层，避免混入。
      actors: list(page, "section:has(h2:contains('出演者')):not(:has(section)) a[href^='/talents/']"),
      tags: list(page, "section:has(h2:contains('タグ・説明文')):not(:has(section)) a[href^='/tags/']"),
      samples: list(page, "[role='tabpanel'] a[href*='pics.dmm.co.jp'][href$='.jpg']", "href")
    };
  },
  image: function(address, referer) {
    var page = host.request(address, {binary: true, referer: referer});
    if (page.status !== 200) fail("image_upstream", "图片站点拒绝访问或图片不存在");
    return {body_base64: page.body_base64, content_type: page.content_type};
  }
};

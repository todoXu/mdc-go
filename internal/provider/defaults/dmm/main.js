function fail(code, message) { throw {code: code, message: message}; }
function base() { return (config.BaseURL || manifest.BaseURL).replace(/\/$/, ""); }
function check(page) {
  if (page.status === 404) fail("not_found", "数据源没有找到这个番号");
  if (page.status === 403 || page.status === 429 || /cf-chl-|just a moment|captcha/i.test(page.body || "")) fail("verification_required", "官网需要验证，请在浏览器中处理后重试；也可在配置中填写 Cookies");
  if (page.status !== 200) fail("upstream_error", "数据源返回 HTTP " + page.status);
  if (/年齢認証/.test(page.body || "")) fail("age_verification_required", "DMM 需要年龄确认：请在浏览器中确认年龄后，把 age_check_done=1 填到数据源配置的 Cookies 中");
}
function nodes(page, selector) { return host.select(page.body, selector); }
function first(page, selector) { return nodes(page, selector)[0] || null; }
function labeled(page, name) {
  var rows = nodes(page, "td");
  for (var i = 0; i < rows.length; i++) {
    if (rows[i].text.replace(/[：:\s]+$/, "") === name && rows[i + 1]) return rows[i + 1].text.trim();
  }
  return "";
}
// 取「名称：」所在行右侧单元格里的全部链接文字，用于标签（ジャンル）。
function labeledLinks(page, name) {
  var cells = nodes(page, "td");
  for (var i = 0; i < cells.length; i++) {
    if (cells[i].text.replace(/[：:\s]+$/, "") === name && cells[i + 1]) {
      var anchors = host.select(cells[i + 1].html, "a"), output = [];
      for (var j = 0; j < anchors.length; j++) {
        var text = anchors[j].text.trim();
        if (text && output.indexOf(text) < 0) output.push(text);
      }
      return output;
    }
  }
  return [];
}
function links(page, selector, pattern) {
  var found = nodes(page, selector), output = [];
  for (var i = 0; i < found.length; i++) {
    var text = found[i].text.trim();
    if (!text || (pattern && !pattern.test(found[i].attrs.href || ""))) continue;
    if (output.indexOf(text) < 0) output.push(text);
  }
  return output;
}
// cid 形如 118abw153（数字前缀）、abw00153（补零）、118abw153dod（版本后缀）。
// 返回番号（如 ABW-153）和后缀（如 dod，标准版为空）。
function parseCid(cid) {
  var match = cid.toLowerCase().match(/^(?:[0-9]+)?([a-z]+)0*([0-9]+)([a-z]*)$/);
  if (!match) return {number: "", suffix: ""};
  return {number: match[1].toUpperCase() + "-" + match[2], suffix: match[3]};
}
function locate(input) {
  input = input.trim();
  var number = host.normalize(input);
  if (!number) fail("invalid_number", "请输入有效番号");
  var search = host.request(base() + "/search/=/searchstr=" + encodeURIComponent(number) + "/limit=30/sort=rankprofile", {});
  check(search);
  var matches = nodes(search, "a[href*='/detail/=/cid=']"), plain = [], other = [];
  for (var i = 0; i < matches.length; i++) {
    var href = matches[i].attrs.href || "";
    var cid = (href.match(/cid=([a-z0-9_]+)/i) || [])[1] || "";
    var parsed = parseCid(cid);
    if (parsed.number !== number) continue;
    if (parsed.suffix === "") { if (plain.indexOf(href) < 0) plain.push(href); }
    else if (other.indexOf(href) < 0) other.push(href);
  }
  // 标准版（无后缀）优先；只有没有标准版时才使用带后缀的版本。
  var detail = plain.length ? plain : other;
  if (!detail.length) fail("not_found", "搜索结果中没有找到完全匹配的番号");
  if (detail.length > 1) fail("ambiguous_number", "存在多个同番号作品，请在搜索结果中确认");
  return {number: number, path: detail[0]};
}
var plugin = {
  test: function() {
    // 首页会跨域跳转到 fanza.jp，运行时拒绝跨站跳转，所以测试使用搜索页（跳转只在 dmm.co.jp 内）。
    var page = host.request(base() + "/search/=/searchstr=ABW-153/limit=30/sort=rankprofile", {}); check(page);
    if (!nodes(page, "a").length) fail("unexpected_page", "已连接网站，但没有找到预期内容");
    return true;
  },
  fetch: function(input) {
    var work = locate(input);
    var page = host.request(host.resolve(base() + "/", work.path), {}); check(page);
    var title = first(page, "h1"), cover = first(page, "img[src*='pl.jpg']");
    var coverURL = cover ? host.resolve(page.url, cover.attrs.src || "") : "";
    var number = work.number;
    if (!title) fail("parse_error", "页面缺少标题；可能是年龄确认未通过或页面结构变化");
    return {
      provider: manifest.ID, source_url: page.url.split("?")[0], number: number,
      title: title.text.trim(), original_title: title.text.trim(),
      cover: coverURL, poster: "",
      outline: first(page, "div.lh4 > p") ? first(page, "div.lh4 > p").text.trim() : "",
      release: labeled(page, "発売日").replace(/\//g, "-"),
      runtime: labeled(page, "収録時間"), studio: labeled(page, "メーカー"),
      publisher: labeled(page, "レーベル"), series: labeled(page, "シリーズ"),
      director: labeled(page, "監督"),
      actors: links(page, "a[href*='article=actress']"),
      tags: labeledLinks(page, "ジャンル")
    };
  },
  image: function(address, referer) {
    var page = host.request(address, {binary: true, referer: referer});
    if (page.status !== 200) fail("image_upstream", "图片站点拒绝访问或图片不存在");
    return {body_base64: page.body_base64, content_type: page.content_type};
  }
};

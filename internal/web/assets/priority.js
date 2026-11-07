import { api, escapeHTML as h } from './api.js';
import { imageGallery } from './gallery.js';
import { providerStyle } from './provider-color.js';

const fieldNames = {
  publish_number: '发行码',
  title: '标题',
  original_title: '原始标题',
  cover: '封面',
  poster: '海报',
  release: '发行日期',
  runtime: '时长',
  director: '导演',
  studio: '制作商',
  publisher: '发行商',
  series: '系列',
  actors: '演员',
  tags: '标签',
  samples: '剧照',
  trailer: '预告片',
  outline: '简介',
  rating: '评分',
};

export async function mountPriorityEditor(container, sources, onDirty, onSaved) {
  sources = sources.filter((source) => source.enabled);
  try {
    const { config, fields } = await api('/api/providers/priorities');
    let saved = structuredClone(config),
      draft = structuredClone(config);
    draft.by_fields ||= {};
    const name = (id) => sources.find((source) => source.id === id)?.name || id;
    const status = (id) =>
      !sources.some((source) => source.id === id)
        ? ' · 未安装'
        : !sources.find((source) => source.id === id).enabled
          ? ' · 未启用'
          : '';
    const order = (key) => draft.by_fields[key] || [];
    const setOrder = (key, list) => {
      draft.by_fields[key] = list;
    };
    let drag = null;
    const row = (key, title) => {
      const list = order(key);
      return `<div class="priority-row"><div class="priority-label">${h(title)}<small>从左到右，优先级依次降低</small></div><div class="priority-order"><div class="priority-chips" data-drop-field="${key}">${list.length ? list.map((id, index) => `<span class="priority-chip provider-colored" style="${providerStyle(id)}" draggable="true" data-drag-field="${key}" data-index="${index}"><span>${index + 1}. ${h(name(id))}<small>${h(status(id))}</small></span><button type="button" data-change="up" data-field="${key}" data-index="${index}" aria-label="${h(title + ' ' + name(id) + ' 提高优先级')}" ${index === 0 ? 'disabled' : ''}>←</button><button type="button" data-change="down" data-field="${key}" data-index="${index}" aria-label="${h(title + ' ' + name(id) + ' 降低优先级')}" ${index === list.length - 1 ? 'disabled' : ''}>→</button><button type="button" data-change="remove" data-field="${key}" data-index="${index}" aria-label="${h(title + ' 移除 ' + name(id))}">×</button></span>`).join('') : `<span class="priority-inherit">未配置，不抓取此字段</span>`}</div><select data-add="${key}" aria-label="${h(title + ' 添加数据源')}"><option value="">＋ 添加数据源</option>${sources
        .filter((source) => !list.includes(source.id))
        .map(
          (source) =>
            `<option value="${h(source.id)}">${h(source.name + status(source.id))}</option>`,
        )
        .join('')}</select></div></div>`;
    };
    function render() {
      container.innerHTML = `<section class="panel"><div class="panel-head"><div><h2>优先级设置（字段）</h2><p class="subtitle">为各个元数据字段配置刮削源优先级</p></div><div class="priority-quick"><select id="priority-quick-source" aria-label="选择要统一调整的数据源"${sources.length ? '' : ' disabled'}><option value="">选择数据源</option>${sources.map((source) => `<option value="${h(source.id)}">${h(source.name)}</option>`).join('')}</select><button class="button" type="button" id="priority-quick-top">全部字段设为最高</button><button class="button" type="button" id="priority-quick-bottom">全部字段设为最低</button></div></div><div class="notice priority-notice">只使用已启用的数据源，按每个字段自己的顺序查询。未配置来源的字段不抓取，不再使用全局补充。可拖动或使用箭头排序。</div>${fields.map((field) => row(field, fieldNames[field] || field)).join('')}<div class="savebar"><span id="priority-save-status">修改后记得保存</span><div><button class="button" id="reset-priorities" type="button">撤销修改</button> <button class="button primary" id="save-priorities" type="button">保存优先级</button></div></div></section><section class="panel"><div class="panel-head"><h2>按优先级查询</h2><small>展示字段来源</small></div><form class="path-bar" id="priority-query"><input class="search" name="number" aria-label="聚合查询番号" placeholder="输入番号，验证字段选择结果" required maxlength="64"><button class="button" type="submit">查询并合并</button></form><div id="priority-result" class="activity-note" role="status">使用已保存的优先级，仅查询已启用的插件；不下载或整理文件。</div></section>`;
      // 「按优先级查询」放在「优先级设置（字段）」上面。
      container.prepend(container.querySelector('#priority-query').closest('section'));
      container.querySelectorAll('[data-add]').forEach(
        (select) =>
          (select.onchange = () => {
            if (!select.value) return;
            setOrder(select.dataset.add, [...order(select.dataset.add), select.value]);
            changed();
          }),
      );
      container.querySelectorAll('[data-change]').forEach(
        (button) =>
          (button.onclick = () => {
            const key = button.dataset.field,
              list = [...order(key)],
              index = Number(button.dataset.index);
            if (button.dataset.change === 'remove') list.splice(index, 1);
            else {
              const next = index + (button.dataset.change === 'up' ? -1 : 1);
              [list[index], list[next]] = [list[next], list[index]];
            }
            setOrder(key, list);
            changed();
          }),
      );
      container.querySelectorAll('[data-drag-field]').forEach((chip) => {
        chip.ondragstart = (event) => {
          drag = { field: chip.dataset.dragField, index: Number(chip.dataset.index) };
          event.dataTransfer.effectAllowed = 'move';
          event.dataTransfer.setData('text/plain', 'priority');
        };
        chip.ondragend = () => {
          drag = null;
        };
      });
      container.querySelectorAll('[data-drop-field]').forEach((group) => {
        group.ondragover = (event) => {
          if (drag?.field === group.dataset.dropField) event.preventDefault();
        };
        group.ondrop = (event) => {
          event.preventDefault();
          if (!drag || drag.field !== group.dataset.dropField) return;
          const target = event.target.closest('[data-index]');
          const list = [...order(drag.field)],
            to = target ? Number(target.dataset.index) : list.length - 1;
          const [id] = list.splice(drag.index, 1);
          list.splice(to, 0, id);
          setOrder(drag.field, list);
          drag = null;
          changed();
        };
      });
      // 对每个字段统一调整：先移除该数据源，再放到最前（最高）或最后（最低）；字段里没有它时直接加入。
      const moveAll = (position) => {
        const id = container.querySelector('#priority-quick-source').value;
        if (!id) {
          container.querySelector('#priority-save-status').textContent = '请先选择数据源';
          return;
        }
        for (const field of fields) {
          const list = order(field).filter((item) => item !== id);
          setOrder(field, position === 'top' ? [id, ...list] : [...list, id]);
        }
        changed();
      };
      container.querySelector('#priority-quick-top').onclick = () => moveAll('top');
      container.querySelector('#priority-quick-bottom').onclick = () => moveAll('bottom');
      container.querySelector('#reset-priorities').onclick = () => {
        draft = structuredClone(saved);
        onSaved();
        render();
      };
      container.querySelector('#save-priorities').onclick = async (event) => {
        event.target.disabled = true;
        try {
          // 只提交字段优先级，「失败时特别提示」单独保存，互不覆盖。
          await api('/api/providers/priorities', { method: 'PUT', body: { by_fields: draft.by_fields } });
          saved = structuredClone(draft);
          onSaved();
          container.querySelector('#priority-save-status').textContent = '优先级已保存';
        } catch (error) {
          container.querySelector('#priority-save-status').textContent = error.message;
        } finally {
          event.target.disabled = false;
        }
      };
      container.querySelector('#priority-query').onsubmit = async (event) => {
        event.preventDefault();
        const result = container.querySelector('#priority-result');
        if (JSON.stringify(draft) !== JSON.stringify(saved)) {
          result.textContent = '请先保存优先级，再进行查询';
          return;
        }
        const button = event.target.querySelector('button');
        button.disabled = true;
        result.textContent = '正在按字段优先级查询…';
        try {
          const data = await api('/api/providers/fetch', {
            method: 'POST',
            body: { number: new FormData(event.target).get('number') },
            timeout: 65000,
          });
          result.innerHTML = `<div class="table-wrap"><table><thead><tr><th>字段</th><th>取得的数据</th><th>来源</th></tr></thead><tbody>${fields
            .map((field) => {
              const value = data.metadata[field];
              const display = ['cover', 'poster', 'samples'].includes(field)
                ? imageGallery(value, fieldNames[field], data.metadata.image_previews)
                : h(Array.isArray(value) ? value.join('、') || '—' : value || '—');
              return `<tr><td>${h(fieldNames[field] || field)}</td><td class="priority-value">${display}</td><td>${h(data.provenance[field] ? name(data.provenance[field]) : '未取得')}</td></tr>`;
            })
            .join('')}</tbody></table></div>${Object.entries(data.warnings)
            .map(([id, message]) => `<p>${h(name(id))}：${h(message)}</p>`)
            .join('')}`;
        } catch (error) {
          result.textContent = error.message;
          for (const [id, message] of Object.entries(error.details?.warnings || {})) {
            const line = document.createElement('p');
            line.textContent = name(id) + '：' + message;
            result.append(line);
          }
        } finally {
          button.disabled = false;
        }
      };
    }
    function changed() {
      onDirty();
      render();
      container.querySelector('#priority-save-status').textContent = '有尚未保存的修改';
    }
    render();
  } catch (error) {
    container.textContent = '无法读取优先级：' + error.message;
  }
}

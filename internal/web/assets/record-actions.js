import { api, escapeHTML as h } from './api.js';

const icons = {
  edit: '<path d="m16 3 5 5-12 12H4v-5L16 3ZM14 5l5 5"/>',
  crop: '<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8" cy="8" r="1"/><path d="m3 17 6-6 4 4 3-3 5 5"/>',
  delete: '<path d="M3 6h18M9 6V3h6v3M6 6l1 15h10l1-15M10 10v7M14 10v7"/>',
  task: '<path d="M20 7v5h-5M4 17v-5h5M6 7a7 7 0 0 1 12-2l2 2M4 17l2 2a7 7 0 0 0 12-2"/>',
};
const actions = [['edit','编辑元数据'],['crop','封面裁剪'],['delete','删除记录'],['task','重新任务']];
const fields = [['number','番号'],['publish_number','发行码'],['title','标题'],['original_title','原标题'],['outline','简介'],['original_outline','原简介'],['actors','演员'],['cover','封面'],['poster','海报'],['samples','剧照'],['trailer','预告片'],['tags','标签'],['release','发布日期'],['runtime','影片时长'],['rating','用户评分'],['director','导演'],['series','系列'],['studio','厂商'],['publisher','出品'],['votes','评分人数']];
const lists = new Set(['actors','samples','tags']);
const inlineLists = new Set(['actors','tags']);
const display = (value, key = '') => Array.isArray(value) ? value.join(inlineLists.has(key) ? ', ' : '\n') : value || '';
const parseList = (value, key) => value.split(inlineLists.has(key) ? /[,，\r\n]+/ : /\r?\n/).map(item => item.trim()).filter(Boolean);
const endpoint = job => '/api/scrape/jobs/' + encodeURIComponent(job.id);

export function recordActionButtons(job) {
  const busy = [0,1].includes(job.status) || job.editing;
  return `<div class="record-actions">${actions.map(([key,label]) => `<button class="icon-button" type="button" data-record-action="${key}" aria-label="${label}" title="${label}" ${busy || (key==='crop' && job.status!==2) || (key==='edit' && !job.metadata) ? 'disabled' : ''}><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${icons[key]}</svg></button>`).join('')}</div>`;
}

// size：sm 用于表单类窗口（任务、批量、删除），md 用于编辑元数据，lg 用于封面裁剪。
function dialog(title, body, submit, size='sm') {
  const element = document.createElement('dialog');
  element.className = `record-editor record-editor-${size}`;
  element.setAttribute('aria-label', title);
  element.innerHTML = `<form><header><h2>${h(title)}</h2><button class="icon-button" type="button" data-cancel aria-label="关闭">×</button></header><div class="record-editor-body">${body}</div><p class="record-editor-error" role="alert"></p><footer><button class="button" type="button" data-cancel>取消</button><button class="button primary" type="submit">${h(submit)}</button></footer></form>`;
  document.body.append(element);
  element.querySelectorAll('[data-cancel]').forEach(button => button.onclick=()=>element.close());
  element.addEventListener('close',()=>element.remove(),{once:true});
  // 打开时把焦点放在窗口本身，避免关闭按钮一出来就带着焦点框。
  element.tabIndex = -1;
  element.showModal();
  element.focus();
  return element;
}

function onSubmit(element, callback) {
  element.querySelector('form').onsubmit=async event=>{
    event.preventDefault();
    const submit=element.querySelector('[type=submit]'), error=element.querySelector('[role=alert]');
    submit.disabled=true;
    element.querySelectorAll('[data-cancel]').forEach(button=>button.disabled=true);
    const preventClose=event=>event.preventDefault();
    element.addEventListener('cancel',preventClose);
    error.textContent='';
    try { await callback(); element.close(); }
    catch (failure) { error.textContent=failure.message; }
    finally {
      submit.disabled=false;
      element.querySelectorAll('[data-cancel]').forEach(button=>button.disabled=false);
      element.removeEventListener('cancel',preventClose);
    }
  };
}

// 编辑元数据里需要占满整行的字段；其余短字段两两并排。
const wideFields = new Set(['title','original_title','outline','original_outline','actors','cover','poster','samples','trailer','tags']);

function editMetadata(job, changed) {
  const meta=job.metadata || {}, sources=job.sources || {};
  const element=dialog('编辑元数据 · '+(job.number || '未识别番号'), `<p class="rd-hint rd-intro">更新会同步记录和 NFO；修改图片地址会按任务设置重新下载，已完成的任务会按命名设置重新整理。演员、标签用逗号分隔，剧照每行一项。</p><div class="record-edit-fields">${fields.map(([key,label])=>{
    const candidates=Object.entries(sources).filter(([,data])=>data[key] !== undefined && data[key] !== '');
    const selected=job.provenance?.[key];
    const input=inlineLists.has(key)?`<input type="text" name="${key}" value="${h(display(meta[key],key))}" readonly>`:`<textarea name="${key}" rows="1" readonly>${h(display(meta[key]))}</textarea>`;
    return `<label class="record-edit-field${wideFields.has(key)?' wide':''}"><span>${h(label)}</span><div>${input}<select data-source="${key}" aria-label="${h(label)}来源"><option value="">当前值</option>${candidates.map(([id])=>`<option value="${h(id)}" ${selected===id && JSON.stringify(meta[key])===JSON.stringify(sources[id][key])?'selected':''}>${h(id)}</option>`).join('')}<option value="manual">手动</option></select><button class="icon-button" type="button" data-edit-field="${key}" aria-label="手动编辑${h(label)}" title="手动编辑">✎</button></div></label>`;
  }).join('')}</div>`, '更新', 'md');
  // 文本框高度跟随内容：空的只占一行，长内容最多显示约 5 行，其余滚动。
  const fit=textarea=>{ textarea.style.height='auto'; textarea.style.height=Math.min(textarea.scrollHeight+2, 118)+'px'; };
  element.querySelectorAll('.record-edit-field textarea').forEach(textarea=>{ fit(textarea); textarea.addEventListener('input',()=>fit(textarea)); });
  element.querySelectorAll('[data-source]').forEach(select=>select.onchange=()=>{
    const input=element.querySelector(`[name="${select.dataset.source}"]`);
    input.readOnly=select.value!=='manual';
    if (sources[select.value]) input.value=display(sources[select.value][select.dataset.source],select.dataset.source);
    if (select.value==='manual') input.focus();
  });
  element.querySelectorAll('[data-edit-field]').forEach(button=>button.onclick=()=>{
    const select=element.querySelector(`[data-source="${button.dataset.editField}"]`);
    select.value='manual'; select.onchange();
  });
  onSubmit(element,async()=>{
    const values={}, selected={};
    for (const [key] of fields) {
      const input=element.querySelector(`[name="${key}"]`), value=lists.has(key)?parseList(input.value,key):input.value.trim();
      const source=element.querySelector(`[data-source="${key}"]`).value;
      if (JSON.stringify(value)!==JSON.stringify(meta[key] || (lists.has(key)?[]:'')) || (source && source!=='manual' && source!==job.provenance?.[key])) { values[key]=value; selected[key]=source; }
    }
    await api(endpoint(job)+'/metadata',{method:'PUT',body:{fields:values,sources:selected},timeout:150000});
    await changed();
  });
}

// 单条「重新任务」和「批量重新刮削」共用的标识选项。
function flagsHTML() {
  return `<section class="rd-section"><h3 class="rd-label">添加标识</h3>
    <div class="rd-grid2">
      <label class="rd-field"><span>影片类型</span><select name="mosaicFlag"><option value="">按文件名识别</option><option value="uncensored">无码</option><option value="ltx">LTX</option><option value="lada">LADA</option><option value="jav">JAV</option><option value="hack">破解</option></select></label>
      <label class="rd-field"><span>分辨率</span><select name="resolution"><option value="">按文件名识别</option><option value="4K">4K</option><option value="8K">8K</option></select></label>
    </div>
    <div class="rd-checks"><label><input type="checkbox" name="subtitle"> 字幕</label><label><input type="checkbox" name="clear"> 清除全部后缀标识</label></div>
    <p class="rd-hint">未勾选清除时按文件名识别；勾选清除且不选影片类型时视为常规作品。LTX、LADA、JAV 对应整理设置中的关键词分类。</p>
  </section>`;
}

export function taskDialog(job, changed) {
  const element=dialog(`重新任务 · ${job.number || '未识别番号'}`, `<p class="rd-hint rd-intro">任务 #${h(job.sequence)}</p>
    <section class="rd-section"><h3 class="rd-label">操作模式</h3><div class="rd-choices">
      <label class="rd-choice"><input type="radio" name="mode" value="retry" checked><span><b>重试刮削</b><small>重新从网络刮削元数据并整理文件</small></span></label>
      <label class="rd-choice"><input type="radio" name="mode" value="reorganize" ${job.status!==2?'disabled':''}><span><b>重新整理</b><small>用已有元数据和当前配置重新整理${job.status!==2?'（仅成功记录可用）':''}</small></span></label>
    </div></section>
    <section class="rd-section"><div class="rd-grid2">
      <label class="rd-field"><span>覆盖番号</span><input name="number" placeholder="留空按文件名识别"></label>
      <label class="rd-field"><span>覆盖网址</span><input name="url" type="url" placeholder="留空使用默认数据源"></label>
    </div></section>
    ${flagsHTML()}`, '确认执行');
  const url=element.querySelector('[name=url]');
  element.querySelectorAll('[name=mode]').forEach(radio=>radio.onchange=()=>{
    url.disabled=element.querySelector('[name=mode]:checked').value==='reorganize';
    if (url.disabled) url.value='';
  });
  onSubmit(element,async()=>{
    const form=element.querySelector('form'), data=new FormData(form), mosaic=data.get('mosaicFlag') || '';
    const flags={clear:data.has('clear'),subtitle:data.has('subtitle'),ltx:mosaic==='ltx',lada:mosaic==='lada',jav:mosaic==='jav',hack:mosaic==='hack',uncensored:mosaic==='uncensored',resolution:data.get('resolution') || ''};
    await api(endpoint(job)+'/'+data.get('mode'),{method:'POST',body:{number:data.get('number').trim(),url:(data.get('url')||'').trim(),flags:Object.values(flags).some(Boolean)?flags:null}});
    await changed();
  });
}

// 批量重新刮削：与单条「重新任务」窗口的标识选项相同，但不支持手动指定番号或网址，
// 每条记录沿用它自己的番号和数据源。
// 确认后一次提交全部记录，由服务端在后台逐条处理；刷新或关闭页面不影响。提交成功后调用 onStarted，进度由列表页显示。
export function batchRetryDialog(jobs, { onStarted }) {
  const element=dialog(`批量重新刮削 · ${jobs.length} 条`, `<p class="rd-hint rd-intro">对所选记录重新刮削并整理文件。批量操作不能指定番号或网址，每条记录沿用自己的番号和默认数据源。</p>${flagsHTML()}`, '确认执行');
  onSubmit(element,async()=>{
    const data=new FormData(element.querySelector('form')), mosaic=data.get('mosaicFlag') || '';
    const flags={clear:data.has('clear'),subtitle:data.has('subtitle'),ltx:mosaic==='ltx',lada:mosaic==='lada',jav:mosaic==='jav',hack:mosaic==='hack',uncensored:mosaic==='uncensored',resolution:data.get('resolution') || ''};
    const body=Object.values(flags).some(Boolean)?flags:null;
    await api('/api/scrape/batch/retry',{method:'POST',body:{ids:jobs.map(job=>job.id),flags:body}});
    onStarted?.();
  });
}

export function deleteRecordDialog(job, onDeleted) {
  const element=dialog('删除记录', `<p>任务 #${h(job.sequence)}</p><p>删除刮削记录及其日志。</p><label class="record-delete-media"><input type="checkbox" name="delete_media"> 同时删除媒体数据</label><p class="record-delete-warning">按整理模式删除：移动模式删除目标文件夹中的视频和产物；原地模式删除原地的视频和产物；STRM 模式删除源视频，以及目标文件夹中的 strm 和产物。文件夹变空时才会删除，监控目录和整理目录本身不会删除。</p>`, '确认删除记录');
  onSubmit(element,async()=>{
    const deleteMedia=element.querySelector('[name=delete_media]').checked;
    await api(endpoint(job),{method:'DELETE',body:{delete_media:deleteMedia}});
    if(onDeleted) await onDeleted();
  });
}

async function cropDialog(job, changed) {
  // 原图来源只列各数据源的海报和缩略图。默认选中当前海报的实际来源：
  // 直接使用某来源的海报时选「该来源 · 海报」，由某来源的缩略图裁剪时选「该来源 · 缩略图」。
  const sourceOptions=[];
  // 本地已保存的海报和缩略图也可以选，但不作为默认项。
  if (job.metadata?.local_images?.poster) sourceOptions.push(['local:poster','本地海报（可能已有水印）']);
  if (job.metadata?.local_images?.cover) sourceOptions.push(['local:thumb','本地缩略图（可能已有水印）']);
  const localCount=sourceOptions.length;
  for (const [id,source] of Object.entries(job.sources || {})) {
    if (source.poster) sourceOptions.push([id+':poster',id+' · 海报']);
    if (source.cover) sourceOptions.push([id+':cover',id+' · 缩略图']);
  }
  // 旧记录没有保存各数据源的结果时，只能按合并后的地址重新下载。
  if (sourceOptions.length===localCount) {
    if (job.metadata?.poster) sourceOptions.push(['poster','来源海报（重新下载）']);
    if (job.metadata?.cover) sourceOptions.push(['cover','来源封面（重新下载）']);
  }
  const posterProvider=job.provenance?.poster;
  const preferred=posterProvider ? posterProvider+(job.metadata?.poster_from_cover?':cover':':poster') : '';
  const defaultSource=sourceOptions.some(([key])=>key===preferred) ? preferred : (sourceOptions[localCount] || sourceOptions[0])?.[0];
  const element=dialog('封面裁剪', `<div class="crop-source-row"><label>原图来源<select name="source">${sourceOptions.map(([key,label])=>`<option value="${h(key)}" ${key===defaultSource?'selected':''}>${h(label)}</option>`).join('')}</select></label><button class="button" type="button" data-upload>手动选择</button><input type="file" accept="image/jpeg,image/png,image/webp,image/gif" hidden></div><div class="record-crop-layout"><div><canvas class="record-crop-canvas" tabindex="0" aria-label="裁剪框，拖动调整位置，滚轮或加减键调整大小，方向键移动"></canvas><label class="crop-zoom">框大小 <input name="zoom" type="range" min="1" max="4" step="0.01" value="1"><output>1.00×</output><button type="button" class="button small" data-reset>重置</button></label><p class="muted">拖动裁剪框移动位置；滚轮或双指调整框大小。方向键移动，Shift 加速，+ / − 调整大小，Home 重置。1.00× 是当前比例的最大框，数值越大框越小。</p><p class="muted" data-crop-size></p></div><aside><fieldset><legend>裁剪方式</legend>${[['Full','纵向（完整海报尺寸）'],['Emby','纵向（Emby 海报尺寸）'],['Landscape','横向']].map(([key,label],i)=>`<label><input type="radio" name="aspect" value="${key}" ${i===0?'checked':''}> ${label}</label>`).join('')}</fieldset><fieldset><legend>添加水印</legend><label><input type="checkbox" name="subtitle" ${job.has_subtitle?'checked':''}> 字幕</label><label>类型<select name="mosaic">${[['','无'],['is_uncensored','无码'],['is_ltx_hack','LTX'],['is_lada_hack','LADA'],['is_jav_hack','JAV'],['is_other_hack','破解']].map(([key,label])=>`<option value="${key}" ${job.file_info?.mosaic===key?'selected':''}>${label}</option>`).join('')}</select></label><label>分辨率<select name="resolution">${[['','无'],['4K','4K'],['8K','8K']].map(([key,label])=>`<option value="${key}" ${job.file_info?.resolution===key?'selected':''}>${label}</option>`).join('')}</select></label></fieldset><fieldset><legend>处理选项</legend><label><input type="checkbox" name="replace_thumb"> 同时用原图替换缩略图（不裁剪）</label></fieldset></aside></div>`, '提交','lg');
  const canvas=element.querySelector('canvas'), ctx=canvas.getContext('2d'), zoom=element.querySelector('[name=zoom]'), select=element.querySelector('[name=source]'), submit=element.querySelector('[type=submit]'), error=element.querySelector('[role=alert]');
  let image, body='', scale=1, x=0, y=0, frame, loading=0, cropMethod='Center', face={found:false,x:0,y:0};
  let disposed=false;
  element.addEventListener('close',()=>{disposed=true; image=null; body='';});
  function aspectRatio() {
    const aspect=element.querySelector('[name=aspect]:checked').value;
    return aspect==='Full'?2.12/3:aspect==='Emby'?2/3:16/9;
  }
  function geometry() {
    if (!image) return null;
    const ratio=aspectRatio();
    let w=Math.min(image.width,image.height*ratio)/scale, hh=w/ratio;
    w=Math.max(1,Math.floor(w)); hh=Math.max(1,Math.floor(hh));
    x=Math.max(0,Math.min(image.width-w,x)); y=Math.max(0,Math.min(image.height-hh,y));
    return {x:Math.round(x),y:Math.round(y),width:w,height:hh};
  }
  function suggestedPosition(width,height) {
    let nextX=(image.width-width)/2, nextY=(image.height-height)/2;
    if (cropMethod==='Right') nextX=image.width-width;
    if (cropMethod==='Face' && face.found) {
      nextX=face.x-width/2;
      nextY=face.y-height/3;
    }
    return {
      x:Math.max(0,Math.min(image.width-width,nextX)),
      y:Math.max(0,Math.min(image.height-height,nextY)),
    };
  }
  function draw() {
    const rect=geometry(); if (!rect) return;
    const factor=Math.min(1,1000/image.width,720/image.height);
    canvas.width=Math.round(image.width*factor); canvas.height=Math.round(image.height*factor);
    ctx.drawImage(image,0,0,canvas.width,canvas.height);
    ctx.fillStyle='#0009'; ctx.fillRect(0,0,canvas.width,canvas.height);
    const r={x:rect.x*factor,y:rect.y*factor,w:rect.width*factor,h:rect.height*factor};
    ctx.drawImage(image,rect.x,rect.y,rect.width,rect.height,r.x,r.y,r.w,r.h);
    ctx.strokeStyle='#ff2d2d'; ctx.lineWidth=3; ctx.strokeRect(r.x,r.y,r.w,r.h);
    frame=rect;
    element.querySelector('output').textContent=scale.toFixed(2)+'×';
    const method={None:'不裁剪',Right:'右侧',Center:'中心',Face:face.found?'人脸':'人脸（未检测到，中心）'}[cropMethod] || cropMethod;
    element.querySelector('[data-crop-size]').textContent=`裁剪范围：${rect.width} × ${rect.height} 像素 · 设置方式：${method}`;
  }
  function reset() {
    scale=1; zoom.value='1';
    if (image) {
      const ratio=aspectRatio(), width=Math.max(1,Math.floor(Math.min(image.width,image.height*ratio))), height=width/ratio;
      const position=suggestedPosition(width,height); x=position.x; y=position.y;
    } else { x=0; y=0; }
    draw();
  }
  async function loadImage(data, type) {
    const next=new Image();
    await new Promise((resolve,reject)=>{next.onload=resolve; next.onerror=()=>reject(Error('图片无法解码')); next.src='data:'+type+';base64,'+data;});
    if (disposed) return;
    if (next.width*next.height>40000000) throw Error('图片尺寸超过 4000 万像素');
    image=next; body=data; reset(); submit.disabled=false;
  }
  async function loadSource() {
    const version=++loading; submit.disabled=true; error.textContent='正在读取原图…';
    try {
      const value=select.value, split=value.lastIndexOf(':');
      const request=value.startsWith('local:')?{source:value}:split<0?{source:value}:{provider:value.slice(0,split),source:value.slice(split+1)};
      const result=await api(endpoint(job)+'/crop-source',{method:'POST',body:request,timeout:50000});
      if (version!==loading || disposed) return;
      cropMethod=result.crop_method || 'Center';
      face={found:Boolean(result.face_found),x:Number(result.face_x)||0,y:Number(result.face_y)||0};
      const aspect=element.querySelector(`[name=aspect][value="${result.crop_aspect}"]`);
      if (aspect) aspect.checked=true;
      await loadImage(result.body_base64,result.content_type); error.textContent='';
    } catch (failure) { if (version===loading && !disposed) error.textContent=failure.message; }
  }
  select.onchange=loadSource;
  const file=element.querySelector('[type=file]');
  element.querySelector('[data-upload]').onclick=()=>file.click();
  file.onchange=async()=>{
    const chosen=file.files?.[0]; if (!chosen) return;
    ++loading; submit.disabled=true;
    try {
      if (chosen.size>20*1024*1024) throw Error('图片不能超过 20 MB');
      const encoded=await new Promise((resolve,reject)=>{const reader=new FileReader();reader.onload=()=>resolve(reader.result);reader.onerror=()=>reject(Error('图片读取失败'));reader.readAsDataURL(chosen);});
      await loadImage(encoded.split(',')[1],chosen.type); error.textContent='';
      let option=select.querySelector('[data-manual]');
      if (!option) { option=document.createElement('option');option.dataset.manual='';option.value='upload';select.append(option); }
      option.textContent=chosen.name; select.value='upload';
    } catch (failure) { error.textContent=failure.message; }
  };
  function setZoom(value) {
    const before=geometry(); scale=Math.max(1,Math.min(4,value)); zoom.value=String(scale);
    const after=geometry(); if (before && after) { x=before.x+(before.width-after.width)/2; y=before.y+(before.height-after.height)/2; } draw();
  }
  zoom.oninput=()=>setZoom(Number(zoom.value));
  element.querySelector('[data-reset]').onclick=reset;
  element.querySelectorAll('[name=aspect]').forEach(radio=>radio.onchange=reset);
  const pointers=new Map(); let drag;
  canvas.onpointerdown=event=>{canvas.focus();canvas.setPointerCapture(event.pointerId);pointers.set(event.pointerId,{x:event.clientX,y:event.clientY});drag={x:event.clientX,y:event.clientY};};
  canvas.onpointermove=event=>{
    if (!pointers.has(event.pointerId) || !image) return;
    const previous=pointers.get(event.pointerId), others=[...pointers.entries()].filter(([id])=>id!==event.pointerId);
    if (others.length) { const other=others[0][1], old=Math.hypot(previous.x-other.x,previous.y-other.y), next=Math.hypot(event.clientX-other.x,event.clientY-other.y); if (old>0) setZoom(scale*next/old); }
    else if (drag) { const bounds=canvas.getBoundingClientRect(), factor=Math.min(bounds.width/image.width,bounds.height/image.height);x+=(event.clientX-drag.x)/factor;y+=(event.clientY-drag.y)/factor;draw(); }
    pointers.set(event.pointerId,{x:event.clientX,y:event.clientY});drag={x:event.clientX,y:event.clientY};
  };
  canvas.onpointerup=canvas.onpointercancel=event=>{pointers.delete(event.pointerId);drag=null;};
  canvas.addEventListener('wheel',event=>{event.preventDefault();setZoom(scale*(event.deltaY<0?1.08:1/1.08));},{passive:false});
  canvas.onkeydown=event=>{
    const step=event.shiftKey?20:2;
    if (!['ArrowLeft','ArrowRight','ArrowUp','ArrowDown','+','=','-','Home'].includes(event.key)) return;
    event.preventDefault();
    if (event.key==='Home') reset(); else if (event.key==='+' || event.key==='=') setZoom(scale*1.1); else if (event.key==='-') setZoom(scale/1.1);
    else { if (event.key==='ArrowLeft') x-=step; if (event.key==='ArrowRight') x+=step; if (event.key==='ArrowUp') y-=step; if (event.key==='ArrowDown') y+=step;draw(); }
  };
  onSubmit(element,async()=>{
    if (!body || !frame) throw Error('请先选择原图');
    const form=new FormData(element.querySelector('form'));
    await api(endpoint(job)+'/crop',{method:'POST',timeout:90000,body:{body_base64:body,...frame,replace_thumb:form.has('replace_thumb'),subtitle:form.has('subtitle'),mosaic:form.get('mosaic'),resolution:form.get('resolution')}});
    await changed();
  });
  await loadSource();
}

export function bindRecordActions(root, getJob, changed, back) {
  root.addEventListener('click',event=>{
    const button=event.target.closest('[data-record-action]');
    if (!button || button.disabled) return;
    const job=getJob();
    if (button.dataset.recordAction==='edit') editMetadata(job,changed);
    if (button.dataset.recordAction==='crop') cropDialog(job,changed);
    if (button.dataset.recordAction==='delete') deleteRecordDialog(job,()=>{location.hash=back;});
    if (button.dataset.recordAction==='task') taskDialog(job,changed);
  });
}

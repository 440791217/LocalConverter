'use strict';
(() => {
  const $ = id => document.getElementById(id);
  const token = document.querySelector('meta[name="localconverter-token"]').content;
  const heartbeatMS = Number(document.querySelector('meta[name="heartbeat-seconds"]').content) * 1000;
  let session = null, reader = null, heartbeat = null, photoURL = '', generation = 0, busy = false;
  const status = (message, error = false) => { $('status').textContent = message; $('status').dataset.error = String(error); };
  const updateButtons = () => { $('connect').disabled = busy; $('capture').disabled = busy || !session; $('disconnect').disabled = busy || !session; };
  async function request(path, method = 'GET', body) {
    const response = await fetch(path, { method, cache: 'no-store', headers: { Authorization: `Bearer ${token}`, ...(body ? { 'Content-Type': 'application/json' } : {}) }, body: body ? JSON.stringify(body) : undefined });
    if (!response.ok) { const data = await response.json().catch(() => ({})); throw new Error(data.error || `请求失败（${response.status}）`); }
    if (response.status === 204) return null;
    return response.headers.get('Content-Type').includes('image/jpeg') ? response.blob() : response.json();
  }
  function clearPhoto() { if (photoURL) URL.revokeObjectURL(photoURL); photoURL = ''; $('snapshot').hidden = true; $('snapshot').removeAttribute('src'); $('download').hidden = true; $('download').removeAttribute('href'); $('snapshot-empty').hidden = false; }
  async function closeSession() {
    const previous = session; session = null; ++generation; clearInterval(heartbeat); heartbeat = null;
    if (reader) reader.close(); reader = null; $('video').srcObject = null; $('video-empty').hidden = false; updateButtons();
    if (previous) await request(`/sessions/${previous.session_id}`, 'DELETE');
  }
  async function tools() {
    try { const result = await request('/tools'); $('tools').textContent = `FFmpeg：${result.ffmpeg ? '就绪' : '未找到'} · MediaMTX：${result.mediamtx ? '就绪' : '未找到'}`; }
    catch (error) { $('tools').textContent = error.message; }
  }
  $('camera-form').addEventListener('submit', async event => {
    event.preventDefault(); if (busy) return; busy = true; updateButtons();
    try {
      await closeSession(); clearPhoto(); status('正在连接摄像头…');
      const current = generation;
      const created = await request('/sessions', 'POST', { device_id: $('device-id').value.trim(), config_version: Number($('config-version').value), rtsp_url: $('rtsp-url').value.trim() });
      if (current !== generation) { await request(`/sessions/${created.session_id}`, 'DELETE'); return; }
      session = created;
      status('摄像头已连接，正在建立预览…');
      heartbeat = setInterval(async () => {
        const active = session; if (!active) return;
        try { const state = await request(`/sessions/${active.session_id}/heartbeat`, 'POST'); if (session === active && state.state !== 'ready') status('摄像头正在重连…'); }
        catch (error) { if (session === active) { status(error.message, true); closeSession().catch(() => {}); } }
      }, heartbeatMS);
      reader = new MediaMTXWebRTCReader({ url: new URL(created.preview_url, location.origin).href, token,
        onTrack: event => { if (session !== created) return; $('video').srcObject = event.streams[0]; $('video-empty').hidden = true; status('摄像头已连接，可以预览和拍照'); },
        onError: error => { if (session === created) status(`预览暂不可用：${String(error)}。请检查码流编码；仍可尝试拍照。`, true); }
      });
    } catch (error) { status(error.message, true); }
    finally { busy = false; updateButtons(); tools(); }
  });
  $('capture').addEventListener('click', async () => {
    if (!session || busy) return; const active = session; busy = true; updateButtons(); status('正在截图…');
    try { const blob = await request(`/sessions/${active.session_id}/snapshot`, 'POST'); if (session !== active) return; clearPhoto(); photoURL = URL.createObjectURL(blob); $('snapshot').src = photoURL; $('snapshot').hidden = false; $('snapshot-empty').hidden = true; $('download').href = photoURL; $('download').hidden = false; $('download').download = `camera-${Date.now()}.jpg`; status('截图成功，可下载图片'); }
    catch (error) { status(error.message, true); }
    finally { busy = false; updateButtons(); }
  });
  $('disconnect').addEventListener('click', async () => { busy = true; updateButtons(); try { await closeSession(); status('连接已释放，无其他使用者时会在空闲期后停止拉流'); } catch (error) { status(error.message, true); } finally { busy = false; updateButtons(); tools(); } });
  $('video').addEventListener('playing', () => { $('video-empty').hidden = true; });
  window.addEventListener('pagehide', () => { if (reader) reader.close(); if (session) fetch(`/sessions/${session.session_id}`, { method: 'DELETE', headers: { Authorization: `Bearer ${token}` }, keepalive: true }).catch(() => {}); });
  tools();
})();

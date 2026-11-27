package lanweb

// pageHTML is served at /<token>/. It is deliberately framework-free: one
// request, one script tag, no external assets, so it works on a LAN with no
// internet access.
const pageHTML = `<!doctype html>
<html lang="zh-CN"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>轻渡 Ferry · 浏览器收发</title>
<link rel="icon" type="image/svg+xml" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'%3E%3Cdefs%3E%3ClinearGradient id='g' x1='0' y1='0' x2='1' y2='1'%3E%3Cstop offset='0' stop-color='%231A82EB'/%3E%3Cstop offset='1' stop-color='%23005BB8'/%3E%3C/linearGradient%3E%3C/defs%3E%3Crect width='32' height='32' rx='8' fill='url(%23g)'/%3E%3Cpath d='M16 6.8L11.4 16.4L16 15.2Z' fill='%23FFFFFF'/%3E%3Cpath d='M16 6.8L16 15.2L20.6 16.4Z' fill='%23DBEAFE'/%3E%3Cpath d='M5.2 14.8L11.4 16.4L9.2 21.2Z' fill='%23EFF6FF'/%3E%3Cpath d='M5.2 14.8L16 15.2L16 21.6L9.2 21.2Z' fill='%23FFFFFF'/%3E%3Cpath d='M16 15.2L26.8 14.4L22.8 21.2L16 21.6Z' fill='%23DBEAFE'/%3E%3Cpath d='M20.6 16.4L26.8 14.4L16 15.2Z' fill='%23BFDBFE'/%3E%3Cpath d='M5.5 25.4C10.2 22.3 15.5 25.5 21.2 24.4C24 23.8 26.2 22.4 27.6 20.6C25.7 24.4 22.4 26.7 18.2 26.8C13.5 26.9 9.8 24.1 5.5 25.4Z' fill='%23B8DBFF'/%3E%3C/svg%3E">
<style>
  :root{
    color-scheme: light dark;
    --paper:#F5F5F7; --card:#FFFFFF; --paper-2:#EAEAEE; --paper-3:#DFDFE5;
    --ink:#1D1D1F; --ink-2:#515154; --ink-3:#86868B;
    --line:#D8D8DE; --line-soft:#E8E8ED;
    --accent:#0066CC; --accent-hover:#0055B3; --accent-text:#0066CC; --accent-ink:#FFFFFF; --accent-soft:#E8F2FC;
    --danger:#D92D20;
    --sans:"Segoe UI","PingFang SC","Microsoft YaHei UI",system-ui,-apple-system,sans-serif;
    --mono:"Cascadia Mono","JetBrains Mono",Consolas,ui-monospace,monospace;
  }
  @media (prefers-color-scheme: dark){
    :root{
      --paper:#0C0D10; --card:#15171C; --paper-2:#1F2229; --paper-3:#292D36;
      --ink:#EFF1F5; --ink-2:#A1A8B5; --ink-3:#6E7685;
      --line:#2A2E38; --line-soft:#20242D;
      --accent:#164EA6; --accent-hover:#1E5EC2; --accent-text:#5E95E6; --accent-ink:#F2F5FA; --accent-soft:#0E1C31;
      --danger:#FF5A52;
    }
    .logo{background:linear-gradient(135deg,#1E5EC2,#0E387D);box-shadow:0 4px 14px rgba(22,78,166,.38)}
  }
  *{box-sizing:border-box}
  html,body{margin:0}
  body{font-family:var(--sans);font-size:14px;line-height:1.5;color:var(--ink);
       background:var(--paper);-webkit-font-smoothing:antialiased}
  .wrap{width:min(920px,100% - 32px);margin:0 auto;padding:24px 0 48px}
  [hidden]{display:none!important}

  .top{display:flex;align-items:center;justify-content:space-between;gap:14px;flex-wrap:wrap;
       padding:14px 18px;background:var(--card);border:1px solid var(--line);border-radius:14px}
  .brand{display:flex;align-items:center;gap:12px}
  .logo{width:36px;height:36px;border-radius:11px;background:linear-gradient(135deg,#1A82EB,#005BB8);
        display:grid;place-items:center;flex:none;box-shadow:0 4px 12px rgba(0,102,204,.26)}
  .title{font-weight:700;font-size:16px;letter-spacing:.01em}
  .sub{font-size:12px;color:var(--ink-2)}
  .pill{display:inline-flex;align-items:center;gap:7px;padding:6px 12px;border-radius:999px;
        background:var(--accent-soft);color:var(--accent-text);font-size:12px;font-weight:600;white-space:nowrap}
  .pill i{width:7px;height:7px;border-radius:50%;background:currentColor;animation:breathe 1.6s ease-in-out infinite}
  @keyframes breathe{0%,100%{opacity:1}50%{opacity:.45}}

  .card{margin-top:16px;background:var(--card);border:1px solid var(--line);border-radius:16px;padding:20px}
  #drop{border:2px dashed var(--line);border-radius:12px;background:var(--paper-2);
        padding:30px 18px;text-align:center;color:var(--ink-2);cursor:pointer;transition:.15s ease}
  #drop:hover,#drop.hot{border-color:var(--accent-text);background:var(--accent-soft);color:var(--accent-text)}
  #drop b{display:block;font-size:15px;color:var(--ink);margin-bottom:4px}
  #drop.hot b{color:var(--accent-text)}
  .pick-btn{display:inline-block;margin-top:12px;padding:8px 18px;border-radius:8px;
            background:var(--accent);color:var(--accent-ink);font-weight:600;font-size:13px}
  #bar{height:6px;background:var(--line-soft);border-radius:999px;overflow:hidden;margin-top:8px}
  #bar>i{display:block;height:100%;width:0;background:var(--accent);transition:width .12s linear}
  .tx-box{margin-top:14px;padding:12px 14px;border-radius:11px;border:1px solid var(--line);background:var(--paper-2)}
  .tx-top{display:flex;align-items:center;justify-content:space-between;gap:10px;font-size:13px}
  .tx-name{font-weight:600;color:var(--ink);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;min-width:0}
  .tx-acts{display:flex;align-items:center;gap:6px;flex:none}
  .tx-pct{font-family:var(--mono);font-size:12px;font-weight:700;color:var(--accent-text);flex:none}
  .tx-btn{font:inherit;font-size:11px;font-weight:600;padding:3px 9px;border:1px solid var(--line);border-radius:7px;
          background:var(--card);color:var(--ink);cursor:pointer;white-space:nowrap}
  .tx-btn.danger{color:var(--danger)}
  .tx-btn:hover{background:var(--paper-3)}
  .tx-meta{margin-top:7px;font-family:var(--mono);font-size:12px;color:var(--ink-2);word-break:break-word}
  #msg{margin-top:10px;font-size:13px;color:var(--ink-2);white-space:pre-wrap}
  #msg:empty{display:none}

  .hdr{display:flex;align-items:center;justify-content:space-between;margin-bottom:12px}
  .hdr h2{margin:0;font-size:14px;font-weight:700;color:var(--ink)}
  .hdr span{font-family:var(--mono);font-size:12px;color:var(--ink-3)}
  .tw{overflow-x:auto}
  table{width:100%;border-collapse:collapse}
  th{font-size:11px;font-weight:600;letter-spacing:.06em;text-transform:uppercase;color:var(--ink-3);
     text-align:left;padding:0 10px 8px;border-bottom:1px solid var(--line)}
  th.n{text-align:right}
  td{padding:11px 10px;border-bottom:1px solid var(--line-soft);font-size:13px;vertical-align:middle}
  tbody tr:hover td{background:var(--paper-2)}
  td:first-child{word-break:break-all;font-weight:500}
  td:nth-child(2){font-family:var(--mono);font-size:12px;color:var(--ink-2);text-align:right;white-space:nowrap}
  td:nth-child(3){font-family:var(--mono);font-size:11px;color:var(--ink-3);white-space:nowrap}
  td:last-child{text-align:right;white-space:nowrap}
  .dl{display:inline-block;padding:6px 14px;border-radius:8px;background:var(--accent);
      color:var(--accent-ink);text-decoration:none;font-size:12px;font-weight:600}
  .dl:hover{background:var(--accent-hover)}
  .empty{margin:0;padding:24px 12px;text-align:center;color:var(--ink-3);font-size:13px}
  .card:has(#rows:empty) table{display:none}
  .card:has(#rows:not(:empty)) .empty{display:none}

  .foot{margin-top:20px;display:flex;justify-content:space-between;gap:12px;flex-wrap:wrap;
        font-size:12px;color:var(--ink-3)}
</style></head>
<body>
<div class="wrap">
  <header class="top">
    <div class="brand">
      <div class="logo" aria-hidden="true"><svg width="21" height="21" viewBox="0 0 32 32" fill="none"><path d="M16 6.8L11.4 16.4L16 15.2Z" fill="#FFFFFF"/><path d="M16 6.8L16 15.2L20.6 16.4Z" fill="#DBEAFE"/><path d="M5.2 14.8L11.4 16.4L9.2 21.2Z" fill="#EFF6FF"/><path d="M5.2 14.8L16 15.2L16 21.6L9.2 21.2Z" fill="#FFFFFF"/><path d="M16 15.2L26.8 14.4L22.8 21.2L16 21.6Z" fill="#DBEAFE"/><path d="M20.6 16.4L26.8 14.4L16 15.2Z" fill="#BFDBFE"/><path d="M5.5 25.4C10.2 22.3 15.5 25.5 21.2 24.4C24 23.8 26.2 22.4 27.6 20.6C25.7 24.4 22.4 26.7 18.2 26.8C13.5 26.9 9.8 24.1 5.5 25.4Z" fill="#B8DBFF"/></svg></div>
      <div>
        <div class="title">轻渡 Ferry · 浏览器收发</div>
        <div class="sub" id="peer">局域网极速直连（不经公网服务器）</div>
      </div>
    </div>
    <span class="pill"><i aria-hidden="true"></i>LAN 局域网直连</span>
  </header>

  <section class="card" id="upload-card">
    <div id="drop">
      <b>把文件拖到这里，或点击选择文件</b>
      <div>直传到对方设备，自动计算 SHA-256 校验</div>
      <span class="pick-btn">选择文件上传</span>
      <input id="pick" type="file" multiple style="display:none">
    </div>
    <div class="tx-box" id="tx-box" hidden>
      <div class="tx-top"><span class="tx-name" id="tx-name"></span><div class="tx-acts"><span class="tx-pct" id="tx-pct"></span><button type="button" class="tx-btn" id="tx-pause">暂停</button><button type="button" class="tx-btn danger" id="tx-cancel">取消</button></div></div>
      <div id="bar"><i></i></div>
      <div class="tx-meta" id="tx-meta"></div>
    </div>
    <div id="msg"></div>
  </section>

  <section class="card">
    <div class="hdr">
      <h2>会话文件列表</h2>
      <span id="count">0 个文件</span>
    </div>
    <div class="tw">
      <table><thead><tr><th>文件名</th><th class="n">大小</th><th>SHA-256</th><th></th></tr></thead><tbody id="rows"></tbody></table>
    </div>
    <p class="empty" id="empty-tip">暂无文件 · 等待文件上传或刷新</p>
  </section>

  <footer class="foot">
    <span>轻渡 Ferry · 同 Wi-Fi 直连</span>
    <span id="tok"></span>
  </footer>
</div>
<script>
(function(){
  var base = location.pathname.replace(/\/$/, "");
  document.getElementById("tok").textContent = location.host;
  var rows = document.getElementById("rows");
  var msg = document.getElementById("msg");
  var txBox = document.getElementById("tx-box");
  var txName = document.getElementById("tx-name");
  var txPct = document.getElementById("tx-pct");
  var txMeta = document.getElementById("tx-meta");
  var fill = document.querySelector("#bar>i");
  var uploadCard = document.getElementById("upload-card");
  function fmt(n){ if(n<1024)return n+" B"; if(n<1048576)return (n/1024).toFixed(1)+" KB"; if(n<1073741824)return (n/1048576).toFixed(1)+" MB"; return (n/1073741824).toFixed(2)+" GB"; }
  function fmtSpeed(bps){ if(!bps||bps<=0)return ""; if(bps<1024)return Math.round(bps)+" B/s"; if(bps<1048576)return (bps/1024).toFixed(1)+" KB/s"; if(bps<1073741824)return (bps/1048576).toFixed(1)+" MB/s"; return (bps/1073741824).toFixed(2)+" GB/s"; }
  function fmtETA(sec){ if(sec==null||!isFinite(sec)||sec<=0||sec>=86400)return ""; sec=Math.max(1,Math.round(sec)); if(sec<60)return "剩余 "+sec+"s"; return "剩余 "+Math.floor(sec/60)+"m"+String(sec%60).padStart(2,"0")+"s"; }
  function addRow(f){
    var tr=document.createElement("tr");
    var td1=document.createElement("td"); td1.textContent=f.name;
    var td2=document.createElement("td"); td2.textContent=fmt(f.size);
    var td3=document.createElement("td"); td3.textContent=(f.sha256||"").slice(0,12)+"…";
    var td4=document.createElement("td");
    var a=document.createElement("a"); a.className="dl"; a.href=base+"/f/"+encodeURIComponent(f.name); a.textContent="下载";
    td4.appendChild(a);
    tr.appendChild(td1);tr.appendChild(td2);tr.appendChild(td3);tr.appendChild(td4);rows.appendChild(tr);
  }
  var busy = false;
  var upPaused = false;
  var upCancelled = false;
  var upAbort = null;
  var upSessionId = null;
  var pauseWake = null;
  var pauseBtn = document.getElementById("tx-pause");
  var cancelBtn = document.getElementById("tx-cancel");
  function wakePause(){
    if(pauseWake){ var fn = pauseWake; pauseWake = null; fn(); }
  }
  function waitWhilePaused(){
    return new Promise(function(resolve){
      if(!upPaused || upCancelled){ resolve(); return; }
      pauseWake = resolve;
    });
  }
  function sleepMs(ms){ return new Promise(function(r){ setTimeout(r, ms); }); }
  function load(){
    fetch(base+"/api/list",{cache:"no-store"}).then(function(r){return r.json()}).then(function(j){
      if(j.name){ document.getElementById("peer").textContent = "已连接设备：" + j.name + "（局域网直连）"; }
      if(j.allow_upload === false){ uploadCard.hidden = true; }
      var list = j.files || [];
      document.getElementById("count").textContent = list.length + " 个文件";
      rows.innerHTML=""; list.forEach(addRow);
    }).catch(function(e){ msg.textContent="列表失败："+e; });
  }
  function showTx(label, done, total, bps, note){
    txBox.hidden = false;
    txName.textContent = label;
    var pct = total > 0 ? Math.max(0, Math.min(100, Math.round(100 * done / total))) : 100;
    txPct.textContent = pct + "%";
    fill.style.width = pct + "%";
    var parts = [total > 0 ? (fmt(Math.min(done, total)) + " / " + fmt(total)) : fmt(done)];
    if(bps > 0) parts.push(fmtSpeed(bps));
    if(total > 0 && done > 0 && done < total && bps > 0){
      var eta = fmtETA((total - done) / bps);
      if(eta) parts.push(eta);
    }
    if(note) parts.push(note);
    txMeta.textContent = parts.join(" · ");
  }
  async function uploadOne(f, idx, totalFiles){
    var fsize = f.size || 0;
    var label = (totalFiles > 1 ? ("(" + idx + "/" + totalFiles + ") ") : "") + f.name;
    var ac = new AbortController();
    upAbort = ac;
    var initResp = await fetch(base + "/api/upload/init", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({name: f.name, size: fsize, batch_count: totalFiles}),
      signal: ac.signal
    });
    if(!initResp.ok){
      throw new Error("初始化失败 " + initResp.status + " " + (await initResp.text()));
    }
    var initData = await initResp.json();
    upSessionId = initData.id;
    var CHUNK = 512 * 1024;
    var off = 0;
    var start = performance.now();
    var samples = [{ t: start, b: 0 }];
    var lastUi = 0;
    showTx("上传中：" + label, 0, fsize, 0);
    while(off < fsize){
      if(upCancelled) throw new Error("__CANCELLED__");
      if(upPaused){
        showTx("已暂停：" + label, off, fsize, 0, "已暂停");
        await waitWhilePaused();
        if(upCancelled) throw new Error("__CANCELLED__");
        samples = [{ t: performance.now(), b: off }];
      }
      var end = Math.min(off + CHUNK, fsize);
      var slice = f.slice(off, end);
      ac = new AbortController();
      upAbort = ac;
      var resp = await fetch(base + "/api/upload/chunk?id=" + encodeURIComponent(upSessionId) + "&offset=" + off, {
        method: "POST",
        body: slice,
        signal: ac.signal
      });
      if(resp.status === 409){
        var st = {};
        try{ st = await resp.json(); }catch(e){}
        if(st.cancelled) throw new Error("__CANCELLED__");
        if(st.paused){
          showTx("已暂停：" + label, off, fsize, 0, "对端已暂停");
          while(!upCancelled){
            await sleepMs(450);
            if(upCancelled) throw new Error("__CANCELLED__");
            var sr = await fetch(base + "/api/upload/status?id=" + encodeURIComponent(upSessionId), {cache:"no-store"});
            if(!sr.ok) throw new Error("__CANCELLED__");
            var sj = await sr.json();
            if(sj.cancelled) throw new Error("__CANCELLED__");
            if(!sj.paused) break;
          }
          samples = [{ t: performance.now(), b: off }];
          continue;
        }
      }
      if(!resp.ok){
        throw new Error("分块失败 " + resp.status + " " + (await resp.text()));
      }
      off = end;
      var now = performance.now();
      if(now - lastUi >= 100 || off >= fsize){
        lastUi = now;
        samples.push({ t: now, b: off });
        while(samples.length > 2 && now - samples[0].t > 2000) samples.shift();
        var dt = (now - samples[0].t) / 1000, db = off - samples[0].b;
        var bps = (dt >= 0.15 && db > 0) ? (db / dt) : ((now - start) >= 150 && off > 0 ? (off / ((now - start) / 1000)) : 0);
        showTx("上传中：" + label, off, fsize, bps, off >= fsize ? "对方校验保存中…" : "");
      }
    }
    if(upCancelled) throw new Error("__CANCELLED__");
    pauseBtn.disabled = true;
    ac = new AbortController();
    upAbort = ac;
    var finResp = await fetch(base + "/api/upload/finish?id=" + encodeURIComponent(upSessionId), {
      method: "POST",
      signal: ac.signal
    });
    upSessionId = null;
    upAbort = null;
    if(!finResp.ok){
      throw new Error("收尾失败 " + finResp.status + " " + (await finResp.text()));
    }
    var totalDt = (performance.now() - start) / 1000;
    var avgSpd = (totalDt >= 0.1 && fsize > 0) ? fmtSpeed(fsize / totalDt) : "";
    msg.textContent = "上传完成：" + f.name + (avgSpd ? "（平均 " + avgSpd + "）" : "");
  }
  async function send(files){
    if(!files || !files.length) return;
    if(busy){ msg.textContent="正在上传中，请稍候"; return; }
    var list = Array.prototype.slice.call(files);
    var totalFiles = list.length;
    busy = true;
    upPaused = false;
    upCancelled = false;
    upSessionId = null;
    upAbort = null;
    pauseBtn.textContent = "暂停";
    pauseBtn.disabled = false;
    msg.textContent = "";
    try{
      for(var i = 0; i < totalFiles; i++){
        pauseBtn.disabled = false;
        await uploadOne(list[i], i + 1, totalFiles);
      }
      if(totalFiles > 1) msg.textContent = "全部上传完成（共 " + totalFiles + " 个文件）";
    }catch(e){
      if(upSessionId){
        fetch(base + "/api/upload/cancel?id=" + encodeURIComponent(upSessionId), {method: "POST"}).catch(function(){});
        upSessionId = null;
      }
      if(upCancelled || (e && (e.name === "AbortError" || e.message === "__CANCELLED__"))){
        msg.textContent = "已取消上传";
      }else{
        msg.textContent = "上传失败：" + (e && e.message ? e.message : String(e));
      }
    }finally{
      busy = false;
      upPaused = false;
      upAbort = null;
      wakePause();
      txBox.hidden = true;
      load();
    }
  }
  pauseBtn.onclick = function(){
    if(!busy || upCancelled) return;
    upPaused = !upPaused;
    pauseBtn.textContent = upPaused ? "继续" : "暂停";
    if(!upPaused) wakePause();
  };
  cancelBtn.onclick = function(){
    if(!busy) return;
    upCancelled = true;
    upPaused = false;
    wakePause();
    if(upAbort) upAbort.abort();
    if(upSessionId){
      fetch(base + "/api/upload/cancel?id=" + encodeURIComponent(upSessionId), {method: "POST"}).catch(function(){});
      upSessionId = null;
    }
  };
  var drop=document.getElementById("drop");
  var pick=document.getElementById("pick");
  drop.onclick=function(){ pick.click(); };
  pick.onchange=function(){ send(pick.files); pick.value=""; };
  ["dragenter","dragover"].forEach(function(ev){ drop.addEventListener(ev,function(e){ e.preventDefault(); drop.classList.add("hot"); }); });
  ["dragleave","drop"].forEach(function(ev){ drop.addEventListener(ev,function(e){ e.preventDefault(); drop.classList.remove("hot"); }); });
  drop.addEventListener("drop",function(e){ if(e.dataTransfer) send(e.dataTransfer.files); });
  load();
})();
</script>
</body></html>`

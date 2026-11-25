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
<style>
  :root{
    color-scheme: light dark;
    --paper:#F7F8F7; --card:#FFFFFF; --paper-2:#EEF2F0; --paper-3:#E3EAE6;
    --ink:#121F1A; --ink-2:#4E5C56; --ink-3:#7A8782;
    --line:#DCE3DF; --line-soft:#EBF0ED;
    --accent:#0F8F76; --accent-hover:#0B7A64; --accent-ink:#FFFFFF; --accent-soft:#E4F4EF;
    --danger:#C24136;
    --sans:system-ui,-apple-system,"Segoe UI","PingFang SC","Microsoft YaHei UI",sans-serif;
    --mono:ui-monospace,"Cascadia Mono",Consolas,"SF Mono",monospace;
  }
  @media (prefers-color-scheme: dark){
    :root{
      --paper:#0D1210; --card:#141C19; --paper-2:#1A2420; --paper-3:#22302B;
      --ink:#ECF2EF; --ink-2:#A4B3AC; --ink-3:#75857E;
      --line:#24312C; --line-soft:#1D2723;
      --accent:#2BB598; --accent-hover:#38C8AA; --accent-ink:#091411; --accent-soft:#152E28;
      --danger:#EF7E74;
    }
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
  .logo{width:34px;height:34px;border-radius:10px;background:var(--accent);color:var(--accent-ink);
        display:grid;place-items:center;font-weight:700;font-size:16px;flex:none}
  .title{font-weight:700;font-size:16px;letter-spacing:.01em}
  .sub{font-size:12px;color:var(--ink-2)}
  .pill{display:inline-flex;align-items:center;gap:7px;padding:6px 12px;border-radius:999px;
        background:var(--accent-soft);color:var(--accent);font-size:12px;font-weight:600;white-space:nowrap}
  .pill i{width:7px;height:7px;border-radius:50%;background:currentColor;animation:breathe 1.6s ease-in-out infinite}
  @keyframes breathe{0%,100%{opacity:1}50%{opacity:.45}}

  .card{margin-top:16px;background:var(--card);border:1px solid var(--line);border-radius:16px;padding:20px}
  #drop{border:2px dashed var(--line);border-radius:12px;background:var(--paper-2);
        padding:30px 18px;text-align:center;color:var(--ink-2);cursor:pointer;transition:.15s ease}
  #drop:hover,#drop.hot{border-color:var(--accent);background:var(--accent-soft);color:var(--accent)}
  #drop b{display:block;font-size:15px;color:var(--ink);margin-bottom:4px}
  #drop.hot b{color:var(--accent)}
  .pick-btn{display:inline-block;margin-top:12px;padding:8px 18px;border-radius:8px;
            background:var(--accent);color:var(--accent-ink);font-weight:600;font-size:13px}
  #bar{height:6px;background:var(--line-soft);border-radius:999px;overflow:hidden;margin-top:8px}
  #bar>i{display:block;height:100%;width:0;background:var(--accent);transition:width .12s linear}
  .tx-box{margin-top:14px;padding:12px 14px;border-radius:11px;border:1px solid var(--line);background:var(--paper-2)}
  .tx-top{display:flex;align-items:center;justify-content:space-between;gap:10px;font-size:13px}
  .tx-name{font-weight:600;color:var(--ink);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;min-width:0}
  .tx-pct{font-family:var(--mono);font-size:12px;font-weight:700;color:var(--accent);flex:none}
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
      <div class="logo">渡</div>
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
      <div class="tx-top"><span class="tx-name" id="tx-name"></span><span class="tx-pct" id="tx-pct"></span></div>
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
  function send(files){
    if(!files || !files.length) return;
    if(busy){ msg.textContent="正在上传中，请稍候"; return; }
    var list = Array.prototype.slice.call(files);
    var totalFiles = list.length;
    var idx = 0;
    busy = true;
    msg.textContent = "";
    function next(){
      if(idx >= list.length){
        busy = false;
        txBox.hidden = true;
        msg.textContent = totalFiles > 1 ? ("全部上传完成（共 " + totalFiles + " 个文件）") : msg.textContent;
        load();
        return;
      }
      var f = list[idx++];
      var fsize = f.size || 0;
      var label = "上传中：" + (totalFiles > 1 ? ("(" + idx + "/" + totalFiles + ") ") : "") + f.name;
      var start = performance.now();
      var samples = [{ t: start, b: 0 }];
      var fd = new FormData();
      fd.append("file", f, f.name);
      showTx(label, 0, fsize, 0);
      var xhr = new XMLHttpRequest();
      xhr.open("POST", base+"/upload");
      xhr.setRequestHeader("X-File-Size", String(fsize));
      xhr.upload.onprogress = function(e){
        var total = fsize > 0 ? fsize : (e.lengthComputable ? e.total : 0);
        var loaded = total > 0 ? Math.min(total, e.loaded) : e.loaded;
        var now = performance.now();
        samples.push({ t: now, b: loaded });
        while(samples.length > 2 && now - samples[0].t > 2000) samples.shift();
        var dt = (now - samples[0].t) / 1000, db = loaded - samples[0].b;
        var bps = (dt >= 0.15 && db > 0) ? (db / dt) : ((now - start) >= 150 && loaded > 0 ? (loaded / ((now - start) / 1000)) : 0);
        var note = (total === 0 || loaded >= total) ? "对方校验保存中…" : "";
        showTx(label, loaded, total, bps, note);
      };
      xhr.onload = function(){
        if(xhr.status !== 200){
          busy = false; txBox.hidden = true;
          msg.textContent = "上传失败：" + xhr.status + " " + (xhr.responseText || "");
          return;
        }
        var totalDt = (performance.now() - start) / 1000;
        var avgSpd = (totalDt >= 0.1 && fsize > 0) ? fmtSpeed(fsize / totalDt) : "";
        msg.textContent = "上传完成：" + f.name + (avgSpd ? "（平均 " + avgSpd + "）" : "");
        next();
      };
      xhr.onerror = function(){ busy = false; txBox.hidden = true; msg.textContent = "上传失败：网络错误"; };
      xhr.onabort = function(){ busy = false; txBox.hidden = true; };
      xhr.send(fd);
    }
    next();
  }
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

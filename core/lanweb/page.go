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
    --paper:#F7F6F0; --paper-2:#F0EEE7; --paper-3:#E6E4DB;
    --ink:#16211C; --ink-2:#505955; --ink-3:#7E8783;
    --line:#D6D4CC; --line-soft:#E1E0D8;
    --accent:#2C5C49; --accent-ink:#FBFAF7; --accent-soft:#D6EDE2;
    --danger:#B64036; --amber:#915C08; --violet:#665189;
    --serif:"Iowan Old Style","Palatino Linotype","Songti SC","SimSun",serif;
    --sans:system-ui,"Segoe UI","Microsoft YaHei UI",sans-serif;
    --mono:ui-monospace,"Cascadia Mono",Consolas,"SF Mono",monospace;
  }
  @media (prefers-color-scheme: dark){
    :root{
      --paper:#0E1411; --paper-2:#161E1A; --paper-3:#1E2823;
      --ink:#E8E6DF; --ink-2:#ABA9A1; --ink-3:#7E7C74;
      --line:#2B3430; --line-soft:#222A26;
      --accent:#89C7AB; --accent-ink:#111916; --accent-soft:#1B2F26;
      --danger:#ED8174; --amber:#E3B667; --violet:#BEA3DF;
    }
  }
  *{box-sizing:border-box}
  html,body{margin:0}
  body{font-family:var(--sans);font-size:13px;line-height:19px;color:var(--ink);
       background:var(--paper);-webkit-font-smoothing:antialiased}
  .wrap{width:min(1080px,100% - 48px);margin:0 auto;padding:20px 0 44px}
  [hidden]{display:none!important}

  /* masthead */
  .sheet{display:flex;align-items:center;justify-content:space-between;gap:16px;
         padding-bottom:14px;border-bottom:1px solid var(--line)}
  h1{font-family:var(--serif);font-weight:500;font-size:21px;line-height:28px;
     margin:0;letter-spacing:.02em}
  .addr{margin-top:2px;font-family:var(--mono);font-size:11px;line-height:16px;
        letter-spacing:.04em;color:var(--ink-3);word-break:break-all}
  .state{display:flex;align-items:center;gap:12px;white-space:nowrap}
  .live{display:inline-flex;align-items:center;gap:6px;font-family:var(--mono);
        font-size:11px;line-height:16px;letter-spacing:.06em;text-transform:uppercase;
        color:var(--accent)}
  .live i{width:7px;height:7px;border-radius:50%;background:currentColor;flex:none;
        animation:breathe 1.6s ease-in-out infinite}
  @keyframes breathe{0%,100%{opacity:1}50%{opacity:.55}}
  @media (prefers-reduced-motion:reduce){.live i{animation:none}}
  /* path stamp: colour + 1px border + word */
  .stamp{display:inline-flex;align-items:center;height:18px;padding:0 6px;
         border:1px solid var(--accent);border-radius:3px;font-family:var(--mono);
         font-size:11px;line-height:16px;letter-spacing:.06em;text-transform:uppercase;
         color:var(--accent);white-space:nowrap}

  main{margin-top:20px}
  #drop{border:1px dashed var(--line);border-radius:7px;background:var(--paper-2);
        padding:26px 18px;text-align:center;color:var(--ink-2);font-size:12px;
        line-height:17px;cursor:pointer}
  #drop b{display:block;font-family:var(--serif);font-weight:500;font-size:16px;
          line-height:22px;color:var(--ink);margin-bottom:4px}
  #drop.hot{border-style:solid;border-color:var(--accent);background:var(--accent-soft);color:var(--accent)}
  #drop.hot b{color:var(--accent)}
  #bar{height:4px;background:var(--line-soft);border-radius:2px;overflow:hidden;
       margin:14px 0 0;display:none}
  #bar>i{display:block;height:100%;width:0;background:var(--accent);
       transition:width .15s linear}
  #msg{margin-top:10px;font-size:12px;line-height:17px;color:var(--ink-2);white-space:pre-wrap}
  #msg:empty{display:none}

  .tw{margin-top:18px;overflow-x:auto}
  table{width:100%;border-collapse:collapse}
  th{font-family:var(--mono);font-size:11px;line-height:16px;font-weight:500;
     letter-spacing:.1em;text-transform:uppercase;color:var(--ink-3);text-align:left;
     padding:0 10px 7px;border-bottom:1px solid var(--line)}
  th.n{text-align:right}
  td{padding:9px 10px;border-bottom:1px solid var(--line-soft);font-size:13px;
     line-height:19px;vertical-align:top}
  tbody tr:hover td{background:var(--paper-2)}
  td:first-child{word-break:break-all}
  td:nth-child(2){font-family:var(--mono);font-size:12px;font-variant-numeric:tabular-nums;
     color:var(--ink-2);text-align:right;white-space:nowrap}
  td:nth-child(3){min-width:14em}
  td:last-child{text-align:right;white-space:nowrap}
  code{font-family:var(--mono);font-size:11px;line-height:16px;color:var(--ink-3);
       word-break:break-all;letter-spacing:.02em}
  a{color:var(--accent);text-decoration:none;border-bottom:1px solid var(--line);padding-bottom:1px}
  a:hover{border-bottom-color:var(--accent)}
  .empty{margin:6px 0 0;padding:22px 12px 16px;text-align:center;color:var(--ink-3);
         font-size:12px;line-height:17px}
  main:has(#rows:empty) table{display:none}
  main:has(#rows:not(:empty)) .empty{display:none}

  .foot{margin-top:26px;padding-top:12px;border-top:1px solid var(--line-soft);
        display:flex;justify-content:space-between;gap:12px;flex-wrap:wrap;
        font-family:var(--mono);font-size:11px;line-height:16px;letter-spacing:.06em;
        text-transform:uppercase;color:var(--ink-3)}

  @media (max-width:720px){
    .wrap{width:min(1080px,100% - 32px);padding:16px 0 36px}
    .sheet{flex-direction:column;align-items:flex-start;gap:10px}
    h1{font-size:19px;line-height:26px}
    #drop{padding:22px 14px}
  }
</style></head>
<body>
<div class="wrap">
  <header class="sheet">
    <div>
      <h1>轻渡 Ferry · 浏览器收发</h1>
      <div class="addr"><code id="tok"></code></div>
    </div>
    <div class="state">
      <span class="live"><i aria-hidden="true"></i>接收中</span>
      <span class="stamp">LAN</span>
    </div>
  </header>
  <main>
    <div id="drop">把文件拖到这里，或点击选择文件<div><small>上传后显示服务端计算的 SHA-256</small></div>
      <input id="pick" type="file" multiple style="display:none">
    </div>
    <div id="bar"><i></i></div>
    <div id="msg"></div>
    <div class="tw">
      <table><thead><tr><th>文件</th><th class="n">大小</th><th>SHA-256</th><th></th></tr></thead><tbody id="rows"></tbody></table>
    </div>
    <p class="empty">还没有文件 · 把文件拖进上面的区域</p>
  </main>
  <footer class="foot">
    <span>轻渡 Ferry · 局域网直连</span>
    <span>传输仅在本机与设备之间</span>
  </footer>
</div>
<script>
(function(){
  var base = location.pathname.replace(/\/$/, "");
  document.getElementById("tok").textContent = base;
  var rows = document.getElementById("rows");
  var msg = document.getElementById("msg");
  var bar = document.getElementById("bar");
  var fill = bar.querySelector("i");
  function fmt(n){ if(n<1024)return n+" B"; if(n<1048576)return (n/1024).toFixed(1)+" KiB"; if(n<1073741824)return (n/1048576).toFixed(1)+" MiB"; return (n/1073741824).toFixed(2)+" GiB"; }
  function addRow(f){
    var tr=document.createElement("tr");
    var td1=document.createElement("td"); td1.textContent=f.name;
    var td2=document.createElement("td"); td2.textContent=fmt(f.size);
    var td3=document.createElement("td"); var c=document.createElement("code"); c.textContent=f.sha256; td3.appendChild(c);
    var td4=document.createElement("td"); var a=document.createElement("a"); a.href=base+"/f/"+encodeURIComponent(f.name); a.textContent="下载"; td4.appendChild(a);
    tr.appendChild(td1);tr.appendChild(td2);tr.appendChild(td3);tr.appendChild(td4);rows.appendChild(tr);
  }
  var busy = false;
  function load(){
    fetch(base+"/api/list",{cache:"no-store"}).then(function(r){return r.json()}).then(function(j){
      rows.innerHTML=""; (j.files||[]).forEach(addRow);
    }).catch(function(e){ msg.textContent="列表失败："+e; });
  }
  function send(files){
    if(!files || !files.length) return;
    if(busy){ msg.textContent="正在上传中，请稍候"; return; }
    busy = true;
    var fd=new FormData();
    for(var i=0;i<files.length;i++) fd.append("file", files[i], files[i].name);
    msg.textContent=""; bar.style.display="block"; fill.style.width="0%";
    var xhr=new XMLHttpRequest();
    xhr.open("POST", base+"/upload");
    xhr.upload.onprogress=function(e){ if(e.lengthComputable) fill.style.width=(100*e.loaded/e.total).toFixed(1)+"%"; };
    xhr.onload=function(){
      busy = false;
      bar.style.display="none";
      if(xhr.status!==200){ msg.textContent="上传失败："+xhr.status+" "+(xhr.responseText||""); return; }
      msg.textContent="上传完成"; load();
    };
    xhr.onerror=function(){ busy=false; bar.style.display="none"; msg.textContent="上传失败：网络错误"; };
    xhr.onabort=function(){ busy=false; bar.style.display="none"; };
    xhr.send(fd);
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

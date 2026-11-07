package lanweb

// pageHTML is served at /<token>/. It is deliberately framework-free: one
// request, one script tag, no external assets, so it works on a LAN with no
// internet access.
const pageHTML = `<!doctype html>
<html lang="zh-CN"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>ft · 浏览器收发</title>
<style>
 body{font:15px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;margin:0;background:#f6f7f9;color:#1c1d21}
 header{padding:14px 18px;background:#fff;border-bottom:1px solid #e6e8ec;font-weight:600}
 main{max-width:760px;margin:0 auto;padding:18px}
 #drop{border:2px dashed #b9c0cc;border-radius:12px;padding:36px 18px;text-align:center;color:#5a6472;background:#fff;cursor:pointer}
 #drop.hot{border-color:#3b82f6;color:#1d4ed8;background:#eff6ff}
 #bar{height:6px;background:#e6e8ec;border-radius:4px;overflow:hidden;margin:14px 0;display:none}
 #bar>i{display:block;height:100%;width:0;background:#3b82f6;transition:width .15s}
 table{width:100%;border-collapse:collapse;margin-top:18px;background:#fff;border-radius:10px;overflow:hidden}
 th,td{padding:9px 12px;text-align:left;border-bottom:1px solid #eef0f3;font-size:14px;vertical-align:top}
 th{background:#fafbfc;color:#5a6472;font-weight:600}
 code{font:12px/1.4 ui-monospace,Consolas,monospace;color:#6b7280;word-break:break-all}
 a{color:#1d4ed8;text-decoration:none}
 #msg{margin-top:10px;font-size:13px;color:#b91c1c;white-space:pre-wrap}
 small{color:#8b93a1}
</style></head>
<body>
<header>ft · 浏览器收发 <small id="tok"></small></header>
<main>
  <div id="drop">把文件拖到这里，或点击选择文件<div><small>上传后显示服务端计算的 SHA-256</small></div>
    <input id="pick" type="file" multiple style="display:none">
  </div>
  <div id="bar"><i></i></div>
  <div id="msg"></div>
  <table><thead><tr><th>文件</th><th>大小</th><th>SHA-256</th><th></th></tr></thead><tbody id="rows"></tbody></table>
</main>
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
  function load(){
    fetch(base+"/api/list").then(function(r){return r.json()}).then(function(j){
      rows.innerHTML=""; (j.files||[]).forEach(addRow);
    }).catch(function(e){ msg.textContent="列表失败："+e; });
  }
  function send(files){
    if(!files || !files.length) return;
    var fd=new FormData();
    for(var i=0;i<files.length;i++) fd.append("file", files[i], files[i].name);
    msg.textContent=""; bar.style.display="block"; fill.style.width="0%";
    var xhr=new XMLHttpRequest();
    xhr.open("POST", base+"/upload");
    xhr.upload.onprogress=function(e){ if(e.lengthComputable) fill.style.width=(100*e.loaded/e.total).toFixed(1)+"%"; };
    xhr.onload=function(){
      bar.style.display="none";
      if(xhr.status!==200){ msg.textContent="上传失败："+xhr.status+" "+(xhr.responseText||""); return; }
      msg.textContent="上传完成"; load();
    };
    xhr.onerror=function(){ bar.style.display="none"; msg.textContent="上传失败：网络错误"; };
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

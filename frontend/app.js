const $ = (id) => document.getElementById(id);
const Go = () => window.go.main.App;

// ---- 窗口控制 ----
$("btn-min").onclick = () => window.runtime.WindowMinimise();
// 注：wails v2.15 的 runtime 绑定没有 WindowClose（调用会抛 TypeError），× 走与「退出」相同的流程。
// 标题栏拖动不用手写：CSS 里 --wails-draggable: drag 命中 wails 内置机制，
// mousedown/mousemove 由 wails 的 runtime JS 处理，在 UI 线程 PostMessage(WM_NCLBUTTONDOWN, HTCAPTION)
// 触发 Windows 原生移动循环 —— 与资源管理器窗口同一套系统机制。
$("btn-close").onclick = doQuit;

// ---- 全局状态（Task 9/10/11 扩展）----
const state = { busy: false, waiting: false };
function setStatus(stage, text, canSkip, canStop) {
  state.waiting = stage === "starting";
  state.busy = stage === "starting";
  $("status-text").textContent = text;
  $("btn-skip").classList.toggle("hidden", !canSkip);
  // 注：三个停止按钮不再由事件驱动，改为下方轮询 Status() 按真实运行状态实时显隐。
}
window.runtime.EventsOn("status", (st) => {
  setStatus(st.stage, st.text, st.canSkip, st.canStop);
  if (st.stage === "ready") Go().OpenHarnessBrowser().catch(e => alert("打开 DeepSeek Harness 页面失败：" + e)); // 模型流程就绪 → 独立 Chrome 页面
  updateLaunchButtons(); // busy 状态变化 → 刷新启动按钮可用性
});

// ---- 电脑信息（仅启动时获取一次，不实时刷新）----
async function refreshSysInfo() {
  try {
    const s = await Go().GetSysInfo();
    const box = $("si-rows");
    box.innerHTML = "";
    for (const row of s.rows || []) {
      // 多值行（显卡/磁盘/显示器）：子项用「＋」连成一行，
      // 形如「主硬盘：A（1000GB）+ B（2048GB）」
      let value = row.value;
      if (row.subs && row.subs.length) {
        value = row.subs
          .map(s => (s.label ? s.label + "：" + (s.value || "") : (s.value || "")))
          .filter(x => x)
          .join(" + ");
      }
      const r = document.createElement("div");
      r.className = "kv";
      const l = document.createElement("span");
      l.textContent = row.label;
      const v = document.createElement("b");
      v.textContent = value || "—";
      r.appendChild(l); r.appendChild(v);
      box.appendChild(r);
    }
  } catch (e) { /* 单项失败保持「—」 */ }
}
refreshSysInfo(); // 只取一次

// ---- 预设列表（Task 9 填充 refreshPresets）----
let lastPresets = [];
let selectedPath = null;
function renderPresets(list) {
  lastPresets = list;
  const ul = $("presets");
  ul.innerHTML = "";
  if (!list.length) {
    const d = document.createElement("li");
    d.className = "p-empty";
    d.textContent = "暂无预设 —— 点「＋ 添加新的 bat」选择启动脚本";
    ul.appendChild(d);
    selectedPath = null;
    updateLaunchButtons();
    return;
  }
  // 必须显式点行选中（不自动选中）；再点一次取消选中；选中项被删/变缺失时取消选中 → 启动按钮置灰。
  if (selectedPath && !list.some(p => p.path === selectedPath && p.exists)) {
    selectedPath = null;
  }
  for (const p of list) {
    const li = document.createElement("li");
    li.className = (p.path === selectedPath ? "selected" : "") + (p.exists ? "" : " missing");
    li.onclick = () => { if (p.exists) { selectedPath = (selectedPath === p.path) ? null : p.path; renderPresets(lastPresets); } };
    const info = document.createElement("div");
    const n = document.createElement("div");
    n.className = "p-name";
    n.textContent = p.name;
    const sub = document.createElement("div");
    sub.className = "p-sub";
    sub.textContent = p.exists ? (p.subtitle || p.path) : "文件缺失：" + p.path;
    info.appendChild(n); info.appendChild(sub);
    const del = document.createElement("button");
    del.className = "ghost tiny";
    del.textContent = "删除";
    del.onclick = async (e) => {
      e.stopPropagation();
      if (!confirm("删除预设「" + p.name + "」？\n（只从列表移除，不删除 bat 文件）")) return;
      try {
        await Go().RemoveBat(p.path);
        if (selectedPath === p.path) selectedPath = null;
        refreshPresets();
      } catch (err) { alert("删除失败：" + err); }
    };
    li.appendChild(info); li.appendChild(del);
    ul.appendChild(li);
  }
  updateLaunchButtons();
}
function selectedPreset() {
  return lastPresets.find(p => p.path === selectedPath) || null;
}
function updateLaunchButtons() {
  const p = selectedPreset();
  const ok = !!p && p.exists && !state.busy;
  $("btn-launch").disabled = !ok;
  $("btn-bat-only").disabled = !ok;
}
async function refreshPresets() {
  try {
    renderPresets(await Go().ListPresets());
  } catch (e) { /* 列表加载失败不阻塞 UI */ }
}
$("btn-add").onclick = async () => {
  try {
    const path = await Go().AddBat();
    if (path) refreshPresets();
  } catch (e) { /* 用户取消 */ }
};
async function StartModel(path) {
  try {
    const stage = await Go().StartModel(path);
    if (stage === "port-busy") {
      if (confirm("8080 已占用（可能已有模型在运行）。仍要继续（只开 DeepSeek Harness）？")) {
        await Go().EnsureHarness();
        await Go().OpenHarnessBrowser();
      }
    }
  } catch (e) { alert("启动失败：" + e); }
}
async function StartBatOnly(path) {
  try {
    const stage = await Go().StartBatOnly(path);
    if (stage === "port-busy") {
      alert("8080 已占用（可能已有模型在运行），未启动。");
    }
  } catch (e) { alert("启动失败：" + e); }
}
$("btn-skip").onclick = async () => {
  try { await Go().SkipWait(); } catch (e) { alert(String(e)); }
};
// ---- 停止按钮：实时检测模型 / Agent 运行状态，运行中显示、已停止隐藏 ----
// 「一键停止」任一在运行即显示；「停止模型」模型在运行才显示；「停止Agent」Agent 在运行才显示。
function updateStopButtons() {
  Go().Status().then(st => {
    $("btn-stop-model").classList.toggle("hidden", !st.model);
    $("btn-stop-agent").classList.toggle("hidden", !st.agent);
    $("btn-stop-all").classList.toggle("hidden", !(st.model || st.agent));
  }).catch(() => { /* 后端调用失败保持当前显隐，下轮重试 */ });
}
$("btn-stop-all").onclick = async () => {
  try { await Go().StopAll(); } catch (e) { alert(String(e)); }
};
$("btn-stop-model").onclick = async () => {
  try { await Go().StopModelOnly(); } catch (e) { alert(String(e)); }
};
$("btn-stop-agent").onclick = async () => {
  try { await Go().StopAgent(); } catch (e) { alert(String(e)); }
};
updateStopButtons(); // 首屏立即检测一次
setInterval(updateStopButtons, 1500); // 每 1.5s 轮询端口运行状态
// ---- 预设启动（Harness 卡片下方，作用于选中的预设行）----
$("btn-launch").onclick = async () => {
  const p = selectedPreset();
  if (!p || !p.exists || state.busy) return;
  // v1.0.14：默认带上输入框里当前的 DSH 启动参数（非空才持久化；空 = 沿用已保存值）
  const v = $("harness-cmd").value.trim();
  if (v) await Go().SetHarnessCmd(v).catch(() => {});
  StartModel(p.path);
};
$("btn-bat-only").onclick = () => {
  const p = selectedPreset();
  if (!p || !p.exists || state.busy) return;
  StartBatOnly(p.path);
};
// ---- Harness 卡片：自定义命令启动 + 独立 Chrome 页面（v1.0.11 恢复 v1.0.7 前逻辑，移除内嵌 iframe）----
async function initHarnessCmd() {
  try {
    const cfg = await Go().GetConfig();
    if (cfg && cfg.harnessCmd) $("harness-cmd").value = cfg.harnessCmd;
  } catch (e) { /* 保留占位符 */ }
}
// 自定义启动命令输入框：默认收起，点「自定义启动 DeepSeek Harness」展开/收起。
$("btn-harness-custom").onclick = () => {
  $("harness-cmd").classList.toggle("hidden");
  $("btn-harness-custom").classList.toggle("ghost"); // 展开时去掉 ghost → 高亮提示当前状态
};
$("btn-harness").onclick = async () => {
  try {
    await Go().SetHarnessCmd($("harness-cmd").value);
    await Go().EnsureHarness();
    await Go().OpenHarnessBrowser(); // 就绪后跳转独立 Chrome 新页面（Chrome 缺失回退默认浏览器）
  } catch (e) { alert("DeepSeek Harness 启动失败：" + e); }
};
initHarnessCmd();
function doQuit() {
  let stop = false;
  if (state.waiting) {
    stop = confirm("模型服务仍在运行。要一并停止吗？\n（是 = 停止并退出，否 = 仅退出）");
  }
  Go().QuitApp(stop).catch(() => window.runtime.Quit());
}
$("btn-quit").onclick = doQuit;

refreshPresets();

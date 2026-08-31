// 纯决策函数：不碰 DOM / 后端，便于 node 直接测试（frontend/decisions.test.js）。
// 浏览器挂载为 self.DSWDecisions；node 下 require 得到同一对象。
(function (root) {
  const api = {
    // 停止按钮显隐：对应目标运行中 → 显示；已停止 → 隐藏；任一在运行 → 一键停止显示
    stopButtonVisibility(st) {
      const model = !!st.model;
      const agent = !!st.agent;
      return { model: model, agent: agent, all: model || agent };
    },
    // 「启动 DeepSeek Harness」「自定义启动 DeepSeek Harness」：harness 运行中 → 置灰不可点
    harnessButtonsDisabled(st) {
      return !!st.agent;
    },
    // 状态栏空转文本：任一在运行 → 已运行；都停 → 空闲
    idleStatusText(st) {
      return (st.model || st.agent) ? "已运行" : "空闲";
    }
  };
  if (typeof module !== "undefined" && module.exports) {
    module.exports = api;
  } else {
    root.DSWDecisions = api;
  }
})(typeof self !== "undefined" ? self : (typeof window !== "undefined" ? window : this));

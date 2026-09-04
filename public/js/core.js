(function (exports) {
  dayjs.extend(window.dayjs_plugin_relativeTime);
  if ((navigator.language || "").toLowerCase().startsWith("zh")) dayjs.locale("zh-cn");

  function request(url, options) {
    options = options || {};
    return fetch(url, options).then(async function (response) {
      var data;
      try { data = await response.json(); } catch (_) { data = {}; }
      if (!response.ok) throw new Error(data.error || "请求失败（" + response.status + "）");
      return data;
    });
  }

  function upload(url, file, fields, onProgress) {
    return new Promise(function (resolve, reject) {
      var body = new FormData();
      body.append("file", file);
      Object.keys(fields || {}).forEach(function (key) { body.append(key, fields[key]); });
      var xhr = new XMLHttpRequest();
      xhr.open("POST", url);
      xhr.upload.onprogress = function (event) {
        if (event.lengthComputable && onProgress) onProgress(event.loaded / event.total);
      };
      xhr.onload = function () {
        var data = {};
        try { data = JSON.parse(xhr.responseText); } catch (_) {}
        if (xhr.status >= 200 && xhr.status < 300) resolve(data);
        else reject(new Error(data.error || "上传失败（" + xhr.status + "）"));
      };
      xhr.onerror = function () { reject(new Error("网络连接中断，请重试")); };
      xhr.send(body);
    });
  }

  function escapeHTML(value) {
    return String(value == null ? "" : value).replace(/[&<>'"]/g, function (char) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" }[char];
    });
  }

  function size(value) {
    if (value >= 1073741824) return (value / 1073741824).toFixed(2) + " GB";
    if (value >= 1048576) return (value / 1048576).toFixed(2) + " MB";
    return Math.max(0, value / 1024).toFixed(2) + " KB";
  }

  function relative(value) { return dayjs(value).fromNow(); }

  function copy(value) {
    if (navigator.clipboard) return navigator.clipboard.writeText(value);
    var area = document.createElement("textarea");
    area.value = value;
    document.body.appendChild(area);
    area.select();
    document.execCommand("copy");
    area.remove();
    return Promise.resolve();
  }

  function openModal(id) {
    var modal = document.getElementById(id);
    modal.hidden = false;
    var focus = modal.querySelector("input:not([type=hidden]), textarea, button");
    if (focus) setTimeout(function () { focus.focus(); }, 0);
  }

  function closeModal(id) { document.getElementById(id).hidden = true; }

  function showToast(message, danger) {
    var toast = document.getElementById("toast");
    if (!toast) return;
    toast.textContent = message;
    toast.className = "toast visible" + (danger ? " danger" : "");
    clearTimeout(showToast.timer);
    showToast.timer = setTimeout(function () { toast.className = "toast"; }, 2600);
  }

  exports.IPA = { request: request, upload: upload, escape: escapeHTML, size: size, relative: relative, copy: copy, openModal: openModal, closeModal: closeModal, toast: showToast };
})(window);

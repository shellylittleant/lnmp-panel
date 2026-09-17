<script setup lang="ts">
import { computed, h, onMounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  NAlert,
  NButton,
  NCard,
  NCheckbox,
  NDataTable,
  NForm,
  NFormItem,
  NInput,
  NModal,
  NSelect,
  NSpace,
  NUpload,
  useMessage,
} from "naive-ui";
import type { DataTableColumns, UploadFileInfo } from "naive-ui";
import { api, apiForm, panelPrefix } from "../api";
import FileEditor from "./FileEditor.vue";

type Entry = { name: string; path: string; dir: boolean; size: number; mode: string; owner: string; group: string; mtime: string; locked?: boolean };
const msg = useMessage();
const route = useRoute();
const router = useRouter();
const path = ref((route.query.path as string) || "/var/www");
const filter = ref("");
const items = ref<Entry[]>([]);
const checked = ref<Entry[]>([]);
const shortcuts = ref<{ id: number; name: string; root: string }[]>([]);
const users = ref<string[]>([]);
const groups = ref<string[]>([]);
const busy = ref("");
const confirmShow = ref(false);
const confirmText = ref("");
let confirmFn: (() => Promise<void>) | null = null;
const editorShow = ref(false);
const editorPath = ref("");
const editorBody = ref("");
const mkdirName = ref("");
const destPath = ref("");
const chmodShow = ref(false);
const chmodMode = ref("0644");
const chmodOwner = ref("www-data");
const chmodGroup = ref("www-data");
const chmodRec = ref(false);
const warn777 = computed(() => chmodMode.value.replace(/^0+/, "") === "777" || chmodOwner.value === "root");

const columns: DataTableColumns<Entry> = [
  { type: "selection", disabled: (row: Entry) => !!row.locked },
  {
    title: "名称",
    key: "name",
    render(row) {
      if (row.locked) {
        return h("span", { title: "面板自身文件，请走 SSH" }, ["🔒 ", row.name]);
      }
      return row.name;
    },
  },
  { title: "大小", key: "size", width: 100 },
  { title: "权限", key: "mode", width: 80 },
  { title: "属主", key: "owner", width: 100 },
  { title: "属组", key: "group", width: 100 },
  { title: "修改时间", key: "mtime", width: 180 },
];
const hasLocked = computed(() => checked.value.some((x) => x.locked));

const crumbs = computed(() => {
  const p = path.value.replace(/\/+$/, "") || "/";
  const parts = p.split("/").filter(Boolean);
  const out = [{ label: "/", path: "/" }];
  let cur = "";
  for (const x of parts) {
    cur += "/" + x;
    out.push({ label: x, path: cur });
  }
  return out;
});

async function load() {
  const r = await api<{ path: string; items: Entry[] }>("GET", "/files/list?path=" + encodeURIComponent(path.value) + "&q=" + encodeURIComponent(filter.value));
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  path.value = r.data.path;
  items.value = r.data.items || [];
  router.replace({ query: { path: path.value } });
}

onMounted(async () => {
  const s = await api<{ items: typeof shortcuts.value }>("GET", "/files/shortcuts");
  if (s.ok) shortcuts.value = s.data.items || [];
  const u = await api<{ users: string[]; groups: string[] }>("GET", "/files/users");
  if (u.ok) {
    users.value = u.data.users || [];
    groups.value = u.data.groups || [];
  }
  await load();
});

function go(p: string) {
  path.value = p;
  load();
}

function ask(text: string, fn: () => Promise<void>) {
  confirmText.value = text;
  confirmFn = fn;
  confirmShow.value = true;
}

async function doConfirm() {
  confirmShow.value = false;
  if (confirmFn) await confirmFn();
}

async function mkdir() {
  if (!mkdirName.value) return;
  const r = await api("POST", "/files/mkdir", { path: path.value.replace(/\/+$/, "") + "/" + mkdirName.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  mkdirName.value = "";
  await load();
}

async function createFile() {
  const name = mkdirName.value || "untitled.txt";
  const r = await api("POST", "/files/create", { path: path.value.replace(/\/+$/, "") + "/" + name });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  mkdirName.value = "";
  await load();
}

function rejectLocked() {
  if (hasLocked.value) {
    msg.warning("面板自身文件，请走 SSH");
    return true;
  }
  return false;
}

async function del() {
  if (!checked.value.length || rejectLocked()) return;
  ask("确认删除选中的 " + checked.value.length + " 项？不可恢复。", async () => {
    const r = await api("POST", "/files/delete", { paths: checked.value.map((x) => x.path), confirm: true });
    if (!r.ok) msg.error(r.error.message);
    else {
      msg.success("已删除");
      await load();
    }
  });
}

async function copyMove(copy: boolean) {
  if (rejectLocked()) return;
  if (checked.value.length !== 1 || !destPath.value) {
    msg.error("请选一项并填写目标路径");
    return;
  }
  const src = checked.value[0].path;
  const run = async (confirm: boolean) => {
    const r = await api("POST", copy ? "/files/copy" : "/files/move", { src, dest: destPath.value, confirm });
    if (!r.ok && r.error.message.includes("确认覆盖")) {
      ask("目标已存在，确认覆盖？", () => run(true));
      return;
    }
    if (!r.ok) {
      msg.error(r.error.message);
      return;
    }
    msg.success(copy ? "已复制" : "已移动");
    await load();
  };
  await run(false);
}

async function unzip() {
  if (rejectLocked()) return;
  if (checked.value.length !== 1) {
    msg.error("请选一个压缩包");
    return;
  }
  ask("解压可能覆盖同名文件，确认？", async () => {
    const r = await api("POST", "/files/unzip", { path: checked.value[0].path, dest: path.value, confirm: true });
    if (!r.ok) msg.error(r.error.message);
    else {
      msg.success("已解压");
      await load();
    }
  });
}

async function compress() {
  if (!checked.value.length || rejectLocked()) return;
  const dest = path.value.replace(/\/+$/, "") + "/archive.zip";
  const r = await api("POST", "/files/compress", { paths: checked.value.map((x) => x.path), dest });
  if (!r.ok) msg.error(r.error.message);
  else {
    msg.success("已压缩");
    await load();
  }
}

async function openEdit() {
  if (rejectLocked()) return;
  if (checked.value.length !== 1 || checked.value[0].dir) {
    msg.error("请选一个文件");
    return;
  }
  const r = await api<{ content: string }>("GET", "/files/read?path=" + encodeURIComponent(checked.value[0].path));
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  editorPath.value = checked.value[0].path;
  editorBody.value = r.data.content;
  editorShow.value = true;
}

async function saveEdit() {
  const r = await api("PUT", "/files/write", { path: editorPath.value, content: editorBody.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已保存");
  editorShow.value = false;
  await load();
}

async function doChmod() {
  if (!checked.value.length || rejectLocked()) return;
  ask(warn777.value ? "将设为 777 或属主 root，确认继续？" : "确认修改权限？", async () => {
    for (const it of checked.value) {
      const r = await api("POST", "/files/chmod", {
        path: it.path,
        mode: chmodMode.value,
        owner: chmodOwner.value,
        group: chmodGroup.value,
        recursive: chmodRec.value,
      });
      if (!r.ok) {
        msg.error(r.error.message);
        return;
      }
    }
    msg.success("权限已更新");
    chmodShow.value = false;
    await load();
  });
}

function downloadSel() {
  if (rejectLocked()) return;
  if (checked.value.length !== 1) {
    msg.error("请选一项下载");
    return;
  }
  const it = checked.value[0];
  const pfx = panelPrefix();
  const url = it.dir
    ? pfx + "/api/v1/files/zip?path=" + encodeURIComponent(it.path)
    : pfx + "/api/v1/files/download?path=" + encodeURIComponent(it.path);
  window.open(url, "_blank");
}

async function onUpload(files: UploadFileInfo[], confirm = false) {
  if (!files.length) return;
  const form = new FormData();
  form.append("path", path.value);
  form.append("confirm", confirm ? "true" : "false");
  for (const f of files) {
    if (f.file) form.append("file", f.file);
  }
  const r = await apiForm("/files/upload", form);
  if (!r.ok && r.error.message.includes("确认覆盖")) {
    ask("目标已存在，确认覆盖？", () => onUpload(files, true));
    return;
  }
  if (!r.ok) msg.error(r.error.message);
  else {
    msg.success("已上传");
    await load();
  }
}

function onRow(row: Entry) {
  if (row.locked) {
    msg.warning("面板自身文件，请走 SSH");
    return;
  }
  if (row.dir) go(row.path);
}
</script>

<template>
  <n-space vertical>
    <n-card title="文件">
      <n-space>
        <n-select
          :options="shortcuts.map((x) => ({ label: x.name + '（' + x.root + '）', value: x.root }))"
          placeholder="站点快捷跳转"
          clearable
          style="width: 260px"
          @update:value="(v: string) => v && go(v)"
        />
        <n-input v-model:value="path" placeholder="/var/www" style="min-width: 320px" @keyup.enter="load" />
        <n-button @click="load">打开</n-button>
        <n-input v-model:value="filter" placeholder="当前目录过滤" style="width: 160px" @keyup.enter="load" />
      </n-space>
      <p style="margin: 8px 0">
        <a v-for="c in crumbs" :key="c.path" href="#" style="margin-right: 8px" @click.prevent="go(c.path)">{{ c.label }}</a>
      </p>
      <n-space style="margin-bottom: 8px">
        <n-upload multiple :default-upload="false" :show-file-list="false" @change="(e) => onUpload(e.fileList)">
          <n-button>上传到目录</n-button>
        </n-upload>
        <n-input v-model:value="mkdirName" placeholder="新目录或文件名" style="width: 160px" />
        <n-button @click="mkdir">新建目录</n-button>
        <n-button @click="createFile">新建文件</n-button>
        <n-button :disabled="hasLocked" @click="downloadSel">下载</n-button>
        <n-button :disabled="hasLocked" @click="del">删除</n-button>
        <n-input v-model:value="destPath" placeholder="复制/移动目标路径" style="width: 220px" />
        <n-button :disabled="hasLocked" @click="copyMove(true)">复制</n-button>
        <n-button :disabled="hasLocked" @click="copyMove(false)">移动</n-button>
        <n-button :disabled="hasLocked" @click="unzip">解压</n-button>
        <n-button :disabled="hasLocked" @click="compress">压缩 zip</n-button>
        <n-button :disabled="hasLocked" @click="openEdit">编辑</n-button>
        <n-button :disabled="hasLocked" @click="chmodShow = true">权限</n-button>
        <span v-if="busy">{{ busy }}</span>
      </n-space>
      <n-data-table :columns="columns" :data="items" :row-key="(r: Entry) => r.path" @update:checked-row-keys="(keys: string[]) => (checked = items.filter((x) => keys.includes(x.path)))" :row-props="(row: Entry) => ({ onDblclick: () => onRow(row), onClick: () => row.locked && msg.warning('面板自身文件，请走 SSH') })" />
    </n-card>
    <n-modal v-model:show="confirmShow">
      <n-card title="请确认" style="width: 420px">
        <p>{{ confirmText }}</p>
        <n-space>
          <n-button type="primary" @click="doConfirm">确认</n-button>
          <n-button @click="confirmShow = false">取消</n-button>
        </n-space>
      </n-card>
    </n-modal>
    <n-modal v-model:show="editorShow" :style="{ width: '80vw' }">
      <n-card :title="'编辑 ' + editorPath">
        <FileEditor v-model="editorBody" :filename="editorPath" />
        <n-space style="margin-top: 8px">
          <n-button type="primary" @click="saveEdit">保存</n-button>
          <n-button @click="editorShow = false">关闭</n-button>
        </n-space>
      </n-card>
    </n-modal>
    <n-modal v-model:show="chmodShow">
      <n-card title="权限" style="width: 480px">
        <n-alert v-if="warn777" type="warning">设 777 或属主改为 root 有风险，但不阻止。</n-alert>
        <n-form>
          <n-form-item label="模式（八进制）"><n-input v-model:value="chmodMode" /></n-form-item>
          <n-form-item label="属主"><n-select v-model:value="chmodOwner" :options="users.map((x) => ({ label: x, value: x }))" filterable /></n-form-item>
          <n-form-item label="属组"><n-select v-model:value="chmodGroup" :options="groups.map((x) => ({ label: x, value: x }))" filterable /></n-form-item>
          <n-checkbox v-model:checked="chmodRec">应用到子目录与文件</n-checkbox>
        </n-form>
        <n-space style="margin-top: 8px">
          <n-button type="primary" @click="doChmod">保存</n-button>
          <n-button @click="chmodShow = false">取消</n-button>
        </n-space>
      </n-card>
    </n-modal>
  </n-space>
</template>
